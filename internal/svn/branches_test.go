package svn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLayout(t *testing.T) {
	for _, c := range []struct {
		url  string
		want Layout
	}{
		{"https://svn.example.com/repo/trunk", Layout{"https://svn.example.com/repo", KindTrunk, "trunk", ""}},
		{"https://svn.example.com/repo/trunk/", Layout{"https://svn.example.com/repo", KindTrunk, "trunk", ""}},
		{"svn://localhost:3690/sandbox/trunk/src/lib", Layout{"svn://localhost:3690/sandbox", KindTrunk, "trunk", "/src/lib"}},
		{"http://svn/repo/branches/imu", Layout{"http://svn/repo", KindBranch, "imu", ""}},
		{"http://svn/repo/branches/imu/src", Layout{"http://svn/repo", KindBranch, "imu", "/src"}},
		{"http://svn/repo/tags/v1.0", Layout{"http://svn/repo", KindTag, "v1.0", ""}},
		// Several projects in one repository.
		{"http://svn/repos/firmware/trunk", Layout{"http://svn/repos/firmware", KindTrunk, "trunk", ""}},
		{"http://svn/repos/tools/cli/branches/fix-x/doc", Layout{"http://svn/repos/tools/cli", KindBranch, "fix-x", "/doc"}},
		// The repository root itself is the trunk folder's parent.
		{"file:///home/me/repo/trunk", Layout{"file:///home/me/repo", KindTrunk, "trunk", ""}},
		{"file:///C:/svn/repo/branches/b1", Layout{"file:///C:/svn/repo", KindBranch, "b1", ""}},
		// Encoded names stay encoded in URLs, decoded in Name.
		{"http://svn/My%20Project/branches/new%20ui/src%20code", Layout{"http://svn/My%20Project", KindBranch, "new ui", "/src%20code"}},
		// Not in the layout.
		{"http://svn/repo", Layout{}},
		{"http://svn/repo/projects/thing", Layout{}},
		{"http://svn/repo/branches", Layout{}}, // the branches folder itself, no branch
	} {
		if got := ParseLayout(c.url); got != c.want {
			t.Errorf("ParseLayout(%q)\n got  %+v\n want %+v", c.url, got, c.want)
		}
	}

	l := ParseLayout("http://svn/repo/trunk/src")
	for _, c := range []struct{ kind, name, want string }{
		{KindTrunk, "", "http://svn/repo/trunk/src"},
		{KindBranch, "imu", "http://svn/repo/branches/imu/src"},
		{KindTag, "v1.0", "http://svn/repo/tags/v1.0/src"},
		{KindBranch, "new ui", "http://svn/repo/branches/new%20ui/src"},
	} {
		if got := l.URLOf(c.kind, c.name); got != c.want {
			t.Errorf("URLOf(%s, %s) = %s, want %s", c.kind, c.name, got, c.want)
		}
	}
}

func TestValidBranchName(t *testing.T) {
	for name, ok := range map[string]bool{
		"imu": true, "feature-x": true, "v1.0": true, "release_2026.11": true, "B2": true,
		"": false, "-x": false, ".hidden": false, "a/b": false, `a\b`: false, "a b": false,
		"..": false, "a..b": false, "end.": false, "x:y": false, strings.Repeat("a", 101): false,
	} {
		if got := ValidBranchName(name); got != ok {
			t.Errorf("ValidBranchName(%q) = %v", name, got)
		}
	}
}

