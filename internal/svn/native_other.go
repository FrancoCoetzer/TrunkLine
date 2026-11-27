//go:build !windows

package svn

// fromNative: svn writes text in the locale's character set, which
// environ() keeps (UTF-8 on any current desktop).
func fromNative(b []byte) string { return string(b) }

// toNative: svn reads text in the locale's character set (UTF-8).
func toNative(s string) ([]byte, bool) { return []byte(s), true }
