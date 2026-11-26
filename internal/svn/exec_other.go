//go:build !windows

package svn

import "os/exec"

// prepare readies an svn command; on Windows it hides the console window
// (exec_windows.go). Nothing to do elsewhere.
func prepare(*exec.Cmd) {}

func extraPaths() []string { return []string{"/usr/local/bin/svn", "/opt/homebrew/bin/svn"} }
