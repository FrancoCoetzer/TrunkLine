package svn

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo makes a repository with trunk/branches/tags and checks trunk out.
// It returns the client, trunk's URL and the working copy.
func newRepo(t *testing.T) (*Client, string, string) {
	t.Helper()
	c, err := Find()
	if err != nil {
		t.Skip(err)
	}
	if _, err := exec.LookPath("svnadmin"); err != nil {
		t.Skip("svnadmin not installed")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if out, err := exec.Command("svnadmin", "create", repo).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin: %v\n%s", err, out)
	}
	root := FileURL(repo)
	ctx := context.Background()
	if _, err := c.run(ctx, Opts{}, nil, "mkdir", "-m", "layout", "--",
		root+"/trunk", root+"/branches", root+"/tags"); err != nil {
		t.Fatal(err)
	}
	wc := filepath.Join(dir, "wc")
	var lines []string
	if err := c.Checkout(ctx, Opts{}, root+"/trunk", wc, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "revision 1") {
		t.Errorf("checkout output = %q", lines)
	}
	return c, root, wc
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func statusMap(t *testing.T, c *Client, wc string) map[string]Change {
	t.Helper()
	changes, _, err := c.Status(context.Background(), Opts{}, wc, false)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Change{}
	for _, ch := range changes {
		m[ch.Path] = ch
	}
	return m
}

func TestWorkflow(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()

	info, err := c.Info(ctx, Opts{}, wc)
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != root+"/trunk" || info.RepoRoot != root || info.Revision != 1 || info.RelativeURL != "^/trunk" {
		t.Errorf("info = %+v", info)
	}

	// A new folder with two files: only one of them is picked for the commit.
	write(t, filepath.Join(wc, "src", "a.c"), "int a;\n")
	write(t, filepath.Join(wc, "src", "b.c"), "int b;\n")
	write(t, filepath.Join(wc, "README"), "hello\n")
	st := statusMap(t, c, wc)
	if st["src"].Item != "unversioned" || !st["src"].IsDir || st["README"].Item != "unversioned" {
		t.Fatalf("status = %+v", st)
	}
	if err := c.Add(ctx, Opts{}, filepath.Join(wc, "src"), filepath.Join(wc, "README")); err != nil {
		t.Fatal(err)
	}
	st = statusMap(t, c, wc)
	if st["src/a.c"].Item != "added" || st["src/b.c"].Item != "added" {
		t.Fatalf("after add: %+v", st)
	}
	rev, err := c.Commit(ctx, Opts{}, "Add src/a.c and README\n\nUTF-8: é", []string{
		filepath.Join(wc, "src"), filepath.Join(wc, "src", "a.c"), filepath.Join(wc, "README"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev != 2 {
		t.Errorf("committed revision %d, want 2", rev)
	}
	st = statusMap(t, c, wc)
	if len(st) != 1 || st["src/b.c"].Item != "added" {
		t.Errorf("b.c should be left behind, still added: %+v", st)
	}

	// Modify, diff, revert.
	write(t, filepath.Join(wc, "README"), "hello\nworld\n")
	d, err := c.Diff(ctx, Opts{}, filepath.Join(wc, "README"), 0)
	if err != nil || !strings.Contains(d, "+world") {
		t.Errorf("diff = %q, %v", d, err)
	}
	if err := c.Revert(ctx, Opts{}, filepath.Join(wc, "README"), filepath.Join(wc, "src", "b.c")); err != nil {
		t.Fatal(err)
	}
	if st = statusMap(t, c, wc); st["README"].Item != "" || st["src/b.c"].Item != "unversioned" {
		t.Errorf("after revert: %+v", st)
	}

	// Log, list and a revision's diff.
	log, err := c.Log(ctx, Opts{}, root, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0].Revision != 2 || log[0].Message != "Add src/a.c and README\n\nUTF-8: é" || len(log[0].Paths) != 3 {
		t.Fatalf("log = %+v", log)
	}
	if p := log[0].Paths[0]; p.Path != "/trunk/README" || p.Action != "A" || p.Kind != "file" {
		t.Errorf("log path = %+v", p)
	}
	ls, err := c.List(ctx, Opts{}, root+"/trunk")
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 2 || ls[0].Name != "src" || ls[0].Kind != "dir" || ls[1].Name != "README" || ls[1].Size != 6 {
		t.Errorf("list = %+v", ls)
	}
	d, err = c.Diff(ctx, Opts{}, root, 2)
	if err != nil || !strings.Contains(d, "+int a;") {
		t.Errorf("revision diff = %q, %v", d, err)
	}
}

func TestConflict(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	write(t, filepath.Join(wc, "f.txt"), "one\n")
	if err := c.Add(ctx, Opts{}, filepath.Join(wc, "f.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "f", []string{filepath.Join(wc, "f.txt")}); err != nil {
		t.Fatal(err)
	}

	// Someone else changes the same line.
	other := filepath.Join(t.TempDir(), "other")
	if err := c.Checkout(ctx, Opts{}, root+"/trunk", other, nil); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(other, "f.txt"), "theirs\n")
	if _, err := c.Commit(ctx, Opts{}, "theirs", []string{filepath.Join(other, "f.txt")}); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(wc, "f.txt"), "mine\n")
	_, err := c.Commit(ctx, Opts{}, "mine", []string{filepath.Join(wc, "f.txt")})
	if KindOf(err) != KindConflict {
		t.Fatalf("out-of-date commit: kind %v, err %v", KindOf(err), err)
	}

	changes, head, err := c.Status(ctx, Opts{}, wc, true)
	if err != nil {
		t.Fatal(err)
	}
	if head != 3 || len(changes) != 1 || changes[0].RepoItem != "modified" {
		t.Errorf("status -u = %+v, head %d", changes, head)
	}

	if err := c.Update(ctx, Opts{}, wc, nil); err != nil {
		t.Fatal(err)
	}
	st := statusMap(t, c, wc)
	if !st["f.txt"].Conflicted() {
		t.Fatalf("expected a conflict: %+v", st)
	}
	if err := c.Resolve(ctx, Opts{}, "mine-full", filepath.Join(wc, "f.txt")); err != nil {
		t.Fatal(err)
	}
	if rev, err := c.Commit(ctx, Opts{}, "mine", []string{filepath.Join(wc, "f.txt")}); err != nil || rev != 4 {
		t.Fatalf("commit after resolve: r%d, %v", rev, err)
	}
}

func TestErrorKinds(t *testing.T) {
	for _, tc := range []struct {
		stderr string
		want   Kind
	}{
		{"svn: E170013: Unable to connect to a repository at URL 'https://x/svn'\nsvn: E215004: No more credentials or we tried too many times.\nAuthentication failed", KindAuth},
		{"svn: E170001: Authorization failed", KindAuth},
		{"svn: E170013: Unable to connect\nsvn: E230001: Server SSL certificate verification failed: issuer is not trusted", KindCert},
		{"svn: E155011: File '/x/f.txt' is out of date", KindConflict},
		{"svn: E155004: Run 'svn cleanup' to remove locks", KindLocked},
		{"svn: E160039: User 'alice' does not own lock on path '/trunk/README.md' (currently locked by 'franco')", KindLockConflict},
		{"svn: warning: W160035: Path '/trunk/x' is already locked by user 'franco' in filesystem 'u'\nsvn: E200009: One or more locks could not be obtained", KindLockConflict},
		{"svn: E170000: URL 'file:///x' doesn't exist", KindOther},
	} {
		if got := newError(tc.stderr, nil).Kind; got != tc.want {
			t.Errorf("%q: kind %v, want %v", tc.stderr, got, tc.want)
		}
	}
}

func TestOptsArgs(t *testing.T) {
	got := strings.Join(Opts{Creds: Creds{"me", "pw"}, TrustCert: true}.args([]string{"list"}), " ")
	if !strings.Contains(got, "--username me --password-from-stdin") || strings.Contains(got, "pw") ||
		!strings.Contains(got, "--trust-server-cert-failures=") || !strings.HasSuffix(got, " list") {
		t.Errorf("args = %q", got)
	}
}

func TestCommittedRevision(t *testing.T) {
	if n := committedRevision("Sending a\nTransmitting file data .done\nCommitting transaction...\nCommitted revision 42.\n"); n != 42 {
		t.Errorf("got %d", n)
	}
}

func TestExportImport(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	write(t, filepath.Join(wc, "src", "a.c"), "int a;\n")
	if err := c.Add(ctx, Opts{}, filepath.Join(wc, "src")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "a", []string{filepath.Join(wc, "src"), filepath.Join(wc, "src", "a.c")}); err != nil {
		t.Fatal(err)
	}

	// Export: plain files, no .svn.
	out := filepath.Join(t.TempDir(), "export")
	if err := c.Export(ctx, Opts{}, root+"/trunk", out, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "src", "a.c")); err != nil || string(b) != "int a;\n" {
		t.Errorf("exported a.c = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(out, ".svn")); err == nil {
		t.Error("export left a .svn folder")
	}

	// Import it somewhere new, with a missing parent folder; an ignored
	// build file stays out.
	write(t, filepath.Join(out, "build.o"), "junk")
	var lines []string
	rev, err := c.Import(ctx, Opts{}, out, root+"/mine/project/trunk", "Import from trunk at r2", func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if rev != 3 {
		t.Errorf("import revision %d, want 3 (output %q)", rev, lines)
	}
	ls, err := c.List(ctx, Opts{}, root+"/mine/project/trunk")
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].Name != "src" {
		t.Errorf("imported = %+v (build.o should be ignored)", ls)
	}
	if _, err := os.Stat(filepath.Join(out, ".svn")); err == nil {
		t.Error("import turned the folder into a working copy")
	}

	// Import in place: the folder becomes the working copy, nothing changed,
	// the ignored file still unversioned (svn status hides ignored files).
	if err := c.CheckoutInPlace(ctx, Opts{}, root+"/mine/project/trunk", out, nil); err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, c, out); len(st) != 0 {
		t.Errorf("status after checkout in place = %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "build.o")); string(b) != "junk" {
		t.Error("checkout in place touched an unversioned file")
	}
}

func TestLogSearch(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	f := filepath.Join(wc, "ctrl [v2].c")
	write(t, f, "1\n")
	if err := c.Add(ctx, Opts{}, f); err != nil {
		t.Fatal(err)
	}
	msgs := []string{"Add the motor driver", "Fix IMU drift", "motor: stop on fault", "Docs"}
	for i, m := range msgs {
		write(t, f, strings.Repeat("x", i+2)+"\n")
		if _, err := c.Commit(ctx, Opts{}, m, []string{f}); err != nil {
			t.Fatal(err)
		}
	}
	revs := func(es []LogEntry) []int {
		var r []int
		for _, e := range es {
			r = append(r, e.Revision)
		}
		return r
	}
	search := func(q string, limit int) []int {
		t.Helper()
		es, err := c.LogSearch(ctx, Opts{}, root, 0, limit, q)
		if err != nil {
			t.Fatal(err)
		}
		return revs(es)
	}
	// r2..r5 are the four commits; r1 made the layout.
	for q, want := range map[string]string{
		"motor":       "[4 2]", // in the message, any case, anywhere in it
		"MOTOR":       "[4 2]",
		"imu":         "[3]",
		"ctrl [v2].c": "[5 4 3 2]", // a changed path, with glob characters taken literally
		"Fix*drift":   "[3]",       // the user's own glob
		"nothing":     "[]",
	} {
		if got := fmt.Sprint(search(q, 100)); got != want {
			t.Errorf("search %q = %s, want %s", q, got, want)
		}
	}
	// The limit caps results, not the revisions searched: the oldest match is still found.
	if got := fmt.Sprint(search("motor", 1)); got != "[4]" {
		t.Errorf("search with limit 1 = %s", got)
	}
	if got := fmt.Sprint(search("add the", 100)); got != "[2]" {
		t.Errorf("search for old r2 = %s", got)
	}
}

func TestIgnore(t *testing.T) {
	c, _, wc := newRepo(t)
	ctx := context.Background()
	if v, err := c.GetProp(ctx, Opts{}, "svn:ignore", wc); err != nil || v != "" {
		t.Fatalf("unset property = %q, %v", v, err)
	}
	write(t, filepath.Join(wc, "build", "out.bin"), "x")
	write(t, filepath.Join(wc, "a.log"), "x")
	write(t, filepath.Join(wc, "keep.txt"), "x")
	write(t, filepath.Join(wc, "src", "b.log"), "x")
	if err := c.Add(ctx, Opts{}, filepath.Join(wc, "src")); err != nil {
		t.Fatal(err)
	}
	if err := c.Revert(ctx, Opts{}, filepath.Join(wc, "src", "b.log")); err != nil { // src added, b.log not
		t.Fatal(err)
	}

	// The build folder, by name, in this folder only.
	if added, err := c.AddIgnore(ctx, Opts{}, wc, "build", false); !added || err != nil {
		t.Fatalf("ignore build: %v %v", added, err)
	}
	if added, _ := c.AddIgnore(ctx, Opts{}, wc, "build", false); added {
		t.Error("the same pattern was added twice")
	}
	// *.log here and below.
	if _, err := c.AddIgnore(ctx, Opts{}, wc, "*.log", true); err != nil {
		t.Fatal(err)
	}
	st := statusMap(t, c, wc)
	for _, gone := range []string{"build", "a.log", "src/b.log"} {
		if _, ok := st[gone]; ok {
			t.Errorf("%s still listed after ignoring: %+v", gone, st)
		}
	}
	if st["keep.txt"].Item != "unversioned" || st["."].Props != "modified" {
		t.Errorf("status = %+v", st)
	}
	if v, _ := c.GetProp(ctx, Opts{}, "svn:ignore", wc); v != "build\n" {
		t.Errorf("svn:ignore = %q", v)
	}
	if v, _ := c.GetProp(ctx, Opts{}, "svn:global-ignores", wc); v != "*.log\n" {
		t.Errorf("svn:global-ignores = %q", v)
	}
	// Removing the last pattern removes the property.
	if err := c.SetProp(ctx, Opts{}, "svn:ignore", "", wc); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.GetProp(ctx, Opts{}, "svn:ignore", wc); v != "" {
		t.Errorf("after delete: %q", v)
	}
}

func TestRelocate(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	oldPath := FilePath(root)

	// The same repository at a new address (a copy keeps its UUID).
	newPath := filepath.Join(t.TempDir(), "moved")
	if out, err := exec.Command("svnadmin", "hotcopy", oldPath, newPath).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	newRoot := FileURL(newPath)
	if err := c.Relocate(ctx, Opts{}, root, newRoot, wc); err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(ctx, Opts{}, wc)
	if err != nil {
		t.Fatal(err)
	}
	if info.RepoRoot != newRoot || info.URL != newRoot+"/trunk" {
		t.Errorf("after relocate: root %s url %s", info.RepoRoot, info.URL)
	}

	// A different repository is refused, and the working copy is unchanged.
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("svnadmin", "create", other).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if err := c.Relocate(ctx, Opts{}, newRoot, FileURL(other), wc); err == nil {
		t.Error("relocating to a different repository was allowed")
	}
	if info, _ := c.Info(ctx, Opts{}, wc); info.RepoRoot != newRoot {
		t.Errorf("refused relocate changed the working copy: %s", info.RepoRoot)
	}
}

func TestSearchGlob(t *testing.T) {
	for in, want := range map[string]string{
		"motor":     "*motor*",
		"a[1]":      `*a\[1\]*`,
		"Fix*drift": "Fix*drift", // the user's own glob
		".":         "*[.]*",     // would match "." and ".." on Windows
		"..":        "*[.][.]*",
		"*.*":       "*[.]*",
		"v1.2":      "*v1.2*", // has letters: can't match "." or ".."
	} {
		if got := searchGlob(in); got != want {
			t.Errorf("searchGlob(%q) = %q, want %q", in, got, want)
		}
	}
}
