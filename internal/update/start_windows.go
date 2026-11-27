package update

import (
	"os/exec"
	"syscall"
)

// startAfterExit runs cmdline with cmd.exe in a hidden console (which the
// console programs it runs share, so no window flashes up); it goes on after
// this app quits. The command line is passed as is, since cmd.exe doesn't
// read quotes the way Go writes them.
func startAfterExit(cmdline string) error {
	const createNoWindow = 0x08000000
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       "cmd.exe /d /c " + cmdline,
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
