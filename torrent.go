package bencode

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	NotLongEnoughError   = errors.New("shorter than piece length")
	FileNotIncludedError = errors.New("file not included in file list")
)

const (
	PRINT_HASHES = false
)

const (
	SINGLE_THREAD = true
)

var (
	ENABLE_BY_SINGLE_FILE_HASH = true
)

func SetSingleHashEnabled(enabled bool) {
	ENABLE_BY_SINGLE_FILE_HASH = enabled
	if enabled {
		log.Printf("single-hash enabled")
	}
}

type Torrent struct {
	info map[string]BNode
}

func NewTorrent(infoMap map[string]BNode) *Torrent {
	return &Torrent{
		info: infoMap,
	}
}

// Print the summary info for the torrent file
func (t *Torrent) PrintSummary() {
	defer func() {
		if e := recover(); e != nil {
			fmt.Printf("Error: %v\n", e)
		}
	}()
	info := t.info
	files := info["files"]
	for _, file := range files.List {
		length := *file.Map["length"].Int
		var pathes []string
		for _, name := range file.Map["path"].List {
			pathes = append(pathes, *name.Str)
		}
		pathstr := strings.Join(pathes, "/")
		fmt.Printf("%16s byte(s)\t%v\n", strconv.FormatInt(length, 10), pathstr)
	}

	fmt.Println()

	pieceLength := *info["piece length"].Int
	fmt.Printf("%24s:\t%v\n", "piece length", pieceLength)

	pieces := info["pieces"].Binary
	pieceBinLength := len(pieces)

	fmt.Printf("%24s:\t%v\n", "piece sha1 length", pieceBinLength)
	fmt.Printf("%24s:\t%v(%v)\n", "blocks count", pieceBinLength/20, float64(pieceBinLength)/20.0)
	fmt.Println()
}

// locate by name
// 1. original name(path joined)
// 2. the last one
func locateIndex(info map[string]BNode, filename string, size int64) (idx int, lengthBefore int64) {

	if _, ok := info["files"]; !ok {
		if size == info["length"].AsInt() {
			return 0, 0
		}
	}

	//
	fileInfos := info["files"].AsList()
	idx = -1
	for index, file := range fileInfos {
		fileinfo := file.AsMap()

		// Check length if matches
		// This must be satisfied.
		if fileinfo["length"].AsInt() != size {
			continue
		}

		paths := fileinfo["path"].AsList()
		name := paths[len(paths)-1].AsString()
		if name == filename {
			idx = index
			break
		}

		// not strictly
		if path.Ext(name) == path.Ext(filename) {
			idx = index
			break
		}
	}

	if idx >= 0 {
		for i, file := range fileInfos {
			if i >= idx {
				break
			}
			lengthBefore += file.AsMap()["length"].AsInt()
		}
	}
	return
}

func locateFile(info map[string]BNode, index int) (string, int64) {
	fi := info["files"].AsList()[index].AsMap()
	pathLS := fi["path"].AsList()
	var paths []string
	for _, el := range pathLS {
		paths = append(paths, el.AsString())
	}
	length := fi["length"].AsInt()
	return path.Join(paths...), length
}

func (t *Torrent) GetTotalLength() int64 {
	var totLength int64
	for _, file := range t.info["files"].AsList() {
		fi := file.AsMap()
		totLength += fi["length"].AsInt()
	}
	return totLength
}

// return if hash exists/ verified ok
func (t *Torrent) tryVerifyByHashInfo(idx int, filename string) (bool, error) {
	// log.Printf("Index = %v\n", idx)
	// log.Printf("%v\n", t.info["files"].AsList()[idx])
	fi := t.info["files"].AsList()[idx].AsMap()
	if _, ok := fi["filehash"]; ok {
		// log.Printf("file has a hash value.")
		chunk, err := ioutil.ReadFile(filename)
		if err != nil {
			return false, err
		}

		if PRINT_HASHES {
			PrintHash(md5.New(), chunk, "MD5")
			PrintHash(sha1.New(), chunk, "SHA1")
			PrintHash(sha256.New(), chunk, "SHA256")
		}

		fileHash := []byte(fi["filehash"].AsString())
		for _, h := range []hash.Hash{md5.New(), sha1.New(), sha256.New()} {
			b := getHash(h, chunk)
			if 0 == bytes.Compare(b, fileHash) {
				log.Printf("verified by file-hash: %T", h)
				return true, nil
			}
		}
		//log.Printf("<%v>", hex.EncodeToString())
	}
	return false, nil
}