func TestBranchAndSwitch(t *testing.T) {
	c, root, wc := newRepo(t)
	ctx := context.Background()
	write(t, filepath.Join(wc, "src", "main.c"), "trunk v1\n")
	write(t, filepath.Join(wc, "README"), "readme\n")
	if err := c.Add(ctx, Opts{}, filepath.Join(wc, "src"), filepath.Join(wc, "README")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, Opts{}, "add", []string{filepath.Join(wc, "src"), filepath.Join(wc, "src", "main.c"), filepath.Join(wc, "README")}); err != nil {
		t.Fatal(err) // r2
	}

	// Branch from trunk at r2, then trunk moves on (r4).
	rev, err := c.Copy(ctx, Opts{}, root+"/trunk@2", root+"/branches/imu", "Branch imu")
	if err != nil || rev != 3 {
		t.Fatalf("copy: r%d, %v", rev, err)
	}
	write(t, filepath.Join(wc, "src", "main.c"), "trunk v2\n")
	if _, err := c.Commit(ctx, Opts{}, "trunk v2", []string{filepath.Join(wc, "src", "main.c")}); err != nil {
		t.Fatal(err)
	}
	// A tag in a folder that doesn't exist yet (--parents).
	if _, err := c.Copy(ctx, Opts{}, root+"/trunk", root+"/tags/releases/v1", "tag"); err != nil {
		t.Fatal(err)
	}

	if ok, err := c.Exists(ctx, Opts{}, root+"/branches/imu"); !ok || err != nil {
		t.Errorf("Exists(branch) = %v, %v", ok, err)
	}
	if ok, err := c.Exists(ctx, Opts{}, root+"/branches/nope"); ok || err != nil {
		t.Errorf("Exists(missing) = %v, %v", ok, err)
	}

	// A local change comes along when switching to the branch.
	write(t, filepath.Join(wc, "README"), "readme\nlocal edit\n")
	var lines []string
	if err := c.Switch(ctx, Opts{}, wc, root+"/branches/imu", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(ctx, Opts{}, wc)
	if err != nil {
		t.Fatal(err)
	}
	if info.RelativeURL != "^/branches/imu" {
		t.Errorf("after switch: %s", info.RelativeURL)
	}
	if b, _ := os.ReadFile(filepath.Join(wc, "src", "main.c")); string(b) != "trunk v1\n" {
		t.Errorf("main.c on the branch = %q (the branch was made before trunk v2)", b)
	}
	if b, _ := os.ReadFile(filepath.Join(wc, "README")); string(b) != "readme\nlocal edit\n" {
		t.Errorf("the local edit didn't come along: %q", b)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "revision") {
		t.Errorf("switch output = %q", lines)
	}

	// Commit on the branch, switch back: trunk's content, branch commit not there.
	write(t, filepath.Join(wc, "src", "main.c"), "imu work\n")
	if _, err := c.Commit(ctx, Opts{}, "imu work", []string{filepath.Join(wc, "src", "main.c")}); err != nil {
		t.Fatal(err)
	}
	if err := c.Revert(ctx, Opts{}, filepath.Join(wc, "README")); err != nil {
		t.Fatal(err)
	}
	if err := c.Switch(ctx, Opts{}, wc, root+"/trunk", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(wc, "src", "main.c")); string(b) != "trunk v2\n" {
		t.Errorf("back on trunk main.c = %q", b)
	}

	// A subfolder checkout switches to the same subfolder of the branch.
	sub := filepath.Join(t.TempDir(), "sub")
	if err := c.Checkout(ctx, Opts{}, root+"/trunk/src", sub, nil); err != nil {
		t.Fatal(err)
	}
	si, _ := c.Info(ctx, Opts{}, sub)
	target := ParseLayout(si.URL).URLOf(KindBranch, "imu")
	if err := c.Switch(ctx, Opts{}, sub, target, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(sub, "main.c")); string(b) != "imu work\n" {
		t.Errorf("subfolder on the branch: main.c = %q", b)
	}

	// Delete the branch: gone from HEAD, still in history.
	if _, err := c.DeleteURL(ctx, Opts{}, root+"/branches/imu", "done with imu"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Exists(ctx, Opts{}, root+"/branches/imu"); ok {
		t.Error("deleted branch still exists")
	}
	if ok, _ := c.Exists(ctx, Opts{}, root+"/branches/imu@3"); !ok {
		t.Error("deleted branch should still be in history at r3")
	}
}

func TestFileURL(t *testing.T) {
	for _, c := range []struct{ path, url string }{
		{"/home/me/repo", "file:///home/me/repo"},
		{"/tmp/my repo", "file:///tmp/my%20repo"},
		{"C:/Users/me/repo", "file:///C:/Users/me/repo"},
	} {
		if got := FileURL(c.path); got != c.url {
			t.Errorf("FileURL(%q) = %q, want %q", c.path, got, c.url)
		}
		if back := filepath.ToSlash(FilePath(c.url)); back != c.path {
			t.Errorf("FilePath(%q) = %q, want %q", c.url, back, c.path)
		}
	}
}
