package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFileManagerActions(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	files := fileManagerActions([]string{"/opt/trunk line/trunkline"})
	byBase := map[string]string{}
	for _, f := range files {
		if filepath.Base(filepath.Dir(f.path)) != "actions" {
			t.Errorf("not in file-manager/actions: %s", f.path)
		}
		byBase[filepath.Base(f.path)] = string(f.content)
	}
	menu := byBase["trunkline.desktop"]
	for _, line := range []string{"Type=Menu", "Name=TrunkLine", "TargetLocation=true",
		"ItemsList=trunkline-commit;trunkline-update;trunkline-log;trunkline-diff;trunkline-blame;trunkline-add;trunkline-revert;trunkline-lock;trunkline-unlock;trunkline-open;"} {
		if !strings.Contains(menu, "\n"+line+"\n") {
			t.Errorf("menu: no line %q in\n%s", line, menu)
		}
	}
	for file, lines := range map[string][]string{
		"trunkline-commit.desktop": {"Type=Action", "Name=Commit…", "TargetLocation=true", "MimeTypes=all/all;",
			"Exec='/opt/trunk line/trunkline' commit -- %F"},
		"trunkline-update.desktop": {"MimeTypes=inode/directory;", "TargetLocation=true"},
		"trunkline-blame.desktop":  {"MimeTypes=all/allfiles;", "TargetLocation=false"},
	} {
		s, ok := byBase[file]
		if !ok {
			t.Errorf("no %s", file)
			continue
		}
		for _, line := range lines {
			if !strings.Contains(s, "\n"+line+"\n") {
				t.Errorf("%s: no line %q in\n%s", file, line, s)
			}
		}
	}
	if len(files) != 11 {
		t.Errorf("%d files, want 10 actions and the menu", len(files))
	}
}

func TestKeyfileExec(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/trunkline": "/usr/bin/trunkline log -- %F",
		"/a b/trunkline":     "'/a b/trunkline' log -- %F",
		"/50%/trunkline":     "'/50%%/trunkline' log -- %F",
		`/a\b`:               `'/a\\b' log -- %F`,
	} {
		if got := keyfileExec([]string{in}, "log"); got != want {
			t.Errorf("keyfileExec(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestContracts(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	files := contracts([]string{"/opt/trunk line/trunkline"})
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f.path))
		if filepath.Base(filepath.Dir(f.path)) != "contractor" {
			t.Errorf("not in contractor: %s", f.path)
		}
		s := string(f.content)
		mime := "MimeType=!inode;"
		if strings.HasSuffix(f.path, "-folders.contract") {
			mime = "MimeType=inode/directory;"
		}
		for _, line := range []string{"[Contractor Entry]", mime, `Exec="/opt/trunk line/trunkline" `} {
			if !strings.Contains(s, line) {
				t.Errorf("%s: no %q in\n%s", f.path, line, s)
			}
		}
	}
	want := []string{"trunkline-commit-files.contract", "trunkline-commit-folders.contract", "trunkline-update-folders.contract",
		"trunkline-log-files.contract", "trunkline-log-folders.contract", "trunkline-open-files.contract", "trunkline-open-folders.contract"}
	if !slices.Equal(names, want) {
		t.Errorf("files %q, want %q", names, want)
	}
}

func TestIntegrateRemovesEverything(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
	saved := integrations
	t.Cleanup(func() { integrations = saved })
	integrations = nil
	for _, in := range saved {
		in.present = func() bool { return true }
		integrations = append(integrations, in)
	}
	var out strings.Builder
	if code := runIntegrate(nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if code := runIntegrate([]string{"--remove"}, &out); code != 0 {
		t.Fatalf("remove: exit %d: %s", code, out.String())
	}
	// Nothing of ours is left in the data folder (Thunar's uca.xml is the
	// user's and stays, without our actions).
	_ = filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("left behind: %s", p)
		}
		return nil
	})
}
