package svn

import (
	"context"
	"fmt"
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

func TestMergeBranches(t *testing.T) {
	c, root, tw := newRepo(t) // tw: a checkout of trunk
	ctx := context.Background()
	f := filepath.Join(tw, "f.txt")
	// commit commits every local change in wc (a merge must be committed as
	// a whole: the merge record on the folder and the changed files together).
	commit := func(wc, msg string, _ ...string) {
		t.Helper()
		var paths []string
		for p, ch := range statusMap(t, c, wc) {
			if ch.Item != "unversioned" {
				paths = append(paths, filepath.Join(wc, filepath.FromSlash(p)))
			}
		}
		if _, err := c.Commit(ctx, Opts{}, msg, paths); err != nil {
			t.Fatal(err)
		}
		if err := c.Update(ctx, Opts{}, wc, nil); err != nil {
			t.Fatal(err)
		}
	}
	write(t, f, "1\n2\n3\n4\n5\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	commit(tw, "add f", f) // r2
	if _, err := c.Copy(ctx, Opts{}, root+"/trunk", root+"/branches/b", "branch b"); err != nil {
		t.Fatal(err) // r3
	}
	bw := filepath.Join(t.TempDir(), "bw")
	if err := c.Checkout(ctx, Opts{}, root+"/branches/b", bw, nil); err != nil {
		t.Fatal(err)
	}
	bf := filepath.Join(bw, "f.txt")
	write(t, f, "ONE\n2\n3\n4\n5\n")
	commit(tw, "trunk: ONE", f) // r4
	g := filepath.Join(tw, "g.txt")
	write(t, g, "new\n")
	if err := c.Add(ctx, Opts{}, g); err != nil {
		t.Fatal(err)
	}
	commit(tw, "trunk: add g", g) // r5
	write(t, bf, "1\n2\n3\n4\nFIVE\n")
	commit(bw, "branch: FIVE", bf) // r6

	trunk, branch := root+"/trunk", root+"/branches/b"
	eligible := func(src, wc string) string {
		t.Helper()
		r, err := c.Eligible(ctx, Opts{}, src, wc)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(r)
	}
	if got := eligible(trunk, bw); got != "[4 5]" {
		t.Fatalf("eligible trunk->branch = %s", got)
	}
	log, err := c.LogRevisions(ctx, Opts{}, trunk, []int{4, 5})
	if err != nil || len(log) != 2 || log[0].Revision != 5 || log[0].Message != "trunk: add g" || log[1].Message != "trunk: ONE" {
		t.Fatalf("LogRevisions = %+v, %v", log, err)
	}

	// Cherry-pick r5 into the branch.
	if err := c.MergeFrom(ctx, Opts{}, trunk, bw, []int{5}, nil); err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, c, bw); st["g.txt"].Item != "added" || st["f.txt"].Item != "" {
		t.Fatalf("after cherry-pick: %+v", st)
	}
	commit(bw, "cherry-pick r5", bw) // r7
	if got := eligible(trunk, bw); got != "[4]" {
		t.Errorf("after cherry-pick, eligible = %s", got)
	}

	// Sync the rest: only r4 comes (r5 isn't applied twice).
	var out []string
	if err := c.MergeFrom(ctx, Opts{}, trunk, bw, nil, func(l string) { out = append(out, l) }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bf); string(b) != "ONE\n2\n3\n4\nFIVE\n" {
		t.Errorf("branch after sync: %q", b)
	}
	commit(bw, "sync trunk", bw) // r8
	if got := eligible(trunk, bw); got != "[]" {
		t.Errorf("after sync, eligible = %s", got)
	}

	// Bring the branch back to trunk: needs a clean, single-revision working copy.
	write(t, g, "local edit\n")
	if err := c.MergeFrom(ctx, Opts{}, branch, tw, nil, nil); KindOf(err) != KindNeedClean {
		t.Errorf("merge back with local edits: kind %v, %v", KindOf(err), err)
	}
	if err := c.Revert(ctx, Opts{}, g); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, Opts{}, tw, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.MergeFrom(ctx, Opts{}, branch, tw, nil, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); string(b) != "ONE\n2\n3\n4\nFIVE\n" {
		t.Errorf("trunk after merging the branch back: %q", b)
	}
	if b, _ := os.ReadFile(g); string(b) != "new\n" {
		t.Errorf("g.txt shouldn't change: %q", b)
	}
	st := statusMap(t, c, tw)
	if st["f.txt"].Item != "modified" || st["."].Props != "modified" || len(st) != 2 {
		t.Errorf("trunk status after merge = %+v", st)
	}
}
