package bencode

//https://en.wikipedia.org/wiki/Torrent_file#Single_file

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	BNodeInvalidType = iota
	BNodeMap
	BNodeString
	BNodeList
	BNodeInteger
	BNodeBinary
)

type BNode struct {
	Map    map[string]BNode
	Str    *string
	Int    *int64
	List   []BNode
	Binary []byte

	Cat int
}

var TypeError = errors.New("error type")

func (b BNode) AsList() []BNode {
	if b.Cat != BNodeList {
		panic(TypeError)
	}
	return b.List
}

func (b BNode) AsMap() map[string]BNode {
	if b.Cat != BNodeMap {
		panic(TypeError)
	}
	return b.Map
}

func (b BNode) AsInt() int64 {
	if b.Cat != BNodeInteger {
		panic(TypeError)
	}
	return *b.Int
}

func (b BNode) AsString() string {
	if b.Cat != BNodeString {
		panic(TypeError)
	}
	return *b.Str
}

func (b BNode) AsBinary() []byte {
	if b.Cat != BNodeBinary {
		panic(TypeError)
	}
	return b.Binary
}

func PrintTorrent(root BNode) {
	info := root.Map["info"]
	files := info.Map["files"]
	for _, file := range files.List {
		length := *file.Map["length"].Int
		var pathes []string
		for _, name := range file.Map["path"].List {
			pathes = append(pathes, *name.Str)
		}
		pathstr := strings.Join(pathes, "/")
		fmt.Printf("%v, length=%v\n", pathstr, length)
	}

	pieceLength := *info.Map["piece length"].Int
	fmt.Printf("piece length is %v\n", pieceLength)

	pieces := info.Map["pieces"].Binary
	pieceBinLength := len(pieces)
	fmt.Printf("piece sha1 length:%v\n", pieceBinLength)
	fmt.Printf("blocks count: %v(%v)\n", pieceBinLength/20, float64(pieceBinLength)/20.0)
}

func PrintNode(node BNode, indent int) {
	switch node.Cat {
	case BNodeBinary:
		fmt.Printf("%v***\n", strings.Repeat(" ", indent*2))
	case BNodeString:
		fmt.Printf("%v%v\n", strings.Repeat(" ", indent*2), *node.Str)
	case BNodeInteger:
		fmt.Printf("%v%v\n", strings.Repeat(" ", indent*2), *node.Int)
	case BNodeList:
		if len(node.List) == 0 {
			fmt.Printf("%v[]\n", strings.Repeat(" ", indent*2))
		} else {
			fmt.Printf("%v[\n", strings.Repeat(" ", indent*2))
			for _, b := range node.List {
				PrintNode(b, indent+1)
			}
			fmt.Printf("%v]\n", strings.Repeat(" ", indent*2))
		}
	case BNodeMap:
		if len(node.Map) == 0 {
			fmt.Printf("%v{}\n", strings.Repeat(" ", indent*2))
		} else {
			fmt.Printf("%v{\n", strings.Repeat(" ", indent*2))
			for k, v := range node.Map {
				fmt.Printf("%v<%v>: ", strings.Repeat(" ", (indent+1)*2), k)
				if v.Cat == BNodeString || v.Cat == BNodeInteger {
					PrintNode(v, 0)
				} else {
					fmt.Printf("\n")
					PrintNode(v, indent+2)
				}
			}
			fmt.Printf("%v}\n", strings.Repeat(" ", indent*2))
		}
	default:
		panic("Unknown cat")
	}
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func scanMap(raw []byte) (map[string]BNode, []byte) {
	///defer fmt.Println("map")
	m := make(map[string]BNode)
	for raw[0] != 'e' {
		key, nRaw := scanString(raw)

		var valueNode BNode
		var rRaw []byte
		if key != "pieces" {
			valueNode, rRaw = Scan(nRaw)
		} else {
			var bytes []byte
			bytes, rRaw = scanBinaryString(nRaw)
			valueNode = BNode{
				Binary: bytes,
				Cat:    BNodeBinary,
			}
		}
		m[key] = valueNode
		raw = rRaw
	}
	return m, raw[1:]
}

// list can be empty
func scanList(raw []byte) ([]BNode, []byte) {
	//defer fmt.Println("list")
	var els []BNode
	// if raw[0] == 'e' {
	// 	//fmt.Printf("zero-length list\n")
	// }
	for raw[0] != 'e' {
		newNode, nRaw := Scan(raw)
		raw = nRaw
		els = append(els, newNode)
	}
	return els, raw[1:]
}

// length:string-content
func scanString(raw []byte) (string, []byte) {
	//defer fmt.Println("string")
	i := 0
	var lenS string
	for ; isDigit(raw[i]); i++ {
		lenS += string(raw[i])
	}
	if raw[i] != ':' {
		fmt.Printf("lenS = <%v>\n", lenS)
		fmt.Printf("%v\n", string(raw))
		panic("not a : for string")
	}
	i++
	length, err := strconv.Atoi(lenS)
	if err != nil {
		panic(err)
	}
	str := string(raw[i : i+length])
	return str, raw[i+length:]
}

func scanBinaryString(raw []byte) ([]byte, []byte) {
	//defer fmt.Println("string")
	i := 0
	var lenS string
	for ; isDigit(raw[i]); i++ {
		lenS += string(raw[i])
	}
	if raw[i] != ':' {
		fmt.Printf("lenS = <%v>\n", lenS)
		fmt.Printf("%v\n", string(raw))
		panic("not a : for string")
	}
	i++
	length, err := strconv.Atoi(lenS)
	if err != nil {
		panic(err)
	}
	return raw[i : i+length], raw[i+length:]
}

func scanInteger(raw []byte) (int64, []byte) {
	//defer fmt.Println("integer")
	var str string
	i := 0
	for raw[i] != 'e' {
		str += string(raw[i])
		i++
	}
	if raw[i] != 'e' {
		panic("Expecting an `e' for integer")
	}
	v, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		panic(err)
	}
	return v, raw[i+1:]
}

func Scan(raw []byte) (BNode, []byte) {
	//defer fmt.Println("Scan")
	switch {
	case 'd' == raw[0]:
		dict, nRaw := scanMap(raw[1:])
		return BNode{
			Map: dict,
			Cat: BNodeMap,
		}, nRaw
	case 'l' == raw[0]:
		lst, nRaw := scanList(raw[1:])
		return BNode{
			List: lst,
			Cat:  BNodeList,
		}, nRaw
	case isDigit(raw[0]):
		str, nRaw := scanString(raw)
		return BNode{
			Str: &str,
			Cat: BNodeString,
		}, nRaw

	case 'i' == raw[0]:
		v, nRaw := scanInteger(raw[1:])
		return BNode{
			Int: &v,
			Cat: BNodeInteger,
		}, nRaw
	default:
		fmt.Printf("raw reset:(%v) %v\n", len(raw), string(raw))
		panic(fmt.Errorf("unknown format:<%v>", raw[0]))
	}
}
