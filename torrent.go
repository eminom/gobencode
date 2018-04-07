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
	NotLongEnough = errors.New("shorter than piece length")
)

const (
	PRINT_HASHES               = false
	ENABLE_BY_SINGLE_FILE_HASH = false
)

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
func locateIndex(info map[string]BNode, filename string, size int64) int {
	files := info["files"].AsList()
	for idx, file := range files {
		fileinfo := file.AsMap()

		paths := fileinfo["path"].AsList()

		name := paths[len(paths)-1].AsString()
		if name == filename {
			return idx
		}

		if path.Ext(name) == path.Ext(filename) && size == int64(fileinfo["length"].AsInt()) {
			return idx
		}
	}
	return -1
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
func (t *Torrent) tryVerifyByHashInfo(idx int, filename string) (bool, bool, error) {
	if idx < 0 {
		return false, false, nil
	}
	if !ENABLE_BY_SINGLE_FILE_HASH {
		return false, false, nil
	}

	// log.Printf("Index = %v\n", idx)
	// log.Printf("%v\n", t.info["files"].AsList()[idx])
	fi := t.info["files"].AsList()[idx].AsMap()
	if _, ok := fi["filehash"]; ok {
		// log.Printf("file has a hash value.")
		chunk, err := ioutil.ReadFile(filename)
		if err != nil {
			return true, false, err
		}

		if PRINT_HASHES {
			PrintHash(md5.New(), chunk, "MD5")
			PrintHash(sha1.New(), chunk, "SHA1")
			PrintHash(sha256.New(), chunk, "SHA256")
		}

		fileHash := []byte(fi["filehash"].AsString())
		for _, h := range []hash.Hash{md5.New(), sha1.New(), sha256.New()} {
			b := GetHash(h, chunk)
			if 0 == bytes.Compare(b, fileHash) {
				log.Printf("verified by file-hash: %T", h)
				return true, true, nil
			}
		}
		//log.Printf("<%v>", hex.EncodeToString())
	}
	return false, false, nil
}

// verify single file. do not verify all.
func (t *Torrent) VerifyFile(filename string) (bool, error) {
	stat, err := os.Stat(filename)
	if err != nil {
		return false, err
	}

	idx := locateIndex(t.info, filename, stat.Size())
	hashExists, hashVerified, hErr := t.tryVerifyByHashInfo(idx, filename)
	if nil == hErr && hashExists && hashVerified {
		return true, nil
	}
	if idx < 0 {
		return false, fmt.Errorf("not enrolled in file list")
	}

	fileInfos := t.info["files"].AsList()

	thisFileInfo := t.info["files"].AsList()[idx].AsMap()

	var lengthBefore int64
	for i, file := range fileInfos {
		if i >= idx {
			break
		}
		lengthBefore += file.AsMap()["length"].AsInt()
	}
	pieceLength := t.info["piece length"].AsInt()
	pieces := t.info["pieces"].AsBinary()

	thisLength := thisFileInfo["length"].AsInt()
	if pieceLength > thisLength {
		return false, NotLongEnough
	}

	startBlock := int(lengthBefore / pieceLength)
	endBlock := int((lengthBefore + thisLength) / pieceLength)
	if (lengthBefore+thisLength)%pieceLength != 0 {
		endBlock++
	}

	psLen := int(pieceLength)
	startOffset := int(lengthBefore) % psLen

	var wg sync.WaitGroup

	cpuNu := runtime.NumCPU()
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
			hash := sha1.New()
			hash.Write(buffer)
			result := hash.Sum(nil)
			if bytes.Compare(that, result) == 0 {
				passed++
			} else if off > 0 {
				headMissing++
			} else if read+off < psLen {
				tailMissing++
			} else {
				failed++
				log.Printf("%v compared to %v", result, that)
			}
			blockTot++
		}

		atomic.AddInt32(pPassed, passed)
		atomic.AddInt32(pMissHead, headMissing)
		atomic.AddInt32(pMissTail, tailMissing)
		atomic.AddInt32(pFailed, failed)
		atomic.AddInt32(inAll, blockTot)
	}

	var okPieces, headPiece, tailPiece, notOkPiece, totCount int32
	for i := 0; i < cpuNu; i++ {
		wg.Add(1)
		go doTask(i, startOffset, &okPieces, &headPiece, &tailPiece, &notOkPiece, &totCount)
	}
	wg.Wait()
	log.Printf("passed:%v head-missing:%v tail-missing:%v failed:%v", okPieces, headPiece, tailPiece, notOkPiece)
	log.Printf("%v in all", totCount)
	return notOkPiece == 0, nil
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

func GetHash(h hash.Hash, data []byte) []byte {
	h.Write(data)
	return h.Sum(nil)
}
