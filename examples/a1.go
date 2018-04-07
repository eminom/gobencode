package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"github.com/eminom/gobencode"
)

func main() {
	log.SetFlags(log.Lshortfile)
	filename := "1000.torrent"
	chunk, err := ioutil.ReadFile(filename)
	if err != nil {
		panic(err)
	}

	node, left := bencode.Scan(chunk)
	if len(left) > 0 {
		fmt.Printf("warning: still some left\n")
	}
	// bencode.PrintNode(node, 0)
	// fmt.Println()

	t := bencode.NewTorrent(node.AsMap()["info"].AsMap())
	t.PrintSummary()

	//verified, err := t.VerifyFile("1d94f21f346532936f3fea2171d067ef_165612kxnnibu2i7ibmml7.jpg")
	start := time.Now()
	verified, err := t.VerifyFile("abp-683.mp4")
	//verified, err := t.VerifyAll()
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	log.Printf("verified: %v", verified)

	log.Printf("%v elapsed", time.Now().Sub(start))
}
