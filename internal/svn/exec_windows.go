package svn

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// The app is a GUI program (-H windowsgui); without this every svn call
// would flash a console window.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// Where TortoiseSVN (with its "command line client tools" option), SlikSVN
// and VisualSVN put svn.exe when they are not on PATH.
func extraPaths() []string {
	var dirs []string
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if d := os.Getenv(env); d != "" {
			dirs = append(dirs, d)
		}
	}
	var paths []string
	for _, d := range dirs {
		paths = append(paths,
			filepath.Join(d, "TortoiseSVN", "bin", "svn.exe"),
			filepath.Join(d, "SlikSvn", "bin", "svn.exe"),
			filepath.Join(d, "VisualSVN", "bin", "svn.exe"),
		)
	}
	return paths
}
