package svn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// lockState of one file, from status (remote asks the server).
func lockOf(t *testing.T, c *Client, wc, path string, remote bool) (string, *LockInfo) {
	t.Helper()
	changes, _, err := c.Status(context.Background(), Opts{}, wc, remote)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes {
		if ch.Path == path {
			return ch.LockState, ch.Lock
		}
	}
	return "", nil
}

func writable(t *testing.T, p string) bool {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()&0o200 != 0
}

func TestLocks(t *testing.T) {
	c, root, a := newRepo(t)
	ctx := context.Background()

	// A design file that needs a lock, and a normal one.
	write(t, filepath.Join(a, "cad.step"), "model")
	write(t, filepath.Join(a, "code.c"), "int x;\n")
	if err := c.Add(ctx, Opts{}, filepath.Join(a, "cad.step"), filepath.Join(a, "code.c")); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProp(ctx, Opts{}, "svn:needs-lock", "*", filepath.Join(a, "cad.step")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "add", []string{filepath.Join(a, "cad.step"), filepath.Join(a, "code.c")}); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(t.TempDir(), "b")
	if err := c.Checkout(ctx, Opts{}, root+"/trunk", b, nil); err != nil {
		t.Fatal(err)
	}
	cadA, cadB := filepath.Join(a, "cad.step"), filepath.Join(b, "cad.step")

	if nl, err := c.NeedsLock(ctx, Opts{}, b); err != nil || len(nl) != 1 || nl[0] != "cad.step" {
		t.Errorf("NeedsLock = %v, %v", nl, err)
	}
	if writable(t, cadB) {
		t.Error("a needs-lock file should arrive read-only")
	}

	// B locks it: writable, and B's status says it's B's.
	if err := c.Lock(ctx, Opts{}, "editing the mounts", false, cadB); err != nil {
		t.Fatal(err)
	}
	if !writable(t, cadB) {
		t.Error("locking should make the file writable")
	}
	if st, l := lockOf(t, c, b, "cad.step", false); st != LockMine || l == nil || l.Comment != "editing the mounts" || l.Owner == "" || l.Created.IsZero() {
		t.Errorf("B's lock = %q %+v", st, l)
	}

	// A doesn't see it without asking the server, does with remote.
	if st, _ := lockOf(t, c, a, "cad.step", false); st != "" {
		t.Errorf("A without remote = %q", st)
	}
	if st, l := lockOf(t, c, a, "cad.step", true); st != LockOther || l == nil || l.Comment != "editing the mounts" {
		t.Errorf("A with remote = %q %+v", st, l)
	}
	// The repository browser shows it too.
	ls, err := c.List(ctx, Opts{}, root+"/trunk")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ls {
		if (e.Name == "cad.step") != (e.Lock != nil) {
			t.Errorf("list: %s lock = %+v", e.Name, e.Lock)
		}
	}

	// A can't lock it; the error says who has it.
	err = c.Lock(ctx, Opts{}, "mine", false, cadA)
	if KindOf(err) != KindLockConflict {
		t.Fatalf("locking a locked file: kind %v, %v", KindOf(err), err)
	}
	if lb := LockedBy(err); len(lb) != 1 || lb[0][0] != "/trunk/cad.step" || lb[0][1] == "" {
		t.Errorf("LockedBy = %v", lb)
	}

	// A takes it over: B's lock shows as stolen, and B can't commit the file.
	if err := c.Lock(ctx, Opts{}, "taking over", true, cadA); err != nil {
		t.Fatal(err)
	}
	if st, _ := lockOf(t, c, b, "cad.step", true); st != LockStolen {
		t.Errorf("B after the takeover = %q", st)
	}
	write(t, cadB, "B's edit")
	if _, err := c.Commit(ctx, Opts{}, "B", []string{cadB}); KindOf(err) != KindLockConflict {
		t.Errorf("committing with a stolen lock: kind %v, %v", KindOf(err), err)
	}

	// A commits: the lock is released and the file is read-only again.
	write(t, cadA, "A's edit")
	if _, err := c.Commit(ctx, Opts{}, "A", []string{cadA}); err != nil {
		t.Fatal(err)
	}
	if st, _ := lockOf(t, c, a, "cad.step", true); st != "" {
		t.Errorf("A's lock after commit = %q", st)
	}
	if writable(t, cadA) {
		t.Error("after commit the needs-lock file should be read-only again")
	}

	// Lock and unlock a normal file; B's stale stolen token: broken after unlock.
	codeA := filepath.Join(a, "code.c")
	if err := c.Lock(ctx, Opts{}, "", false, codeA); err != nil {
		t.Fatal(err)
	}
	if err := c.Unlock(ctx, Opts{}, false, codeA); err != nil {
		t.Fatal(err)
	}
	if st, _ := lockOf(t, c, a, "code.c", true); st != "" {
		t.Errorf("after unlock = %q", st)
	}
	if st, _ := lockOf(t, c, b, "cad.step", true); st != LockBroken {
		t.Errorf("B's old token after A released = %q", st)
	}
}
