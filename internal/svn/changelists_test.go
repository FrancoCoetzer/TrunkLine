package svn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidChangelistName(t *testing.T) {
	for name, ok := range map[string]bool{
		"ui": true, "fix login bug": true, "café": true, "#42 [wip]": true, strings.Repeat("é", 60): true,
		"": false, " ui": false, "ui ": false, "a\nb": false, "a\tb": false, strings.Repeat("a", 61): false,
	} {
		if got := ValidChangelistName(name); got != ok {
			t.Errorf("ValidChangelistName(%q) = %v", name, got)
		}
	}
}

func TestChangelists(t *testing.T) {
	c, _, wc := newRepo(t)
	ctx := context.Background()
	a, b, d := filepath.Join(wc, "a.txt"), filepath.Join(wc, "b.txt"), filepath.Join(wc, "d.txt")
	for _, p := range []string{a, b, d} {
		write(t, p, "x\n")
	}
	if err := c.Add(ctx, Opts{}, a, b, d); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "init", []string{a, b, d}); err != nil {
		t.Fatal(err)
	}
	write(t, a, "x\nchanged\n")
	write(t, d, "x\nchanged\n")

	// b has no changes yet, but a file in a changelist is still listed.
	if err := c.SetChangelist(ctx, Opts{}, "login fix", a, b); err != nil {
		t.Fatal(err)
	}
	st := statusMap(t, c, wc)
	if st["a.txt"].Changelist != "login fix" || st["b.txt"].Changelist != "login fix" || st["b.txt"].Item != "normal" || st["d.txt"].Changelist != "" {
		t.Errorf("status = %+v", st)
	}
	// Moving one to another changelist.
	if err := c.SetChangelist(ctx, Opts{}, "other", b); err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, c, wc); st["b.txt"].Changelist != "other" {
		t.Errorf("b after moving: %+v", st["b.txt"])
	}
	// A revert keeps the changelist; a commit ends it.
	if err := c.Revert(ctx, Opts{}, a); err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, c, wc); st["a.txt"].Changelist != "login fix" {
		t.Errorf("a after revert: %+v", st["a.txt"])
	}
	write(t, a, "x\nagain\n")
	if _, err := c.Commit(ctx, Opts{}, "a", []string{a}); err != nil {
		t.Fatal(err)
	}
	if _, ok := statusMap(t, c, wc)["a.txt"]; ok {
		t.Error("a is still listed after committing it")
	}
	// Taking b out: unchanged, so no longer listed.
	if err := c.SetChangelist(ctx, Opts{}, "", b); err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, c, wc); len(st) != 1 || st["d.txt"].Item != "modified" {
		t.Errorf("status at the end = %+v", st)
	}
	// An unversioned file can't go in one, and that isn't a lock conflict.
	n := filepath.Join(wc, "new.txt")
	write(t, n, "x\n")
	if err := c.SetChangelist(ctx, Opts{}, "x", n); err == nil || KindOf(err) != KindOther {
		t.Errorf("changelist on an unversioned file: kind %v, %v", KindOf(err), err)
	}
}

func TestListProps(t *testing.T) {
	c, _, wc := newRepo(t)
	ctx := context.Background()
	f := filepath.Join(wc, "f.txt")
	write(t, f, "x\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	if p, err := c.ListProps(ctx, Opts{}, f); err != nil || len(p) != 0 || p == nil {
		t.Errorf("no properties: %#v, %v", p, err)
	}
	for name, v := range map[string]string{"svn:eol-style": "native", "svn:ignore": "", "team:owner": "Zoë <z@example.com>", "notes": "line 1\nline 2\n"} {
		if v == "" {
			continue
		}
		if err := c.SetProp(ctx, Opts{}, name, v, f); err != nil {
			t.Fatal(name, err)
		}
	}
	if err := c.SetProp(ctx, Opts{}, "svn:needs-lock", "yes", f); err != nil {
		t.Fatal(err)
	}
	p, err := c.ListProps(ctx, Opts{}, f)
	if err != nil {
		t.Fatal(err)
	}
	want := []Prop{{"notes", "line 1\nline 2\n"}, {"svn:eol-style", "native"}, {"svn:needs-lock", "*"}, {"team:owner", "Zoë <z@example.com>"}}
	if len(p) != len(want) {
		t.Fatalf("props = %q", p)
	}
	for i := range want {
		if p[i] != want[i] {
			t.Errorf("prop %d = %q, want %q", i, p[i], want[i])
		}
	}
	// svn checks the values it knows.
	if err := c.SetProp(ctx, Opts{}, "svn:eol-style", "sideways", f); err == nil || KindOf(err) != KindOther {
		t.Errorf("bad eol-style: kind %v, %v", KindOf(err), err)
	}
	if err := c.SetProp(ctx, Opts{}, "svn:ignore", "*.o", f); err == nil || KindOf(err) != KindOther {
		t.Errorf("svn:ignore on a file: kind %v, %v", KindOf(err), err)
	}
}