func (t *Torrent) checkMain(filename string,
	lengthBefore, thisLength, pieceLength int64, pieces []byte) (
	okPieces, headPiece, tailPiece,
	notOkPiece, totCount int32, startBlock, endBlock int) {

	startBlock = int(lengthBefore / pieceLength)
	endBlock = int((lengthBefore + thisLength) / pieceLength)
	if (lengthBefore+thisLength)%pieceLength != 0 {
		endBlock++
	}

	psLen := int(pieceLength)
	startOffset := int(lengthBefore) % psLen

	log.Printf("starting block: %v", startBlock)
	log.Printf("end block: %v", endBlock)
	log.Printf("piece-length: %v", psLen)

	var wg sync.WaitGroup

	cpuNu := runtime.NumCPU()
	if SINGLE_THREAD {
		log.Printf("using single thread")
		cpuNu = 1
	}
	// taskID range: 0 ~ cpuNu-1
	doTask := func(taskID int, rOff int, pPassed, pMissHead, pMissTail, pFailed, inAll *int32) {
		defer wg.Done()
		fin, err := os.Open(filename)
		if err != nil {
			panic(err)
		}
		defer fin.Close()

		buffer := make([]byte, psLen)
		var passed, headMissing, tailMissing, failed int32
		var blockTot int32
		for i := startBlock + taskID; i < endBlock; i += cpuNu {
			off, readPos := 0, int64(i)*pieceLength
			if i != startBlock {
				readPos -= int64(rOff)
			} else {
				off = rOff
			}
			read, err := fin.ReadAt(buffer[off:], readPos)
			if err != nil && err != io.EOF {
				log.Printf("readat: %v\n", err)
			}
			pad := psLen - (read + off)
			for j := 0; j < pad; j++ {
				buffer[read+off+j] = 0
			}
			that := pieces[i*20 : i*20+20]
			result := calcSha1Hash(buffer)

			//
			for {
				if failed > 10 {
					if bytes.Compare(that, result) == 0 {
						break
					}
					log.Printf("try md5 for each piece")
					res1 := calcMd5Hash(buffer)
					if bytes.Compare(that, res1) == 0 {
						log.Fatalf("md5 seesm to be the right choice....")
					}
					log.Printf("try sha256 for each piece")
					res2 := calcSha256Hash(buffer)
					if bytes.Compare(that, res2) == 0 {
						log.Fatalf("sha256 seems to be the right choice...")
					}

					log.Printf("dump info:")
					log.Printf("bytes-array for pieces(in bytes): %v", len(pieces))
					if len(pieces)%20 != 0 {
						log.Printf("cannot be divided by 20")
					}
					pieceCount := len(pieces) / 20
					log.Printf("piece count: %v", pieceCount)

					log.Printf("length for each-piece: %v", pieceLength)
					volumeForFiles := pieceLength * int64(pieceCount)
					fileVol := t.GetTotalLength()
					log.Printf("volume: %v", volumeForFiles)
					log.Printf("all files: %v byte(s)", fileVol)
					log.Printf("margin percent: %.2f%%", float64(volumeForFiles-fileVol)/float64(volumeForFiles)*100.0)
					log.Fatalf("too many errors")
				}
				break
			}

			isTailMissing := false
			if bytes.Compare(that, result) == 0 {
				// log.Printf("# <%v>piece ok: read %v, block<%v>", taskID, read, i)
				passed++
			} else if off > 0 {
				headMissing++
			} else if read+off < psLen {
				// log.Printf("tail failed due to read+off<psLen")
				log.Printf("encounter tail. read %v byte(s)", read)
				isTailMissing = true
			} else {
				failed++
				log.Printf("%v compared to %v", result, that)
			}

			// a second chance
			if isTailMissing {
				if bytes.Compare(that, calcSha1Hash(buffer[:read+off])) == 0 {
					log.Printf("## tail fixed by incomplete buffer !##")
					passed++
				} else {
					tailMissing++
				}
			}

			blockTot++
		}

		atomic.AddInt32(pPassed, passed)
		atomic.AddInt32(pMissHead, headMissing)
		atomic.AddInt32(pMissTail, tailMissing)
		atomic.AddInt32(pFailed, failed)
		atomic.AddInt32(inAll, blockTot)
	}

	for i := 0; i < cpuNu; i++ {
		wg.Add(1)
		go doTask(i, startOffset, &okPieces, &headPiece, &tailPiece, &notOkPiece, &totCount)
	}
	wg.Wait()
	log.Printf("###################################")
	return
}

