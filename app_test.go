package main

import (
	"context"
	"fmt"
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
	root := svn.FileURL(repo)
	wc := filepath.Join(dir, "wc")
	if err := c.Checkout(context.Background(), svn.Opts{}, root, wc, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	return &App{ctx: context.Background(), cfg: cfg, svn: c, creds: map[string]svn.Creds{}, noEvents: true}, wc
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
	if err := a.svn.SetProp(ctx, svn.Opts{}, "svn:needs-lock", "*", filepath.Join(wc, "cad.step")); err != nil {
		t.Fatal(err)
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
	if out, err := exec.Command("svnadmin", "hotcopy", svn.FilePath(old), moved).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	newRoot := svn.FileURL(moved)

	for _, bad := range []string{"", "ftp://x/repo", "not a url", old} {
		if _, err := a.Relocate(wc, bad); err == nil {
			t.Errorf("Relocate(%q) accepted", bad)
		}
	}
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("svnadmin", "create", other).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := a.Relocate(wc, svn.FileURL(other)); err == nil || !strings.Contains(err.Error(), "isn't the same repository") {
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

func TestSetChangelistApp(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "main.c"))
	if _, err := a.Commit(wc, "src", []string{"src"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "new.c"))
	for _, c := range []struct {
		rel  []string
		name string
		want string
	}{
		{nil, "x", "Pick the files"},
		{[]string{"src"}, "x", "is a folder"},
		{[]string{"new.c"}, "x", "Add it first"},
		{[]string{"src/main.c"}, " x", "can't be empty"},
	} {
		if err := a.SetChangelist(wc, c.rel, c.name); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("SetChangelist(%v, %q) = %v, want %q", c.rel, c.name, err, c.want)
		}
	}
	if err := a.SetChangelist(wc, []string{"src/main.c"}, "parser"); err != nil {
		t.Fatal(err)
	}
	st, err := a.Status(wc, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ch := range st.Changes {
		if ch.Path == "src/main.c" {
			found = ch.Changelist == "parser" && ch.Item == "normal"
		}
	}
	if !found {
		t.Errorf("status = %+v", st.Changes)
	}
}

func TestSetPropsApp(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "main.c"))
	if _, err := a.Commit(wc, "src", []string{"src"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	// A commit inside src leaves the folder itself at an older revision: a
	// property change on it then only commits if SetProps updated it first.
	if err := os.WriteFile(filepath.Join(wc, "src", "main.c"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "edit", []string{"src/main.c"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProps(wc, "src", map[string]string{"svn:ignore": "*.o\n*.tmp\n", "owner": "firmware"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProps(wc, "src/main.c", map[string]string{"svn:eol-style": "native", "svn:needs-lock": "*"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "props", []string{"src", "src/main.c"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	p, err := a.Props(wc, "src")
	if err != nil || !p.IsDir || fmt.Sprint(p.Props) != "[{owner firmware} {svn:ignore *.o\n*.tmp\n}]" {
		t.Errorf("Props(src) = %+v, %v", p, err)
	}
	// Removing one, changing another.
	if err := a.SetProps(wc, "src/main.c", map[string]string{"svn:needs-lock": "", "svn:eol-style": "LF"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.Props(wc, "src/main.c"); p.IsDir || fmt.Sprint(p.Props) != "[{svn:eol-style LF}]" {
		t.Errorf("Props(main.c) = %+v", p)
	}
	// The top folder, and a folder that's only added.
	put(t, filepath.Join(wc, "doc", "a.md"))
	if err := a.Add(wc, []string{"doc"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProps(wc, "doc", map[string]string{"svn:ignore": "*.pdf"}); err != nil {
		t.Errorf("on an added folder: %v", err)
	}
	if err := a.SetProps(wc, ".", map[string]string{"svn:global-ignores": "build"}); err != nil {
		t.Errorf("on the top folder: %v", err)
	}
	for _, c := range []struct {
		set  map[string]string
		want string
	}{
		{map[string]string{"svn:mergeinfo": "/x:1"}, "changed by merging"},
		{map[string]string{"bad name": "x"}, "valid property name"},
		{map[string]string{"svn:eol-style": "sideways"}, "native, LF"},
		// Checked before anything changes: svn:eol-style isn't set either.
		{map[string]string{"svn:eol-style": "CRLF", "svn:ignore": "*.o"}, "is for folders"},
	} {
		err := a.SetProps(wc, "src/main.c", c.set)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("SetProps(%v) = %v, want %q", c.set, err, c.want)
		}
	}
}

func TestSetPropsFolderOnly(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "a.c"))
	if _, err := a.Commit(wc, "a", []string{"a.c"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProps(wc, ".", map[string]string{"svn:needs-lock": "*"}); err == nil || !strings.Contains(err.Error(), "is for files") {
		t.Errorf("needs-lock on a folder: %v", err)
	}
	// Removing is always allowed (whatever got there).
	if err := a.SetProps(wc, "a.c", map[string]string{"svn:ignore": ""}); err != nil {
		t.Errorf("removing: %v", err)
	}
	if p, _ := a.Props(wc, "a.c"); len(p.Props) != 0 {
		t.Errorf("a.c props = %v", p.Props)
	}
}

func TestDiffFolderOnly(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "main.c"))
	if _, err := a.Commit(wc, "src", []string{"src"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wc, "src", "main.c"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProps(wc, "src", map[string]string{"svn:ignore": "*.o"}); err != nil {
		t.Fatal(err)
	}
	d, err := a.Diff(wc, "src")
	if err != nil || !strings.Contains(d, "svn:ignore") || strings.Contains(d, "changed") {
		t.Errorf("folder diff = %q, %v", d, err)
	}
	if d, _ := a.Diff(wc, "src/main.c"); !strings.Contains(d, "+changed") {
		t.Errorf("file diff = %q", d)
	}
}

func TestSetLook(t *testing.T) {
	a, _ := testApp(t)
	if s := a.Startup(); s.Theme != "system" || s.Font != "mono" || s.Zoom != 100 {
		t.Errorf("default look = %q, %q, %d", s.Theme, s.Font, s.Zoom)
	}
	if err := a.SetLook("light", "regular", 125); err != nil {
		t.Fatal(err)
	}
	if s := a.Startup(); s.Theme != "light" || s.Font != "regular" || s.Zoom != 125 {
		t.Errorf("look = %q, %q, %d", s.Theme, s.Font, s.Zoom)
	}
	if err := a.SetLook("pink", "mono", 100); err == nil {
		t.Error("an unknown theme was saved")
	}
	if err := a.SetLook("dark", "mono", 400); err == nil {
		t.Error("a zoom of 400% was saved")
	}
	if err := a.SetLook("system", "mono", 100); err != nil {
		t.Fatal(err)
	}
	if s := a.Startup(); s.Theme != "system" || s.Font != "mono" || s.Zoom != 100 {
		t.Errorf("back to the default: %q, %q, %d", s.Theme, s.Font, s.Zoom)
	}
}

func TestSaveAndApplyPatch(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "main.c"))
	if _, err := a.Commit(wc, "src", []string{"src"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	info, _ := a.Info(wc)
	other := filepath.Join(t.TempDir(), "other")
	if err := a.svn.Checkout(context.Background(), svn.Opts{}, info.URL, other, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wc, "src", "main.c"), []byte("x\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "notes.txt")) // new, not added: left out
	file := filepath.Join(t.TempDir(), "my.patch")

	if _, err := a.SavePatch(wc, []string{"src/main.c"}, "my.patch", false); err == nil || !strings.Contains(err.Error(), "full path") {
		t.Errorf("relative file: %v", err)
	}
	if _, err := a.SavePatch(wc, []string{"notes.txt"}, file, false); err == nil || !strings.Contains(err.Error(), "Add") {
		t.Errorf("only a new file: %v", err)
	}
	saved, err := a.SavePatch(wc, []string{"src/main.c", "notes.txt"}, file, false)
	if err != nil || saved.Files != 1 || fmt.Sprint(saved.Skipped) != "[notes.txt]" {
		t.Fatalf("SavePatch = %+v, %v", saved, err)
	}
	if _, err := a.SavePatch(wc, []string{"src/main.c"}, file, false); err == nil || !strings.Contains(err.Error(), `"exists"`) {
		t.Errorf("saving over a file without overwrite: %v", err)
	}
	if _, err := a.SavePatch(wc, []string{"src/main.c"}, file, true); err != nil {
		t.Errorf("overwrite: %v", err)
	}

	res, err := a.ApplyPatch(other, file, false)
	if err != nil || len(res) != 1 || res[0].Path != "src/main.c" || res[0].Action != "updated" {
		t.Fatalf("ApplyPatch = %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "src", "main.c")); string(b) != "x\nmore\n" {
		t.Errorf("applied: %q", b)
	}
	if _, err := a.ApplyPatch(other, filepath.Join(t.TempDir(), "none.patch"), true); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Errorf("missing patch file: %v", err)
	}
}

func TestTagAtRevisionAndCompare(t *testing.T) {
	a, wc, root := layoutApp(t)
	put(t, filepath.Join(wc, "f.txt")) // r2
	if _, err := a.Commit(wc, "f", []string{"f.txt"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wc, "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(wc, "change", []string{"f.txt"}, CommitOptions{}); err != nil {
		t.Fatal(err) // r3
	}
	// A tag of trunk as it was at r2 (before the change).
	if _, err := a.CreateBranch(wc, "tag", "v1", "r2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateBranch(wc, "tag", "v2", "rx", ""); err == nil {
		t.Error("a bad revision was taken")
	}
	ch, err := a.Compare(root+"/tags/v1", 0, root+"/trunk", 0)
	if err != nil || fmt.Sprint(ch) != "[{f.txt modified none file}]" {
		t.Fatalf("Compare = %v, %v", ch, err)
	}
	d, err := a.CompareDiff(root+"/tags/v1", 0, root+"/trunk", 0, "f.txt", "modified")
	if err != nil || !strings.Contains(d, "+changed") {
		t.Errorf("CompareDiff = %q, %v", d, err)
	}
	// The same folder at two revisions.
	if ch, err := a.Compare(root+"/trunk", 2, root+"/trunk", 3); err != nil || len(ch) != 1 {
		t.Errorf("by revision = %v, %v", ch, err)
	}
}

func TestMoveFile(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "a.txt"))
	if _, err := a.Commit(wc, "a", []string{"a.txt"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(wc, "new.txt"))
	for _, c := range []struct{ from, to, want string }{
		{"a.txt", "../outside.txt", "isn't inside"},
		{"a.txt", "a.txt", "same as the old"},
		{"a.txt", "new.txt", "already exists"},
		{"nope.txt", "x.txt", "isn't there"},
	} {
		if err := a.MoveFile(wc, c.from, c.to); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("MoveFile(%s, %s) = %v, want %q", c.from, c.to, err, c.want)
		}
	}
	if err := a.MoveFile(wc, "a.txt", "docs/b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveFile(wc, "new.txt", "docs/new2.txt"); err != nil { // not in svn: just renamed
		t.Fatal(err)
	}
	st := status(t, a, wc)
	if st["a.txt"] != "deleted" || st["docs/b.txt"] != "added" || st["docs/new2.txt"] != "unversioned" {
		t.Errorf("status = %v", st)
	}
}

func TestRenameCandidates(t *testing.T) {
	a, wc := testApp(t)
	for _, p := range []string{"same.txt", "edited.txt", "dir/f.txt"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wc, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wc, p), []byte("content of "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Commit(wc, "init", []string{"same.txt", "edited.txt", "dir"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	// Renamed in a file manager: same content under a new name; the same
	// name in another folder, edited; a folder renamed.
	mv := func(from, to string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wc, to)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(wc, from), filepath.Join(wc, to)); err != nil {
			t.Fatal(err)
		}
	}
	mv("same.txt", "renamed.txt")
	mv("edited.txt", "sub/edited.txt")
	if err := os.WriteFile(filepath.Join(wc, "sub", "edited.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mv("dir", "folder")
	put(t, filepath.Join(wc, "unrelated.txt"))

	pairs, err := a.RenameCandidates(wc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RenamePair{}
	for _, p := range pairs {
		got[p.From] = p
	}
	if len(pairs) != 3 || got["same.txt"].To != "renamed.txt" || !got["same.txt"].Same ||
		got["edited.txt"].To != "sub/edited.txt" || got["edited.txt"].Same ||
		got["dir"].To != "folder" || !got["dir"].Dir {
		t.Fatalf("pairs = %+v", pairs)
	}
	if err := a.RecordRenames(wc, pairs); err != nil {
		t.Fatal(err)
	}
	st := status(t, a, wc)
	for _, p := range []string{"same.txt", "edited.txt", "dir"} {
		if st[p] != "deleted" {
			t.Errorf("%s: %s", p, st[p])
		}
	}
	for _, p := range []string{"renamed.txt", "sub/edited.txt", "folder"} {
		if st[p] != "added" {
			t.Errorf("%s: %s", p, st[p])
		}
	}
	if b, _ := os.ReadFile(filepath.Join(wc, "sub", "edited.txt")); string(b) != "changed\n" {
		t.Errorf("the edit was lost: %q", b)
	}
	if st["unrelated.txt"] != "unversioned" {
		t.Errorf("unrelated.txt: %s", st["unrelated.txt"])
	}
}

func TestFoldersAndRepoChanges(t *testing.T) {
	a, _, root := layoutApp(t)
	for _, d := range []string{"docs", "cad"} {
		if _, err := a.RepoMkdir(root+"/trunk/"+d, "folder"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.RepoMove(root+"/trunk/cad", root+"/trunk/cad", "x"); err == nil {
		t.Error("moving onto itself was allowed")
	}
	if _, err := a.RepoMove(root+"/trunk/cad", root+"/trunk/docs", "x"); err == nil || !strings.Contains(err.Error(), "already there") {
		t.Errorf("moving onto an existing folder: %v", err)
	}
	if _, err := a.RepoMove(root+"/trunk/cad", root+"/trunk/models", "rename"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RepoDelete(root+"/trunk/docs", "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RepoMkdir(root+"/trunk/docs", "again"); err != nil {
		t.Fatal(err)
	}

	// A sparse checkout: the top folder only, then choose.
	sp := filepath.Join(t.TempDir(), "sparse")
	if _, err := a.Checkout(root+"/trunk", sp, "everything"); err == nil {
		t.Error("an unknown depth was taken")
	}
	if _, err := a.Checkout(root+"/trunk", sp, svn.DepthImmediates); err != nil {
		t.Fatal(err)
	}
	f, err := a.Folders(sp, "")
	if err != nil || fmt.Sprint(f) != "[{docs empty} {models empty} {src empty}]" {
		t.Fatalf("Folders = %v, %v", f, err)
	}
	if err := a.SetFolders(sp, []string{"src"}, []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if f, _ := a.Folders(sp, ""); fmt.Sprint(f) != "[{docs exclude} {models empty} {src infinity}]" {
		t.Errorf("after choosing: %v", f)
	}
	if _, err := os.Stat(filepath.Join(sp, "src", "main.c")); err != nil {
		t.Errorf("src wasn't fetched: %v", err)
	}
	// A folder with local changes isn't removed.
	if err := os.WriteFile(filepath.Join(sp, "src", "main.c"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.SetFolders(sp, nil, []string{"src"}); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Errorf("removing a changed folder: %v", err)
	}
	if err := a.SetFolders(sp, nil, []string{"../x"}); err == nil {
		t.Error("a path outside the working copy was taken")
	}
	// Inside a folder the working copy doesn't have yet.
	if f, err := a.Folders(sp, "models"); err != nil || len(f) != 0 {
		t.Errorf("Folders(models) = %v, %v", f, err)
	}
}

func TestFormatExternalsChecks(t *testing.T) {
	a := &App{}
	for _, c := range []struct {
		ext  []svn.External
		want string
	}{
		{[]svn.External{{URL: "", Dir: "x"}}, "needs a URL"},
		{[]svn.External{{URL: "^/a b", Dir: "x"}}, "can't have spaces"},
		{[]svn.External{{URL: "^/a", Dir: "x", Rev: "HEAD"}}, "a number"},
	} {
		if _, err := a.FormatExternals(c.ext); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("FormatExternals(%v) = %v, want %q", c.ext, err, c.want)
		}
	}
	if s, err := a.FormatExternals([]svn.External{{URL: " ^/lib ", Dir: " lib ", Rev: "12"}}); err != nil || s != "^/lib@12 lib\n" {
		t.Errorf("FormatExternals = %q, %v", s, err)
	}
}

func TestCommitMoves(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "dir", "f.txt"))
	put(t, filepath.Join(wc, "dir", "sub", "g.txt"))
	put(t, filepath.Join(wc, "a.txt"))
	if _, err := a.Commit(wc, "init", []string{"dir", "a.txt"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveFile(wc, "dir", "folder"); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveFile(wc, "a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	// Both halves of each move, as the page ticks them.
	if _, err := a.Commit(wc, "moves", []string{"dir", "folder", "a.txt", "b.txt"}, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); len(st) != 0 {
		t.Errorf("left over after the commit: %v", st)
	}
	for _, p := range []string{"folder/f.txt", "folder/sub/g.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(wc, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	info, _ := a.Info(wc)
	if l, err := a.svn.List(context.Background(), svn.Opts{}, info.URL+"/folder/sub"); err != nil || len(l) != 1 {
		t.Errorf("folder/sub in the repository: %v, %v", l, err)
	}
}
