package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

func testApp(t *testing.T) (*App, string) {
	t.Helper()
	c, err := svn.Find()
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
	root := (&url.URL{Scheme: "file", Path: filepath.ToSlash(repo)}).String()
	wc := filepath.Join(dir, "wc")
	if err := c.Checkout(context.Background(), svn.Opts{}, root, wc, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	return &App{ctx: context.Background(), cfg: cfg, svn: c, creds: map[string]svn.Creds{}}, wc
}

func put(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, a *App, wc string) map[string]string {
	t.Helper()
	st, err := a.Status(wc, false)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, ch := range st.Changes {
		m[ch.Path] = ch.Item
	}
	return m
}

func TestCommitNewFolder(t *testing.T) {
	a, wc := testApp(t)
	// Picking an unversioned folder commits it with everything inside.
	put(t, filepath.Join(wc, "lib", "deep", "a.c"))
	put(t, filepath.Join(wc, "other.c"))
	rev, err := a.Commit(wc, "lib", []string{"lib"}, CommitOptions{AllowMarkers: false, KeepLocks: false})
	if err != nil || rev != 1 {
		t.Fatalf("commit: r%d, %v", rev, err)
	}
	if st := status(t, a, wc); len(st) != 1 || st["other.c"] != "unversioned" {
		t.Errorf("status = %v", st)
	}
}

func TestCommitTakesAddedParent(t *testing.T) {
	a, wc := testApp(t)
	// A file inside an added (not yet committed) folder: the folder goes too,
	// its other file stays behind.
	put(t, filepath.Join(wc, "dir", "a.c"))
	put(t, filepath.Join(wc, "dir", "b.c"))
	if err := a.Add(wc, []string{"dir"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "a only", []string{"dir/a.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); len(st) != 1 || st["dir/b.c"] != "added" {
		t.Errorf("status = %v", st)
	}

	// A file deleted from disk (missing) is deleted in the repository.
	if err := os.Remove(filepath.Join(wc, "dir", "a.c")); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); st["dir/a.c"] != "missing" {
		t.Fatalf("status = %v", st)
	}
	if _, err := a.Commit(wc, "rm a", []string{"dir/a.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); len(st) != 1 {
		t.Errorf("status = %v", st)
	}
}

func TestCommitRefuses(t *testing.T) {
	a, wc := testApp(t)
	if _, err := a.Commit(wc, "  ", []string{"x"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err == nil {
		t.Error("empty message accepted")
	}
	if _, err := a.Commit(wc, "m", nil, CommitOptions{AllowMarkers: false, KeepLocks: false}); err == nil {
		t.Error("nothing to commit accepted")
	}
}

func TestNewFileDiff(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "n.txt"))
	d, err := a.Diff(wc, "n.txt")
	if err != nil || d == "" || d[len(d)-3:] != "+x\n" {
		t.Errorf("diff = %q, %v", d, err)
	}
}

func TestExportImportRefuse(t *testing.T) {
	a, wc := testApp(t)
	info, err := a.Info(wc)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "f.c"))
	if _, err := a.Commit(wc, "f", []string{"f.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	put(t, filepath.Join(src, "x.c"))

	if err := a.Export(info.RepoRoot, src); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("export into an existing folder: %v", err)
	}
	if _, err := a.Import(src, info.RepoRoot, "msg", false); err == nil || !strings.Contains(err.Error(), "has files") {
		t.Errorf("import into a folder with files: %v", err)
	}
	if _, err := a.Import(src, info.RepoRoot+"/new", " ", false); err == nil {
		t.Error("import without a message accepted")
	}
	if _, err := a.Import(filepath.Join(src, "nope"), info.RepoRoot+"/new", "m", false); err == nil {
		t.Error("import of a missing folder accepted")
	}
}

func TestBlameFile(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "a.c"))
	if _, err := a.Commit(wc, "a", []string{"src"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	b, err := a.BlameFile(wc, "src/a.c")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "^/src/a.c" || b.RepoRoot == "" || len(b.Lines) != 1 || b.Lines[0].Text != "x" || b.Lines[0].Revision != 1 {
		t.Errorf("blame = %+v", b)
	}
	if _, err := a.BlameFile(wc, "src"); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("blaming a folder: %v", err)
	}
	if b, err := a.BlameURL(b.RepoRoot + "/src/a.c@1"); err != nil || b.Title != "^/src/a.c@1" {
		t.Errorf("BlameURL = %+v, %v", b, err)
	}
}

// The mistake this guards against: a conflict marked resolved without
// editing the file, then committed with the markers in it.
func TestConflictMarkersGuard(t *testing.T) {
	a, wc := testApp(t)
	info, err := a.Info(wc)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(wc, "main.c")
	if err := os.WriteFile(f, []byte("init();\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "add", []string{"main.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	// Someone else changes the line.
	other := filepath.Join(t.TempDir(), "other")
	if err := a.svn.Checkout(context.Background(), svn.Opts{}, info.URL, other, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "main.c"), []byte("init(); // theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.svn.Commit(context.Background(), svn.Opts{}, "theirs", []string{filepath.Join(other, "main.c")}); err != nil {
		t.Fatal(err)
	}
	// We change it too, and update into a conflict.
	if err := os.WriteFile(f, []byte("init(); // mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.svn.Update(context.Background(), svn.Opts{}, wc, nil); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); st["main.c"] != "conflicted" {
		t.Fatalf("expected a conflict: %v", st)
	}

	// "Mark resolved" without editing: refused, and the conflict stays.
	err = a.Resolve(wc, "working", []string{"main.c"}, false)
	if err == nil || !strings.Contains(err.Error(), `"kind":"markers"`) || !strings.Contains(err.Error(), "main.c (line 1)") {
		t.Fatalf("resolve with markers: %v", err)
	}
	if st := status(t, a, wc); st["main.c"] != "conflicted" {
		t.Errorf("refused resolve still resolved it: %v", st)
	}

	// Resolved anyway (the user insisted), then committed: refused too.
	if err := a.Resolve(wc, "working", []string{"main.c"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "oops", []string{"main.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err == nil || !strings.Contains(err.Error(), `"kind":"markers"`) {
		t.Fatalf("commit with markers: %v", err)
	}

	// Fixed by hand: both go through.
	if err := os.WriteFile(f, []byte("init(); // mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "fixed", []string{"main.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatalf("commit after fixing: %v", err)
	}
}

func TestLockFiles(t *testing.T) {
	a, wc := testApp(t)
	ctx := context.Background()
	info, err := a.Info(wc)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "cad.step"))
	put(t, filepath.Join(wc, "code.c"))
	if err := a.Add(wc, []string{"cad.step", "code.c"}); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("svn", "propset", "svn:needs-lock", "*", filepath.Join(wc, "cad.step")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := a.Commit(wc, "add", []string{"cad.step", "code.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}

	locks := func(w string) map[string]LockedFile {
		t.Helper()
		l, err := a.Locks(w)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]LockedFile{}
		for _, f := range l {
			m[f.Path] = f
		}
		return m
	}
	if l := locks(wc); len(l) != 1 || !l["cad.step"].NeedsLock || l["cad.step"].State != "" {
		t.Errorf("before locking: %+v", l)
	}

	// Another working copy locks both.
	other := filepath.Join(t.TempDir(), "other")
	if err := a.svn.Checkout(ctx, svn.Opts{}, info.URL, other, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.LockFiles(other, []string{"cad.step", "code.c"}, "editing", false); err != nil {
		t.Fatal(err)
	}
	l := locks(wc)
	if len(l) != 2 || l["cad.step"].State != "other" || l["code.c"].State != "other" || l["code.c"].Lock.Comment != "editing" || l["code.c"].NeedsLock {
		t.Errorf("seen from here: %+v", l)
	}

	// Locking here: refused, saying who.
	err = a.LockFiles(wc, []string{"cad.step"}, "", false)
	if err == nil || !strings.Contains(err.Error(), `"kind":"lockconflict"`) || !strings.Contains(err.Error(), "/cad.step (locked by ") {
		t.Fatalf("lock conflict: %v", err)
	}
	// Taking it over works; it's ours now.
	if err := a.LockFiles(wc, []string{"cad.step"}, "mine now", true); err != nil {
		t.Fatal(err)
	}
	if l := locks(wc); l["cad.step"].State != "mine" || l["cad.step"].Lock.Comment != "mine now" {
		t.Errorf("after taking over: %+v", l["cad.step"])
	}
	if l := locks(other); l["cad.step"].State != "stolen" {
		t.Errorf("the other copy after the takeover: %+v", l["cad.step"])
	}

	// Commit with keepLocks keeps it; a plain commit releases it.
	if err := os.Chmod(filepath.Join(wc, "cad.step"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wc, "cad.step"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "v2", []string{"cad.step"}, CommitOptions{AllowMarkers: false, KeepLocks: true}); err != nil {
		t.Fatal(err)
	}
	if l := locks(wc); l["cad.step"].State != "mine" {
		t.Errorf("after commit keeping locks: %+v", l["cad.step"])
	}
	if err := os.WriteFile(filepath.Join(wc, "cad.step"), []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "v3", []string{"cad.step"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	if l := locks(wc); l["cad.step"].State != "" {
		t.Errorf("after a plain commit: %+v", l["cad.step"])
	}

	// The other copy's code.c lock: break it from here.
	if err := a.UnlockFiles(wc, []string{"code.c"}, false); err == nil {
		t.Error("unlocking someone else's lock without breaking it worked")
	}
	if err := a.UnlockFiles(wc, []string{"code.c"}, true); err != nil {
		t.Fatal(err)
	}
	if l := locks(wc); l["code.c"].State != "" {
		t.Errorf("after breaking: %+v", l["code.c"])
	}
}

// layoutApp is testApp with a trunk/branches/tags repository; it returns the
// app, a checkout of trunk, and the repository root URL.
func layoutApp(t *testing.T) (*App, string, string) {
	t.Helper()
	a, root := testApp(t)
	info, err := a.Info(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, d := range []string{"trunk", "branches", "tags"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(root, "trunk", "src", "main.c"))
	if _, err := a.Commit(root, "layout", []string{"trunk", "branches", "tags"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	wc := filepath.Join(t.TempDir(), "trunk-wc")
	if err := a.svn.Checkout(ctx, svn.Opts{}, info.RepoRoot+"/trunk", wc, nil); err != nil {
		t.Fatal(err)
	}
	return a, wc, info.RepoRoot
}

func TestBranches(t *testing.T) {
	a, wc, root := layoutApp(t)
	ctx := context.Background()

	b, err := a.Branches(wc)
	if err != nil {
		t.Fatal(err)
	}
	if b.Layout.Kind != "trunk" || len(b.Branches) != 1 || !b.Branches[0].Current || b.Branches[0].Commit.Revision != 1 {
		t.Fatalf("fresh repo: %+v", b)
	}

	// Bad names, unknown kinds and sources are refused before anything happens.
	for _, c := range [][3]string{{"branch", "a b", "head"}, {"branch", "x/y", "head"}, {"trunk", "x", "head"}, {"branch", "ok", "yesterday"}} {
		if _, err := a.CreateBranch(wc, c[0], c[1], c[2], ""); err == nil {
			t.Errorf("CreateBranch%v accepted", c)
		}
	}

	// From head and from the working copy's revision.
	put(t, filepath.Join(wc, "src", "new.c"))
	if _, err := a.Commit(wc, "new.c", []string{"src/new.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err) // r2; the working copy root is still at r1
	}
	if rev, err := a.CreateBranch(wc, "branch", "from-head", "head", ""); err != nil || rev != 3 {
		t.Fatalf("branch from head: r%d %v", rev, err)
	}
	if _, err := a.CreateBranch(wc, "tag", "v1.0", "wc", "Release 1.0"); err != nil {
		t.Fatal(err)
	}
	ls, _ := a.svn.List(ctx, svn.Opts{}, root+"/branches/from-head/src")
	if len(ls) != 2 {
		t.Errorf("branch from head should have new.c: %+v", ls)
	}
	ls, _ = a.svn.List(ctx, svn.Opts{}, root+"/tags/v1.0/src")
	if len(ls) != 1 {
		t.Errorf("tag from the working copy's r1 shouldn't have new.c: %+v", ls)
	}
	log, _ := a.svn.Log(ctx, svn.Opts{}, root+"/tags/v1.0", 0, 1)
	if len(log) != 1 || log[0].Message != "Release 1.0" {
		t.Errorf("tag message: %+v", log)
	}

	// An existing name is refused (svn would copy INTO it).
	if _, err := a.CreateBranch(wc, "branch", "from-head", "head", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing branch: %v", err)
	}

	b, err = a.Branches(wc)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, br := range b.Branches {
		names = append(names, br.Kind+":"+br.Name)
	}
	if strings.Join(names, " ") != "trunk:trunk branch:from-head tag:v1.0" {
		t.Errorf("branches = %v", names)
	}

	// A subfolder checkout: Branches offers the same subfolder of each branch.
	sub := filepath.Join(t.TempDir(), "sub")
	if err := a.svn.Checkout(ctx, svn.Opts{}, root+"/trunk/src", sub, nil); err != nil {
		t.Fatal(err)
	}
	b, err = a.Branches(sub)
	if err != nil {
		t.Fatal(err)
	}
	for _, br := range b.Branches {
		if !strings.HasSuffix(br.URL, "/src") {
			t.Errorf("subfolder checkout: %s URL %s", br.Name, br.URL)
		}
	}
	// A branch made from a subfolder checkout still copies the whole trunk.
	if _, err := a.CreateBranch(sub, "branch", "whole", "head", ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.svn.Exists(ctx, svn.Opts{}, root+"/branches/whole/src/main.c"); !ok {
		t.Error("branch from a subfolder checkout should hold the whole trunk")
	}

	// Local changes are counted (they'd come along on a switch).
	if err := os.WriteFile(filepath.Join(wc, "src", "main.c"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := a.Branches(wc); b.Changes != 1 {
		t.Errorf("changes = %d", b.Changes)
	}

	// Delete: not the current one, not trunk; a branch goes.
	if _, err := a.DeleteBranch(wc, "trunk", "trunk", ""); err == nil {
		t.Error("deleting trunk was allowed")
	}
	if err := a.svn.Switch(ctx, svn.Opts{}, wc, root+"/branches/from-head", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteBranch(wc, "branch", "from-head", ""); err == nil || !strings.Contains(err.Error(), "Switch to another") {
		t.Errorf("deleting the current branch: %v", err)
	}
	if _, err := a.DeleteBranch(wc, "branch", "whole", ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.svn.Exists(ctx, svn.Opts{}, root+"/branches/whole"); ok {
		t.Error("deleted branch still there")
	}
}

func TestBranchesNoLayout(t *testing.T) {
	a, wc := testApp(t) // a checkout of the repository root: no trunk/branches/tags
	b, err := a.Branches(wc)
	if err != nil {
		t.Fatal(err)
	}
	if b.Layout.Kind != "" || len(b.Branches) != 0 {
		t.Errorf("no layout: %+v", b)
	}
	if _, err := a.CreateBranch(wc, "branch", "x", "head", ""); err == nil {
		t.Error("branch without a layout was allowed")
	}
}

func TestUndoRevisionNewer(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "f.c"))
	if _, err := a.Commit(wc, "f", []string{"f.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err) // r1; the working copy root is still at r0
	}
	err := a.UndoRevision(wc, 1)
	if err == nil || !strings.Contains(err.Error(), `"kind":"needupdate"`) || !strings.Contains(err.Error(), "newer than your working copy") {
		t.Errorf("undoing a revision newer than the working copy: %v", err)
	}
	if err := a.UndoRevision(wc, 0); err == nil {
		t.Error("undoing r0 accepted")
	}
}

func TestIgnoreFromChanges(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "main.c"))
	if _, err := a.Commit(wc, "src", []string{"src"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "src", "main.tmp"))
	put(t, filepath.Join(wc, "src", "util.tmp"))
	put(t, filepath.Join(wc, "src", "old.o")) // svn ignores *.o by default
	put(t, filepath.Join(wc, "build", "app"))
	put(t, filepath.Join(wc, "notes"))

	if _, err := a.Ignore(wc, []string{"src/main.c"}, false, false); err == nil {
		t.Error("ignoring a versioned file was allowed")
	}
	if _, err := a.Ignore(wc, []string{"notes"}, true, false); err == nil {
		t.Error("ignoring by extension a name without one was allowed")
	}
	if _, err := a.Ignore(wc, []string{"src/old.o"}, false, false); err == nil || !strings.Contains(err.Error(), "ignored already") {
		t.Errorf("ignoring an already-ignored file: %v", err)
	}
	// *.tmp in src (both files: one pattern), and build by name.
	if f, err := a.Ignore(wc, []string{"src/main.tmp", "src/util.tmp"}, true, false); fmt.Sprint(f) != "[src]" || err != nil {
		t.Fatalf("ignore *.tmp: %v %v", f, err)
	}
	if f, err := a.Ignore(wc, []string{"build"}, false, false); fmt.Sprint(f) != "[.]" || err != nil {
		t.Fatalf("ignore build: %v %v", f, err)
	}
	st := status(t, a, wc)
	if len(st) != 3 || st["notes"] != "unversioned" || st["src"] != "normal" || st["."] != "normal" {
		t.Errorf("status after ignoring = %v", st)
	}
	// The property changes commit like anything else.
	if _, err := a.Commit(wc, "ignore build output", []string{".", "src"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); len(st) != 1 || st["notes"] != "unversioned" {
		t.Errorf("status after committing the ignores = %v", st)
	}
}

func TestRelocateApp(t *testing.T) {
	a, wc := testApp(t)
	info, err := a.Info(wc)
	if err != nil {
		t.Fatal(err)
	}
	old := info.RepoRoot
	if err := a.cfg.Update(func(c *config.Config) {
		c.AddWorkingCopy(config.WorkingCopy{Name: "wc", Path: wc, URL: info.URL, RepoRoot: old})
		c.AddServer(config.Server{Name: "repo", URL: old})
		c.AddServer(config.Server{Name: "other", URL: "https://elsewhere/repo"})
	}); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if out, err := exec.Command("svnadmin", "hotcopy", strings.TrimPrefix(old, "file://"), moved).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	newRoot := "file://" + filepath.ToSlash(moved)

	for _, bad := range []string{"", "ftp://x/repo", "not a url", old} {
		if _, err := a.Relocate(wc, bad); err == nil {
			t.Errorf("Relocate(%q) accepted", bad)
		}
	}
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("svnadmin", "create", other).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := a.Relocate(wc, "file://"+filepath.ToSlash(other)); err == nil || !strings.Contains(err.Error(), "isn't the same repository") {
		t.Errorf("relocating to another repository: %v", err)
	}
	got, err := a.Relocate(wc, newRoot+"/")
	if err != nil || got != newRoot {
		t.Fatalf("relocate: %q %v", got, err)
	}
	a.cfg.Read(func(c *config.Config) {
		if c.WorkingCopies[0].RepoRoot != newRoot || c.Servers[0].URL != newRoot || c.Servers[1].URL != "https://elsewhere/repo" {
			t.Errorf("saved settings: %+v %+v", c.WorkingCopies, c.Servers)
		}
	})
	// Still works against the new address.
	put(t, filepath.Join(wc, "after-move.txt"))
	if _, err := a.Commit(wc, "after the move", []string{"after-move.txt"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
}

func TestIncoming(t *testing.T) {
	a, wc := testApp(t)
	info, _ := a.Info(wc)
	put(t, filepath.Join(wc, "a.c"))
	put(t, filepath.Join(wc, "b.c"))
	if _, err := a.Commit(wc, "two files", []string{"a.c", "b.c"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	// Our own commit isn't "incoming", even before updating.
	if n, err := a.Incoming(wc); n != 0 || err != nil {
		t.Errorf("after our own commit: %d %v", n, err)
	}
	// Someone else changes both files in two commits.
	other := filepath.Join(t.TempDir(), "other")
	if err := a.svn.Checkout(context.Background(), svn.Opts{}, info.URL, other, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.c", "b.c"} {
		if err := os.WriteFile(filepath.Join(other, f), []byte("theirs\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := a.svn.Commit(context.Background(), svn.Opts{}, "theirs", []string{filepath.Join(other, f)}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := a.Incoming(wc); n != 2 || err != nil {
		t.Errorf("after two incoming changes: %d %v", n, err)
	}
	if err := a.svn.Update(context.Background(), svn.Opts{}, wc, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := a.Incoming(wc); n != 0 {
		t.Errorf("after updating: %d", n)
	}
}

func TestIgnoreRecursiveOnce(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "deep", "keep.c"))
	if _, err := a.Commit(wc, "src", []string{"src"}, CommitOptions{AllowMarkers: false, KeepLocks: false}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "a.tmp"))
	put(t, filepath.Join(wc, "src", "b.tmp"))
	put(t, filepath.Join(wc, "src", "deep", "c.tmp"))
	// One rule on the top folder covers all three; no copies below it.
	f, err := a.Ignore(wc, []string{"a.tmp", "src/b.tmp", "src/deep/c.tmp"}, true, true)
	if err != nil || fmt.Sprint(f) != "[.]" {
		t.Fatalf("folders = %v, %v", f, err)
	}
	if st := status(t, a, wc); len(st) != 1 || st["."] != "normal" {
		t.Errorf("status = %v", st)
	}
	// Files in src and src/deep: one rule, on src (the folder holding both).
	put(t, filepath.Join(wc, "src", "x.bak"))
	put(t, filepath.Join(wc, "src", "deep", "y.bak"))
	if f, err := a.Ignore(wc, []string{"src/x.bak", "src/deep/y.bak"}, true, true); err != nil || fmt.Sprint(f) != "[src]" {
		t.Errorf("folders = %v, %v", f, err)
	}
	if commonDir("src/a", "src/b") != "src" || commonDir("a", "b") != "." || commonDir(".", "src") != "." || commonDir("src/x", "src/x") != "src/x" {
		t.Error("commonDir")
	}
}

func TestMergePlanAndGuards(t *testing.T) {
	a, tw, root := layoutApp(t)
	ctx := context.Background()
	if _, err := a.CreateBranch(tw, "branch", "b", "head", ""); err != nil {
		t.Fatal(err) // r2
	}
	bw := filepath.Join(t.TempDir(), "bw")
	if err := a.svn.Checkout(ctx, svn.Opts{}, root+"/branches/b", bw, nil); err != nil {
		t.Fatal(err)
	}
	// Two trunk commits.
	for i, name := range []string{"one.c", "two.c"} {
		put(t, filepath.Join(tw, "src", name))
		if _, err := a.Commit(tw, fmt.Sprintf("trunk %d", i+1), []string{"src/" + name}, CommitOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := a.MergePlan(bw, root+"/trunk")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Total != 2 || len(plan.Revisions) != 2 || plan.Revisions[0].Message != "trunk 2" || plan.Target != root+"/branches/b" {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := a.MergePlan(bw, root+"/branches/b"); err == nil {
		t.Error("merging a branch into itself was offered")
	}

	// A local change blocks the merge.
	if err := os.WriteFile(filepath.Join(bw, "src", "main.c"), []byte("edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.MergeInto(bw, root+"/trunk", nil); err == nil || !strings.Contains(err.Error(), `"kind":"needclean"`) {
		t.Errorf("merge with a local change: %v", err)
	}
	if err := a.Revert(bw, []string{"src/main.c"}); err != nil {
		t.Fatal(err)
	}

	// After a merge (done here through svn), committing only the folder's
	// merge record is refused; the whole merge goes through.
	if err := a.svn.MergeFrom(ctx, svn.Opts{}, root+"/trunk", bw, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, err = a.Commit(bw, "merge record only", []string{"."}, CommitOptions{})
	if err == nil || !strings.Contains(err.Error(), `"kind":"partialmerge"`) || !strings.Contains(err.Error(), "src/one.c") {
		t.Fatalf("partial merge commit: %v", err)
	}
	var all []string
	for p, item := range status(t, a, bw) {
		if item != "unversioned" {
			all = append(all, p)
		}
	}
	if _, err := a.Commit(bw, "Merge trunk", all, CommitOptions{}); err != nil {
		t.Fatalf("whole merge commit: %v", err)
	}
	if plan, _ := a.MergePlan(bw, root+"/trunk"); plan.Total != 0 {
		t.Errorf("after merging, still eligible: %+v", plan)
	}
	// A plain commit without any merge record isn't affected by the guard.
	put(t, filepath.Join(bw, "x.c"))
	put(t, filepath.Join(bw, "y.c"))
	if _, err := a.Commit(bw, "just x", []string{"x.c"}, CommitOptions{}); err != nil {
		t.Errorf("ordinary partial commit: %v", err)
	}
}
