package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"time"

	"github.com/eminom/gobencode"
	"github.com/fatih/color"
)

var (
	redSprint    = color.New(color.FgRed).SprintFunc()
	yellowSprint = color.New(color.FgYellow).SprintFunc()
	greenSprint  = color.New(color.FgGreen).SprintFunc()
)

func red(i interface{}) string {
	return redSprint(fmt.Sprintf("%v", i))
}

func yellow(i interface{}) string {
	return yellowSprint(fmt.Sprintf("%v", i))
}

func green(i interface{}) string {
	return greenSprint(fmt.Sprint("%v", i))
}

func main() {
	log.SetFlags(log.Lshortfile | log.Ltime)

	var fInput string = "1000.torrent"
	var fDebug, fNoSingleHash bool
	flag.BoolVar(&fDebug, "debug", false, "debug mode")
	flag.BoolVar(&fNoSingleHash, "nosinglehash", false, "no single hash")
	flag.Parse()

	if len(flag.Args()) > 0 {
		fInput = flag.Args()[0]
	}

	bencode.SetSingleHashEnabled(!fNoSingleHash)

	chunk, err := ioutil.ReadFile(fInput)
	if err != nil {
		log.Fatalf("cannot open %v to read:%v", fInput, err)
	}

	node, left := bencode.Scan(chunk)
	if len(left) > 0 {
		log.Fatalf("torrent file are padding with more ? (%v)", left)
	}
	if fDebug {
		bencode.PrintNode(node, 0)
		log.Println()
	}

	t := bencode.NewTorrent(node.AsMap()["info"].AsMap())
	t.PrintSummary()

	for _, filename := range t.GetFileList() {
		if !strings.HasPrefix(filename, "_____padding_file") {
			verifyOne(t, filename)
		} else {
			log.Printf("skipping %v", filename)
		}
	}
}

func verifyOne(t *bencode.Torrent, filename string) {
	start := time.Now()
	defer func() {
		log.Printf("%v elapsed", time.Now().Sub(start))
	}()
	log.Printf("test for %v", filename)
	verified, err := t.VerifyFile(filename)
	if err != nil {
		log.Printf("error: %s", red(err))
	} else {
		log.Printf("verified: %s", yellow(verified))
	}
}
