package svn

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestBlame(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	f := filepath.Join(wc, "f.c")
	commit := func(msg string, paths ...string) {
		t.Helper()
		if _, err := c.Commit(ctx, Opts{}, msg, paths); err != nil {
			t.Fatal(err)
		}
	}

	write(t, f, "one\ntwo\nthree\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	commit("add", f) // r2
	write(t, f, "one\nTWO\nthree\nfour é\n")
	commit("change two, add four", f) // r3

	lines, err := c.Blame(ctx, Opts{}, f)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		text string
		rev  int
	}{{"one", 2}, {"TWO", 3}, {"three", 2}, {"four é", 3}}
	if len(lines) != len(want) {
		t.Fatalf("blame = %+v", lines)
	}
	for i, w := range want {
		if l := lines[i]; l.Line != i+1 || l.Text != w.text || l.Revision != w.rev || l.Author == "" || l.Date.IsZero() {
			t.Errorf("line %d = %+v, want %q r%d", i+1, l, w.text, w.rev)
		}
	}

	// A working copy file is blamed as it is on disk: a line inserted in the
	// middle has no revision, and the lines after it keep theirs.
	write(t, f, "one\nTWO\nnew line\nthree\nfour é\n")
	lines, err = c.Blame(ctx, Opts{}, f)
	if err != nil {
		t.Fatal(err)
	}
	wantLocal := []struct {
		text string
		rev  int
	}{{"one", 2}, {"TWO", 3}, {"new line", 0}, {"three", 2}, {"four é", 3}}
	if len(lines) != len(wantLocal) {
		t.Fatalf("blame with a local edit = %+v", lines)
	}
	for i, w := range wantLocal {
		if l := lines[i]; l.Text != w.text || l.Revision != w.rev {
			t.Errorf("local line %d = %q r%d, want %q r%d", i+1, l.Text, l.Revision, w.text, w.rev)
		}
	}
	write(t, f, "one\nTWO\nthree\nfour é\n") // back to the committed text

	// An older version, through a URL with a peg revision.
	lines, err = c.Blame(ctx, Opts{}, root+"/trunk/f.c@2")
	if err != nil || len(lines) != 3 || lines[1].Text != "two" || lines[1].Revision != 2 {
		t.Errorf("blame @2 = %+v, %v", lines, err)
	}

	// A binary file.
	bin := filepath.Join(wc, "b.bin")
	write(t, bin, "\x00\x01\x02binary")
	if err := c.Add(ctx, Opts{}, bin); err != nil {
		t.Fatal(err)
	}
	commit("bin", bin)
	if _, err := c.Blame(ctx, Opts{}, bin); !errors.Is(err, ErrBinary) {
		t.Errorf("binary blame: %v", err)
	}

	// An empty file.
	empty := filepath.Join(wc, "empty")
	write(t, empty, "")
	if err := c.Add(ctx, Opts{}, empty); err != nil {
		t.Fatal(err)
	}
	commit("empty", empty)
	if lines, err := c.Blame(ctx, Opts{}, empty); err != nil || len(lines) != 0 {
		t.Errorf("empty blame = %+v, %v", lines, err)
	}
}
