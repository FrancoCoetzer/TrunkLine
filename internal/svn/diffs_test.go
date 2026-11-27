package svn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	f, gone, cafe := filepath.Join(wc, "f.txt"), filepath.Join(wc, "gone.txt"), filepath.Join(wc, "src", "café ü.txt")
	write(t, f, "a\nb\n")
	write(t, gone, "x\n")
	write(t, cafe, "1\n")
	if err := c.Add(ctx, Opts{}, f, gone, filepath.Join(wc, "src")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "init", []string{f, gone, filepath.Join(wc, "src"), cafe}); err != nil {
		t.Fatal(err) // r2
	}
	if _, err := c.Copy(ctx, Opts{}, root+"/trunk", root+"/tags/v1", "tag"); err != nil {
		t.Fatal(err) // r3
	}
	write(t, f, "a\nB\n")
	write(t, cafe, "2\n")
	n := filepath.Join(wc, "new.txt")
	write(t, n, "new\n")
	if err := c.Add(ctx, Opts{}, n); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, Opts{}, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "change", []string{f, cafe, n, gone}); err != nil {
		t.Fatal(err) // r4
	}

	// The tag against trunk now, and trunk at r2 against r4: the same.
	for _, pair := range [][2]string{{root + "/tags/v1", root + "/trunk"}, {root + "/trunk@2", root + "/trunk@4"}} {
		ch, err := c.DiffSummary(ctx, Opts{}, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(ch); got != "[{f.txt modified none file} {gone.txt deleted none file} {new.txt added none file} {src/café ü.txt modified none file}]" {
			t.Errorf("%v: %s", pair, got)
		}
	}
	// One file's diff, and a deleted one's, by their paths in the summary.
	d, err := c.DiffPath(ctx, Opts{}, root+"/tags/v1", root+"/trunk", "src/café ü.txt", "modified")
	if err != nil || !strings.Contains(d, "-1") || !strings.Contains(d, "+2") || strings.Contains(d, "f.txt") {
		t.Errorf("café diff = %q, %v", d, err)
	}
	if d, err := c.DiffPath(ctx, Opts{}, root+"/trunk@2", root+"/trunk@4", "gone.txt", "deleted"); err != nil || !strings.Contains(d, "\n-x\n") {
		t.Errorf("deleted file diff = %q, %v", d, err)
	}
	if d, err := c.DiffPath(ctx, Opts{}, root+"/trunk@2", root+"/trunk@4", "new.txt", "added"); err != nil || !strings.Contains(d, "\n+new\n") {
		t.Errorf("added file diff = %q, %v", d, err)
	}
	if d, err := c.DiffBetween(ctx, Opts{}, root+"/tags/v1", root+"/trunk"); err != nil || !strings.Contains(d, "+B") || !strings.Contains(d, "+new") {
		t.Errorf("whole diff = %q, %v", d, err)
	}
	// One file at two revisions: its summary path is "".
	ch, err := c.DiffSummary(ctx, Opts{}, root+"/trunk/f.txt@2", root+"/trunk/f.txt@4")
	if err != nil || len(ch) != 1 || ch[0].Path != "" {
		t.Fatalf("file summary = %v, %v", ch, err)
	}
	if d, err := c.DiffPath(ctx, Opts{}, root+"/trunk/f.txt@2", root+"/trunk/f.txt@4", "", ch[0].Item); err != nil || !strings.Contains(d, "+B") {
		t.Errorf("file diff = %q, %v", d, err)
	}
	// Nothing different.
	if ch, err := c.DiffSummary(ctx, Opts{}, root+"/trunk@4", root+"/trunk"); err != nil || len(ch) != 0 {
		t.Errorf("same = %v, %v", ch, err)
	}
}

func TestRelativePatch(t *testing.T) {
	in := "Index: f.txt\n" +
		"diff --git a/trunk/src/f.txt b/trunk/src/f.txt\n" +
		"--- a/trunk/src/f.txt\t(revision 4)\r\n" +
		"+++ b/trunk/src/f.txt\t(working copy)\n" +
		"-a/trunk/src/x stays: it's content\n" +
		"Property changes on: trunk/src/f.txt\n" +
		"copy from trunk/src/old.txt\n"
	want := "Index: f.txt\n" +
		"diff --git a/f.txt b/f.txt\n" +
		"--- a/f.txt\t(revision 4)\r\n" +
		"+++ b/f.txt\t(working copy)\n" +
		"-a/trunk/src/x stays: it's content\n" +
		"Property changes on: f.txt\n" +
		"copy from old.txt\n"
	if got := relativePatch(in, "/trunk/src/"); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := relativePatch(in, ""); got != in {
		t.Error("an empty prefix changed the patch")
	}
}

