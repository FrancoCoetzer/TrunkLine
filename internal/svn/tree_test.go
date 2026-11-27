package svn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMove(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	f := filepath.Join(wc, "old name.txt")
	write(t, f, "one\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "add", []string{f}); err != nil {
		t.Fatal(err) // r2
	}
	write(t, f, "one\nedited\n")
	dst := filepath.Join(wc, "docs", "new name.txt") // a new folder too
	if err := c.Move(ctx, Opts{}, f, dst); err != nil {
		t.Fatal(err)
	}
	st := statusMap(t, c, wc)
	if st["old name.txt"].Item != "deleted" || st["docs/new name.txt"].Item != "added" || !st["docs/new name.txt"].Copied || st["docs"].Item != "added" {
		t.Errorf("status = %+v", st)
	}
	if b, _ := os.ReadFile(dst); string(b) != "one\nedited\n" {
		t.Errorf("the edit didn't come along: %q", b)
	}
	if _, err := c.Commit(ctx, Opts{}, "move", []string{filepath.Join(wc, "old name.txt"), filepath.Join(wc, "docs"), dst}); err != nil {
		t.Fatal(err)
	}
	// The history goes back through the move.
	log, err := c.Log(ctx, Opts{}, root+"/trunk/docs/new%20name.txt", 0, 10)
	if err != nil || len(log) != 2 {
		t.Errorf("log = %+v, %v", log, err)
	}
}

func TestURLChanges(t *testing.T) {
	c, root, _ := newRepo(t)
	ctx := context.Background()
	rev, err := c.MkdirURL(ctx, Opts{}, root+"/trunk/docs/drafts", "folders")
	if err != nil || rev != 2 {
		t.Fatalf("mkdir: r%d, %v", rev, err)
	}
	if _, err := c.MoveURL(ctx, Opts{}, root+"/trunk/docs", root+"/trunk/manual", "rename"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Exists(ctx, Opts{}, root+"/trunk/manual/drafts"); !ok {
		t.Error("the moved folder isn't there")
	}
	if ok, _ := c.Exists(ctx, Opts{}, root+"/trunk/docs"); ok {
		t.Error("the old folder is still there")
	}
}

func TestSparse(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	for _, p := range []string{"a/sub/y", "a/x", "b/z", "top.txt"} {
		write(t, filepath.Join(wc, filepath.FromSlash(p)), p+"\n")
	}
	if err := c.Add(ctx, Opts{}, filepath.Join(wc, "a"), filepath.Join(wc, "b"), filepath.Join(wc, "top.txt")); err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, p := range []string{"a", "a/sub", "a/sub/y", "a/x", "b", "b/z", "top.txt"} {
		all = append(all, filepath.Join(wc, filepath.FromSlash(p)))
	}
	if _, err := c.Commit(ctx, Opts{}, "init", all); err != nil {
		t.Fatal(err)
	}

	sp := filepath.Join(t.TempDir(), "sparse")
	if err := c.CheckoutDepth(ctx, Opts{}, root+"/trunk", sp, DepthImmediates, nil); err != nil {
		t.Fatal(err)
	}
	d, err := c.Depths(ctx, Opts{}, sp)
	if err != nil || fmt.Sprint(d) != "map[:immediates a:empty b:empty]" {
		t.Fatalf("depths = %v, %v", d, err)
	}
	// Fetch a deep folder (its parents come along), leave b out.
	if err := c.SetDepth(ctx, Opts{}, filepath.Join(sp, "a", "sub"), DepthInfinity, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(sp, "a", "sub", "y")); string(b) != "a/sub/y\n" {
		t.Errorf("a/sub/y = %q", b)
	}
	if err := c.SetDepth(ctx, Opts{}, filepath.Join(sp, "b"), DepthExclude, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sp, "b")); !os.IsNotExist(err) {
		t.Error("b is still there")
	}
	d, _ = c.Depths(ctx, Opts{}, sp)
	if d["b"] != DepthExclude {
		t.Errorf("b's depth = %q", d["b"])
	}
	// And back.
	if err := c.SetDepth(ctx, Opts{}, filepath.Join(sp, "b"), DepthInfinity, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(sp, "b", "z")); string(b) != "b/z\n" {
		t.Errorf("b/z = %q", b)
	}
}

