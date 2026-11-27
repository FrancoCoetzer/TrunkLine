package svn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func shortPath(t *testing.T, long string) string {
	t.Helper()
	p, _ := syscall.UTF16PtrFromString(long)
	buf := make([]uint16, 512)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skip("no short names here")
	}
	s := syscall.UTF16ToString(buf[:n])
	if strings.EqualFold(s, long) {
		t.Skip("8.3 short names are off on this volume")
	}
	return s
}

// A working copy reached through a short (8.3) name: svn move into a new
// folder used to fail with E155004 ("is already locked").
func TestMoveShortPath(t *testing.T) {
	c, err := Find()
	if err != nil {
		t.Skip(err)
	}
	long := filepath.Join(t.TempDir(), "a long folder name")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(long, "repo")
	if out, err := exec.Command("svnadmin", "create", repo).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin: %v\n%s", err, out)
	}
	short := shortPath(t, long)
	ctx := context.Background()
	wc := filepath.Join(short, "wc")
	if err := c.Checkout(ctx, Opts{}, FileURL(repo), wc, nil); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(wc, "a.txt")
	write(t, f, "x\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "init", []string{f}); err != nil {
		t.Fatal(err)
	}
	if err := c.Move(ctx, Opts{}, f, filepath.Join(wc, "docs", "b.txt")); err != nil {
		t.Fatalf("move via the short path %s: %v", short, err)
	}
	// Every short part is spelled out (the temp folder itself may have one).
	full := LongPath(short)
	if strings.Contains(full, "~") || !strings.HasSuffix(full, `\a long folder name`) {
		t.Errorf("LongPath(%s) = %s", short, full)
	}
	if got := LongPath(filepath.Join(short, "not yet", "x.txt")); got != filepath.Join(full, "not yet", "x.txt") {
		t.Errorf("LongPath of a missing path = %s", got)
	}
	// Status names files relative to the working copy whichever name it has.
	write(t, filepath.Join(wc, "new.txt"), "n\n")
	st := statusMap(t, c, wc)
	if st["new.txt"].Item != "unversioned" || st["docs/b.txt"].Item != "added" {
		t.Errorf("status via the short path = %+v", st)
	}
}
