package bencode

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"
)

const (
	PRINT_HASHES = false
)

type Torrent struct {
	info map[string]BNode
}

func NewTorrent(infoMap map[string]BNode) *Torrent {
	return &Torrent{
		info: infoMap,
	}
}

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

func (t *Torrent) VerifyFile(filename string) (bool, error) {
	stat, err := os.Stat(filename)
	if err != nil {
		return false, err
	}
	idx := locateIndex(t.info, filename, stat.Size())
	if idx >= 0 {
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
				b := GetHash(h, chunk)
				if 0 == bytes.Compare(b, fileHash) {
					log.Printf("verified by file-hash")
					return true, nil
				}
			}
			//log.Printf("<%v>", hex.EncodeToString())
		}
	}

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
