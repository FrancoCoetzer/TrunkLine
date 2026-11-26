package svn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUndoRevision(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	f := filepath.Join(wc, "f.txt")
	commit := func(text, msg string) {
		t.Helper()
		write(t, f, text)
		if _, err := c.Commit(ctx, Opts{}, msg, []string{f}); err != nil {
			t.Fatal(err)
		}
	}
	write(t, f, "a\nb\nc\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	commit("a\nb\nc\n", "r2")
	commit("a\nB\nc\n", "r3: change b")
	commit("a\nB\nc\nd\n", "r4: add d")
	trunk := root + "/trunk"

	// Right after committing, the working copy is at mixed revisions.
	if err := c.Merge(ctx, Opts{}, trunk, -3, wc, nil); KindOf(err) != KindNeedUpdate {
		t.Fatalf("merge into a mixed-revision working copy: kind %v, %v", KindOf(err), err)
	}
	if err := c.Update(ctx, Opts{}, wc, nil); err != nil {
		t.Fatal(err)
	}

	// Undo r3: b comes back, r4's d stays, nothing committed yet.
	if err := c.Merge(ctx, Opts{}, trunk, -3, wc, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); string(b) != "a\nb\nc\nd\n" {
		t.Errorf("after undoing r3: %q", b)
	}
	st := statusMap(t, c, wc)
	if st["f.txt"].Item != "modified" {
		t.Errorf("status after undo = %+v", st)
	}
	// svn records the merge on the working copy root, then drops it again
	// (elides it) since nothing else needs it: nothing but f.txt to commit.
	for p, ch := range st {
		if p != "f.txt" && ch.Item != "unversioned" {
			t.Errorf("unexpected change after undo: %s %+v", p, ch)
		}
	}
	if err := c.Revert(ctx, Opts{}, wc); err != nil {
		t.Fatal(err)
	}

	// Undoing a change a later commit built on: a conflict, left for the user.
	commit("a\nBB\nc\nd\n", "r5: BB")
	if err := c.Update(ctx, Opts{}, wc, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(ctx, Opts{}, trunk, -3, wc, nil); err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, c, wc); !st["f.txt"].Conflicted() {
		t.Errorf("expected a conflict: %+v", st)
	}
}