func (t *Torrent) fixHeadTail(okPieces, headPiece, tailPiece, notOkPiece int32,
	filename string,
	idx int,
	pieceLength int64,
	prevMargin, postMargin int64,
	pieces []byte,
	startBlock, endBlock int) (int32, int32, int32, int32) {
	if headPiece > 0 || tailPiece > 0 {
		log.Printf("### fixing with head-piece: %v, tail-piece: %v ###", headPiece, tailPiece)
		log.Printf("### prev-margin: %v, post-margin: %v", prevMargin, postMargin)
		fm := NewFileMan()
		fileInfos := t.info["files"].AsList()

		// Processing with the head
		if headPiece > 0 {
			func() {
				log.Printf("## starting fixing head ...")
				// make up one piece
				var headBuff []byte
				remainPrev := int64(prevMargin)
				for elIdx := idx - 1; elIdx >= 0 && remainPrev > 0; elIdx-- {
					origin, length := locateFile(t.info, elIdx)
					that, ok := fm.Lookup(origin, length)
					if !ok {
						log.Printf("cannot find %v", origin)
						return
					}

					log.Printf("  reading from %v", that)
					if remainPrev >= length {
						log.Printf("    %v byte(s)", length)
					} else {
						log.Printf("    %v byte(s)", remainPrev)
					}

					if remainPrev >= length {
						chunk, err := ioutil.ReadFile(that)
						if err != nil {
							log.Printf("cannnot read %v", that)
							return
						}
						if int64(len(chunk)) != length {
							log.Printf("length expected: %v", length)
						}
						headBuff = append(chunk, headBuff...)
					} else {
						// Read the last remainPrev bytes only
						fin, err := os.Open(that)
						if err != nil {
							log.Printf("cannot read %v", that)
							return
						}
						defer fin.Close()
						buff := make([]byte, int(remainPrev))
						fin.ReadAt(buff, length-remainPrev)
						fin.Close()
						headBuff = append(buff, headBuff...)
					}
					remainPrev -= length
					elIdx--
				}
				inFront := int(pieceLength - prevMargin)
				frontBytes := make([]byte, inFront)
				fin, _ := os.Open(filename)
				defer fin.Close()
				nRead, err := fin.Read(frontBytes)
				if err != nil || nRead != inFront {
					log.Printf("read front error")
					return
				}
				headBuff = append(headBuff, frontBytes...)

				result := calcSha1Hash(headBuff)
				that := pieces[startBlock*20 : startBlock*20+20]
				if bytes.Compare(that, result) == 0 {
					log.Printf("### head fixed successful ###")
					headPiece--
					okPieces++
				}
			}()
		}

		// Processing with the tail
		// One exception: there is only one file in the torrent list
		if tailPiece > 0 {
			func() {
				log.Printf("## starting fixing tail ...")
				var tailBuff []byte
				remainPost := postMargin
				for elIdx := idx + 1; elIdx < len(fileInfos) && remainPost > 0; elIdx++ {
					origin, length := locateFile(t.info, elIdx)
					that, ok := fm.Lookup(origin, length)
					if !ok {
						log.Printf("cannot find %v", origin)
						return
					}

					log.Printf("  reading from %v", that)
					if remainPost >= length {
						log.Printf("    %v byte(s)", length)
					} else {
						log.Printf("    %v byte(s)", remainPost)
					}

					if remainPost >= length {
						chunk, err := ioutil.ReadFile(that)
						if err != nil {
							log.Printf("cannot read %v", that)
							return
						}
						tailBuff = append(tailBuff, chunk...)
					} else {
						fin, err := os.Open(that)
						if err != nil {
							log.Printf("cannot open %v", that)
							return
						}
						defer fin.Close()
						var buff = make([]byte, int(remainPost))
						nRead, err := fin.Read(buff)
						if nRead != int(remainPost) || err != nil {
							log.Printf("read %v error", that)
							return
						}
						tailBuff = append(tailBuff, buff...)
					}
				}

				tailLength := pieceLength - postMargin
				fin, _ := os.Open(filename)
				defer fin.Close()
				_, err := fin.Seek(-tailLength, 2)
				if err != nil {
					log.Printf("seek failed: %v", err)
					return
				}
				var inRear = make([]byte, tailLength)
				nRead, err := fin.Read(inRear)
				if err != nil || nRead != int(tailLength) {
					log.Printf("read failed: %v", err)
					return
				}
				tailBuff = append(inRear, tailBuff...)
				result := calcSha1Hash(tailBuff)
				thatPiece := pieces[(endBlock-1)*20 : endBlock*20]
				if bytes.Compare(result, thatPiece) == 0 {
					log.Printf("  tail hash: %x", result)
					// log.Printf("  piece info: %v", hex.EncodeToString(thatPiece))

					scratch := 20
					if scratch > len(tailBuff) {
						scratch = len(tailBuff)
					}
					log.Printf(" first %v bytes: %x", scratch, tailBuff[:scratch])
					log.Printf("### tail fix successfully ###")
					tailPiece--
					okPieces++
				}
			}()
		}
	}

	return okPieces, headPiece, tailPiece, notOkPiece
}

