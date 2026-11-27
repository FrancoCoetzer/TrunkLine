//go:build !windows

package svn

// LongPath: only Windows has short (8.3) names.
func LongPath(path string) string { return path }
