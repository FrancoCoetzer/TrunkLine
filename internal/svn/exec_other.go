//go:build !windows

package svn

import "os/exec"

func hideWindow(*exec.Cmd) {}

func extraPaths() []string { return []string{"/usr/local/bin/svn", "/opt/homebrew/bin/svn"} }
