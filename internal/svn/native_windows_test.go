package svn

import (
	"syscall"
	"testing"
)

func TestFromNative(t *testing.T) {
	acp, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetACP").Call()
	if acp != 1252 {
		t.Skipf("the ANSI code page is %d, not 1252", acp)
	}
	if got := fromNative([]byte("caf\xe9 \xfc.txt")); got != "café ü.txt" {
		t.Errorf("fromNative = %q", got)
	}
	if got := string(utf8Headers([]byte("Index: caf\xe9.txt\r\n-caf\xe9\r\n"))); got != "Index: café.txt\r\n-caf\xe9\r\n" {
		t.Errorf("utf8Headers = %q", got)
	}
	if b, ok := toNative("café"); !ok || string(b) != "caf\xe9" {
		t.Errorf("toNative = %q, %v", b, ok)
	}
	if _, ok := toNative("日本"); ok {
		t.Error("toNative took characters 1252 doesn't have")
	}
	if _, err := nativeHeaders([]byte("Index: 日本.txt\n")); err == nil {
		t.Error("nativeHeaders took a name 1252 can't hold")
	}
}
