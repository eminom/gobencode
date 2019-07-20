package bencode

import (
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func walkPathSub(pwd string, a *[]string) {
	files, err := ioutil.ReadDir(pwd)
	if err != nil {
		log.Fatal(err)
	}
	nxd := []string{}
	for _, fi := range files {
		if fi.Name() == "." || fi.Name() == ".." {
			log.Printf("=>>> %v", fi.Name())
			continue
		}
		full := path.Join(pwd, fi.Name())
		if fi.IsDir() {
			nxd = append(nxd, full)
			continue
		}
		*a = append(*a, full)
	}
	for _, nd := range nxd {
		walkPath(nd, a)
	}
}

func walkPath(pwd string, a *[]string) {
	walkPathSub(pwd, a)
	for i := range *a {
		(*a)[i] = filepath.ToSlash((*a)[i])
	}
}

type FileMan struct {
	filels []string
}

func NewFileMan() *FileMan {
	rv := &FileMan{}
	now, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	walkPath(now, &rv.filels)
	return rv
}

func lookupNowWith(filels []string, that string, length int64, strict bool) (string, bool) {
	that = filepath.ToSlash(that)
	for _, fi := range filels {
		stat, err := os.Stat(fi)
		if err != nil {
			log.Print(err)
			continue
		}

		if length != stat.Size() {
			continue
		}

		if strings.HasSuffix(fi, that) {
			return fi, true
		}

		if !strict {
			if path.Ext(fi) == path.Ext(that) {
				return fi, true
			}
		}
	}
	return "", false
}

func (fm *FileMan) Lookup(name string, length int64) (string, bool) {
	filepath, ok := lookupNowWith(fm.filels, name, length, true)
	if ok {
		return filepath, ok
	}
	log.Printf("try strict=false")
	return lookupNowWith(fm.filels, name, length, false)
}