func TestParsePatchOutput(t *testing.T) {
	out := "UU        /w/f.txt\n" +
		">         applied hunk @@ -1,3 +1,4 @@ with fuzz 2\n" +
		"CU        /w/g.txt\n" +
		">         rejected hunk @@ -1,3 +1,4 @@\n" +
		"A         /w/new dir/n.txt\n" +
		" U        /w/d\n" +
		"Skipped missing target: '/w/missing.txt'\n" +
		"Summary of conflicts:\n  Text conflicts: 1\n  Skipped paths: 1\n"
	got := parsePatchOutput(out, "/w")
	want := []Patched{
		{Path: "f.txt", Action: "updated", Props: "updated", Notes: []string{"applied hunk @@ -1,3 +1,4 @@ with fuzz 2"}},
		{Path: "g.txt", Action: "conflict", Props: "updated", Rejected: true, Notes: []string{"rejected hunk @@ -1,3 +1,4 @@"}},
		{Path: "new dir/n.txt", Action: "added"},
		{Path: "d", Props: "updated"},
		{Path: "missing.txt", Action: "skipped"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestPatchRoundTrip(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	src := filepath.Join(wc, "src")
	f, gone, bin := filepath.Join(src, "café ü.txt"), filepath.Join(src, "gone.txt"), filepath.Join(src, "pic.bin")
	write(t, f, "a\nb\nc\n")
	write(t, gone, "bye\n")
	write(t, bin, "\x00\x01\x02binary\xff")
	if err := c.Add(ctx, Opts{}, src); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "init", []string{src, f, gone, bin}); err != nil {
		t.Fatal(err)
	}
	// Two checkouts of trunk/src (a subfolder, so the repository path is trunk/src).
	mine, theirs := filepath.Join(t.TempDir(), "mine"), filepath.Join(t.TempDir(), "theirs")
	for _, d := range []string{mine, theirs} {
		if err := c.Checkout(ctx, Opts{}, root+"/trunk/src", d, nil); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(mine, "café ü.txt"), "a\nB\nc\nd\n")
	write(t, filepath.Join(mine, "pic.bin"), "\x00\x09new binary\xfe")
	write(t, filepath.Join(mine, "new.txt"), "brand new\n")
	if err := c.Add(ctx, Opts{}, filepath.Join(mine, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, Opts{}, filepath.Join(mine, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProp(ctx, Opts{}, "owner", "me", filepath.Join(mine, "café ü.txt")); err != nil {
		t.Fatal(err)
	}
	p, err := c.MakePatch(ctx, Opts{}, mine, "trunk/src", []string{"café ü.txt", "pic.bin", "new.txt", "gone.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p, "trunk/src") || !strings.Contains(p, "diff --git a/café ü.txt b/café ü.txt") {
		t.Fatalf("patch paths:\n%s", p)
	}
	file := filepath.Join(t.TempDir(), "x.patch")
	if err := os.WriteFile(file, []byte(p), 0o644); err != nil {
		t.Fatal(err)
	}

	// A dry run changes nothing.
	res, err := c.ApplyPatch(ctx, Opts{}, theirs, file, true)
	if err != nil || len(res) != 4 {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	if st := statusMap(t, c, theirs); len(st) != 0 {
		t.Errorf("the dry run changed: %v", st)
	}
	res, err = c.ApplyPatch(ctx, Opts{}, theirs, file, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(res); got != "[{café ü.txt updated updated false []} {pic.bin updated  false []} {new.txt added  false []} {gone.txt deleted  false []}]" {
		t.Errorf("applied: %s", got)
	}
	for _, name := range []string{"café ü.txt", "pic.bin", "new.txt"} {
		a, _ := os.ReadFile(filepath.Join(mine, name))
		b, _ := os.ReadFile(filepath.Join(theirs, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs: %q vs %q", name, a, b)
		}
	}
	if v, _ := c.GetProp(ctx, Opts{}, "owner", filepath.Join(theirs, "café ü.txt")); v != "me" {
		t.Errorf("property = %q", v)
	}
	if _, err := os.Stat(filepath.Join(theirs, "gone.txt")); !os.IsNotExist(err) {
		t.Error("gone.txt is still there")
	}

	// A part that doesn't fit is rejected; the rest goes in.
	if err := c.Revert(ctx, Opts{}, theirs); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(theirs, "new.txt"))
	write(t, filepath.Join(theirs, "café ü.txt"), "nothing\nlike\nit\nat all\n")
	res, err = c.ApplyPatch(ctx, Opts{}, theirs, file, false)
	if err != nil || len(res) == 0 || !res[0].Rejected || res[0].Path != "café ü.txt" {
		t.Errorf("rejected: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(theirs, "café ü.txt.svnpatch.rej")); err != nil {
		t.Error("no .svnpatch.rej file")
	}

	// Not a patch.
	notes := filepath.Join(t.TempDir(), "notes.txt")
	write(t, notes, "hello\n")
	if _, err := c.ApplyPatch(ctx, Opts{}, theirs, notes, true); !errors.Is(err, ErrNotPatch) {
		t.Errorf("not a patch: %v", err)
	}
}
