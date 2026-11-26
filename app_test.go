package main

import (
	"context"
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
	rev, err := a.Commit(wc, "lib", []string{"lib"})
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
	if _, err := a.Commit(wc, "a only", []string{"dir/a.c"}); err != nil {
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
	if _, err := a.Commit(wc, "rm a", []string{"dir/a.c"}); err != nil {
		t.Fatal(err)
	}
	if st := status(t, a, wc); len(st) != 1 {
		t.Errorf("status = %v", st)
	}
}

func TestCommitRefuses(t *testing.T) {
	a, wc := testApp(t)
	if _, err := a.Commit(wc, "  ", []string{"x"}); err == nil {
		t.Error("empty message accepted")
	}
	if _, err := a.Commit(wc, "m", nil); err == nil {
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
	if _, err := a.Commit(wc, "f", []string{"f.c"}); err != nil {
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
	if _, err := a.Commit(wc, "a", []string{"src"}); err != nil {
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