func TestExternals(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"^/libs/common/trunk common\n", "[{^/libs/common/trunk  common}]", true},
		{"https://svn.example.com/r/lib@123 lib\n-r 7 ../tools tools\n", "[{https://svn.example.com/r/lib 123 lib} {../tools 7 tools}]", true},
		{"-r7 //host/repo/x x", "[{//host/repo/x 7 x}]", true},
		{"third-party/zlib -r 42 http://svn.example.com/zlib/trunk", "[{http://svn.example.com/zlib/trunk 42 third-party/zlib}]", true}, // old format
		{`^/docs "user guide"`, "[{^/docs  user guide}]", true},
		{"\n\n", "[]", true},
		{"# a comment\n^/x x", "", false},
		{"just-one-word", "", false},
		{`^/x "unclosed`, "", false},
	} {
		ext, ok := ParseExternals(c.in)
		if ok != c.ok || (ok && fmt.Sprint(ext) != c.want) {
			t.Errorf("ParseExternals(%q) = %v, %v; want %s, %v", c.in, ext, ok, c.want, c.ok)
		}
	}
	got := FormatExternals([]External{{URL: "^/libs/common/trunk", Dir: "common"}, {URL: "../tools", Rev: "7", Dir: "my tools"}})
	if want := "^/libs/common/trunk common\n../tools@7 \"my tools\"\n"; got != want {
		t.Errorf("FormatExternals = %q, want %q", got, want)
	}
	// What it writes, it reads back.
	if ext, ok := ParseExternals(got); !ok || len(ext) != 2 || ext[1].Dir != "my tools" || ext[1].Rev != "7" {
		t.Errorf("round trip: %v, %v", ext, ok)
	}
}

func TestExternalsFetched(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	if _, err := c.MkdirURL(ctx, Opts{}, root+"/libs/common", "lib"); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateFolderOnly(ctx, Opts{}, wc); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProp(ctx, Opts{}, "svn:externals", FormatExternals([]External{{URL: "^/libs/common", Dir: "common"}}), wc); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "externals", []string{wc}); err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := c.Update(ctx, Opts{}, wc, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(wc, "common")); err != nil || !st.IsDir() {
		t.Errorf("the external wasn't fetched: %v\n%s", err, strings.Join(lines, "\n"))
	}
}

func TestGraph(t *testing.T) {
	c, root, wc := newRepo(t) // r1: trunk, branches, tags
	ctx := context.Background()
	f := filepath.Join(wc, "f")
	write(t, f, "a\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	commit := func(msg string, paths ...string) {
		t.Helper()
		if _, err := c.Commit(ctx, Opts{}, msg, paths); err != nil {
			t.Fatal(err)
		}
	}
	commit("t1", f)                                                                               // r2
	if _, err := c.Copy(ctx, Opts{}, root+"/trunk", root+"/branches/b", "branch b"); err != nil { // r3
		t.Fatal(err)
	}
	if _, err := c.Copy(ctx, Opts{}, root+"/trunk", root+"/tags/v1", "tag v1"); err != nil { // r4
		t.Fatal(err)
	}
	if err := c.Switch(ctx, Opts{}, wc, root+"/branches/b", nil); err != nil {
		t.Fatal(err)
	}
	write(t, f, "a\nb\n")
	commit("b work", f) // r5
	if err := c.Switch(ctx, Opts{}, wc, root+"/trunk", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.MergeFrom(ctx, Opts{}, root+"/branches/b", wc, nil, nil); err != nil { // everything not merged yet
		t.Fatal(err)
	}
	commit("merge b", wc, f)                                                               // r6
	if _, err := c.DeleteURL(ctx, Opts{}, root+"/branches/b", "done with b"); err != nil { // r7
		t.Fatal(err)
	}

	log, err := c.Log(ctx, Opts{}, root, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	g := BuildGraph("", log)
	if len(g.Lanes) != 2 || g.Lanes[0].Name != "trunk" || g.Lanes[1].Name != "b" || g.Lanes[1].Parent != 0 ||
		g.Lanes[1].ParentRev != 2 || g.Lanes[1].From != 3 || g.Lanes[1].To != 7 {
		t.Fatalf("lanes = %+v", g.Lanes)
	}
	var got []string
	for _, cm := range g.Commits {
		got = append(got, fmt.Sprintf("r%d:%d:%s%v", cm.Rev, cm.Lane, cm.Message, cm.Tags))
	}
	if want := "[r7:1:done with b[] r6:0:merge b[] r5:1:b work[] r3:1:branch b[] r2:0:t1[v1] r1:0:layout[]]"; fmt.Sprint(got) != want {
		t.Errorf("commits = %v\nwant      %s", got, want)
	}
	merged, err := c.MergedInto(ctx, Opts{}, root+"/trunk", 100)
	if err != nil {
		t.Fatal(err)
	}
	g.AddMerges("", "/trunk", merged)
	if fmt.Sprint(g.Merges) != "[{1 5 0 6}]" {
		t.Errorf("merges = %+v", g.Merges)
	}
}