// verify single file. do not verify all.
func (t *Torrent) VerifyFile(filename string) (bool, error) {
	stat, err := os.Stat(filename)
	if err != nil {
		return false, err
	}

	log.Printf("%v", filename)
	log.Printf("file length: %v", stat.Size())
	thisLength := stat.Size()
	idx, lengthBefore := locateIndex(t.info, filename, stat.Size())
	if idx < 0 {
		return false, FileNotIncludedError
	}

	log.Printf("File-idx<%v>", idx)

	if ENABLE_BY_SINGLE_FILE_HASH {
		log.Printf("try verifying by filehash ...")
		hashOK, _ := t.tryVerifyByHashInfo(idx, filename)
		if hashOK {
			return true, nil
		}
		log.Printf("verifying by filehash unavailable")
	}

	// piece length must be placed in the front
	pieceLength := t.info["piece length"].AsInt()

	// presume that prevMargin is less than 4GB
	var prevMargin int64 = lengthBefore % pieceLength
	pieces := t.info["pieces"].AsBinary()
	var postMargin int64 = 0
	if 0 != (lengthBefore+thisLength)%pieceLength {
		postMargin = pieceLength - (lengthBefore+thisLength)%pieceLength
	}

	okPieces, headPiece, tailPiece,
		notOkPiece, totCount,
		startBlock, endBlock := t.checkMain(filename, lengthBefore,
		thisLength, pieceLength, pieces)

	// try to load the next file
	// if pieceLength > thisLength {
	// 	return false, NotLongEnoughError
	// }

	log.Printf("## Currently ##: passed:%v, head-missing:%v, tail-missing:%v, failed:%v", okPieces, headPiece, tailPiece, notOkPiece)

	if headPiece > 1 {
		log.Fatal("head-piece > 1")
	}
	if tailPiece > 1 {
		log.Fatal("tail-piece > 1")
	}

	//
	okPieces, headPiece, tailPiece, notOkPiece = t.fixHeadTail(okPieces, headPiece, tailPiece, notOkPiece,
		filename, idx, pieceLength,
		prevMargin, postMargin,
		pieces,
		startBlock, endBlock)

	log.Printf("")
	log.Printf("%v in all", totCount)
	log.Printf("## Final ## passed:%v, head-missing:%v, tail-missing:%v, failed:%v", okPieces, headPiece, tailPiece, notOkPiece)
	return notOkPiece == 0 && 0 == headPiece && 0 == tailPiece, nil
}

func (t *Torrent) GetFileList() []string {
	fileInfos := t.info["files"].AsList()
	var rvs []string
	for _, v := range fileInfos {
		pathlist := v.AsMap()["path"].AsList()
		var ps []string
		for _, p := range pathlist {
			ps = append(ps, p.AsString())
		}
		rvs = append(rvs, strings.Join(ps, "/"))
	}
	return rvs
}

