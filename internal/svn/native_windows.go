package svn

import (
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	multiByteToWideChar = kernel32.NewProc("MultiByteToWideChar")
	wideCharToMultiByte = kernel32.NewProc("WideCharToMultiByte")
)

// fromNative turns text svn.exe wrote in the system's ANSI code page (the
// paths in diff headers and svn patch's report; its --xml output is always
// UTF-8) into UTF-8.
func fromNative(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const cpACP = 0
	n, _, _ := multiByteToWideChar.Call(cpACP, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}
	w := make([]uint16, n)
	multiByteToWideChar.Call(cpACP, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&w[0])), n)
	return string(utf16.Decode(w))
}

// toNative is UTF-8 text in the ANSI code page, for svn.exe to read (svn
// patch reads the paths in a patch that way); ok is false when a character
// has no equivalent there.
func toNative(s string) (b []byte, ok bool) {
	if s == "" {
		return nil, true
	}
	w := utf16.Encode([]rune(s))
	const cpACP, wcNoBestFitChars = 0, 0x400
	n, _, _ := wideCharToMultiByte.Call(cpACP, wcNoBestFitChars, uintptr(unsafe.Pointer(&w[0])), uintptr(len(w)), 0, 0, 0, 0)
	if n == 0 {
		return []byte(s), false
	}
	b = make([]byte, n)
	var usedDefault int32
	wideCharToMultiByte.Call(cpACP, wcNoBestFitChars, uintptr(unsafe.Pointer(&w[0])), uintptr(len(w)),
		uintptr(unsafe.Pointer(&b[0])), n, 0, uintptr(unsafe.Pointer(&usedDefault)))
	return b, usedDefault == 0
}
