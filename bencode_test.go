package bencode

import (
	"fmt"
	"strconv"
	"testing"
)

func doExpect(t *testing.T, func1 func(), err error) {
	defer func() {
		if e := recover(); err == e {
			t.Logf("as expected: %v", e)
		} else {
			panic(fmt.Errorf("not as expected:<%v>", e))
		}
	}()
	func1()
}

func TestBencode(t *testing.T) {
	b0 := MustScanString("le")
	_ = b0.AsList()

	doExpect(t, func() { _ = b0.AsMap() }, TypeError)
	doExpect(t, func() { _ = b0.AsInt() }, TypeError)

	b1 := MustScanString("de")
	_ = b1.AsMap()

	b2 := MustScanString("d4:listl2:XXee")
	_ = b2.AsMap()["list"].AsList()[0].AsString()

	doExpect(t, func() {
		_ = MustScanString("leX")
	}, RemainsError)

	func() {
		defer func() {
			e := recover()
			if nil == e {
				t.Logf("expect an error here")
				t.Fail()
				return
			}
			if _, ok := e.(*strconv.NumError); ok {
				// Nada
			} else {
				panic(fmt.Errorf("no error ?(%v)", e))
			}
		}()
		b3 := MustScanString("ie")
		t.Logf("integer value is:%v", b3.AsInt())
	}()

	b4 := MustScanString("i2008e")
	if b4.AsInt() != 2008 {
		t.Logf("parse integer failed")
		t.Fail()
	}

}