func (t *Torrent) VerifyAll() (bool, error) {
	// try to verified by pieces
	//log.Printf("trying to verify by pieces")
	pieces := t.info["pieces"].AsBinary()
	blockCount := len(pieces) / 20
	//log.Printf("block count is %v", blockCount)

	fileInfos := t.info["files"].AsList()

	pieceLength := t.info["piece length"].AsInt()
	//fmt.Printf("piece length is %v\n", pieceLength)

	var thisRemains int64 = 0
	//var iLeft int64 = t.GetTotalLength()
	iFileIdx := 0
	myBuffer := bytes.NewBuffer(nil)
	passed, failed := 0, 0
	var curFin *os.File
	var zeroBuffer *bytes.Buffer
	//failedDueToMissing := 0

	totFileCount := len(fileInfos)
	tempBuff := make([]byte, pieceLength)

	for i := 0; i < blockCount; i++ {
		for myBuffer.Len() < int(pieceLength) {
			if thisRemains <= 0 && nil == curFin && iFileIdx < totFileCount {
				lengthForThisFile := fileInfos[iFileIdx].AsMap()["length"].AsInt()
				thisRemains = lengthForThisFile
				curFin = loadFile(fileInfos[iFileIdx].AsMap())
				iFileIdx++
				if curFin == nil {
					zeroBuffer = bytes.NewBuffer(make([]byte, lengthForThisFile))
				}
			}
			if thisRemains <= 0 && iFileIdx >= totFileCount {
				break
			}
			var nRead int
			var readErr error
			if nil == curFin {
				nRead, readErr = zeroBuffer.Read(tempBuff)
			} else {
				nRead, readErr = curFin.Read(tempBuff)
			}

			// do trimming
			if int64(nRead) > thisRemains {
				// only if the bencode record is misleading as the actual lenght is longer than its record.
				log.Printf("SO WEIRED THIS SHALL NOT HAPPEN")
				nRead = int(thisRemains)
			}
			takesBuff := tempBuff[0:nRead]
			thisRemains -= int64(nRead)

			// reaching end. but buffer is not long enough,
			// do some padding. weird.
			if readErr == io.EOF && thisRemains > 0 {
				padding := make([]byte, thisRemains)
				takesBuff = append(takesBuff, padding...)
			}

			if thisRemains == 0 || readErr == io.EOF {
				curFin.Close()
				curFin = nil
			}
			// move to working buffer
			myBuffer.Write(takesBuff)
		}
		// fmt.Printf("one piece\n")
		thisSeg := make([]byte, pieceLength)
		myBuffer.Read(thisSeg) // read as much as possible
		sha1hash := sha1.New()
		sha1hash.Write(thisSeg) // no padding needed
		result := sha1hash.Sum(nil)
		thisPiece := pieces[i*20 : i*20+20]
		if 0 == bytes.Compare(result, thisPiece) {
			passed++
			// fmt.Printf(".")
		} else {
			failed++
			fmt.Printf("block<%d> Target<%v> Current<%v>\n", i,
				hex.EncodeToString(thisPiece),
				hex.EncodeToString(result),
			)
		}
	}

	// defense
	if nil != curFin {
		curFin.Close()
		curFin = nil
	}

	fmt.Printf("(passed/all) (%v/%v)\n", passed, blockCount)
	return true, nil
}

// func loadLoadChunk(fileinfo map[string]BNode) []byte {
// 	pathLs := fileinfo["path"].AsList()

// 	var pathArr []string
// 	for _, v := range pathLs {
// 		pathArr = append(pathArr, v.AsString())
// 	}

// 	tl := len(pathArr)
// 	for i := len(pathArr); i >= 1; i-- {
// 		p := strings.Join(pathArr[tl-i:tl], "/")
// 		data, err := ioutil.ReadFile(p)
// 		if err == nil {
// 			log.Printf("loading <%v> done", p)
// 			return data
// 		}
// 	}
// 	return nil
// }

func toPathName(fileinfo map[string]BNode) string {
	pathLs := fileinfo["path"].AsList()
	var pathArr []string
	for _, v := range pathLs {
		pathArr = append(pathArr, v.AsString())
	}
	return path.Join(pathArr...)
}

func loadFile(fileinfo map[string]BNode) *os.File {
	pathLs := fileinfo["path"].AsList()
	var pathArr []string
	for _, v := range pathLs {
		pathArr = append(pathArr, v.AsString())
	}
	tl := len(pathArr)
	for i := len(pathArr); i >= 1; i-- {
		p := strings.Join(pathArr[tl-i:tl], "/")
		fin, err := os.Open(p)
		if err == nil {
			log.Printf("loading <%v> ok", p)
			return fin
		}
	}
	return nil
}

func PrintHash(h hash.Hash, chunk []byte, name string) {
	h.Write(chunk)
	hashed := h.Sum(nil)
	log.Printf("%v para este: %v", name, hex.EncodeToString(hashed))
}

func getHash(h hash.Hash, data []byte) []byte {
	h.Write(data)
	return h.Sum(nil)
}

func calcSha1Hash(buffer []byte) []byte {
	hash := sha1.New()
	hash.Write(buffer)
	return hash.Sum(nil)
}

func calcMd5Hash(buffer []byte) []byte {
	hash := md5.New()
	hash.Write(buffer)
	return hash.Sum(nil)
}

func calcSha256Hash(buffer []byte) []byte {
	hash := sha256.New()
	hash.Write(buffer)
	return hash.Sum(buffer)
}
