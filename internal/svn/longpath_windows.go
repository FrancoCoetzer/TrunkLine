package svn

import (
	"path/filepath"
	"syscall"
)

// LongPath is path with any 8.3 short names (C:\Users\RUNNER~1) spelled out
// (C:\Users\runneradmin). svn locks a working copy by path, and given a
// short name for a working copy it knows by its long name it trips over its
// own locks (E155004, "is already locked") when a command touches a
// subfolder, as svn move does. A path that doesn't exist yet gets its
// existing part spelled out.
func LongPath(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, 512)
	for {
		n, err := syscall.GetLongPathName(p, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			// Missing: spell out the part that exists.
			dir, base := filepath.Split(filepath.Clean(path))
			dir = filepath.Clean(dir)
			if base == "" || dir == path || dir == "." {
				return path
			}
			return filepath.Join(LongPath(dir), base)
		}
		if int(n) <= len(buf) {
			return syscall.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}
