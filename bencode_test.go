package bencode

import (
	"fmt"
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
	b0 := MustScan([]byte("le"))
	_ = b0.AsList()

	doExpect(t, func() { _ = b0.AsMap() }, TypeError)
	doExpect(t, func() { _ = b0.AsInt() }, TypeError)

	b1 := MustScan([]byte("de"))
	_ = b1.AsMap()

	b2 := MustScan([]byte("d4:listl2:XXee"))
	_ = b2.AsMap()["list"].AsList()[0].AsString()

	doExpect(t, func() {
		_ = MustScan([]byte("leX"))
	}, RemainsError)

}
