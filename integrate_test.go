package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMenuActions(t *testing.T) {
	if err := menuActionsValid(); err != nil {
		t.Fatal(err)
	}
}

// A stand-in for gi and Nautilus: enough of the API to build menus and record
// what an item runs, so the extension's logic runs without Nautilus.
const fakeGI = `
import sys, types
gi = types.ModuleType("gi")
gi.require_version = lambda ns, v: None
repo = types.ModuleType("gi.repository")
class GObject: pass
class MenuItem:
    def __init__(self, name, label, icon=None):
        self.name, self.label, self.icon, self.submenu, self.handler = name, label, icon, None, None
    def connect(self, signal, fn): self.handler = fn
    def set_submenu(self, m): self.submenu = m
class Menu:
    def __init__(self): self.items = []
    def append_item(self, i): self.items.append(i)
ran = []
class SpawnFlags: SEARCH_PATH = 1
repo.GObject = types.SimpleNamespace(GObject=GObject)
for fm in ("Nautilus", "Nemo", "Caja"):
    api = types.SimpleNamespace(MenuProvider=type("MenuProvider", (), {}), MenuItem=MenuItem, Menu=Menu)
    if fm == "Nemo":
        api.NameAndDescProvider = type("NameAndDescProvider", (), {})
    setattr(repo, fm, api)
    sys.modules["gi.repository." + fm] = api
repo.GLib = types.SimpleNamespace(spawn_async=lambda argv, flags=0: ran.append(argv), SpawnFlags=SpawnFlags)
gi.repository = repo
sys.modules.update({"gi": gi, "gi.repository": repo})
`

// The menus the extension builds for some cases, and what its items run.
const exerciseMenu = `
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("trunkline_ext", sys.argv[1])
ext = importlib.util.module_from_spec(spec); spec.loader.exec_module(ext)
class Loc:
    def __init__(self, p): self.p = p
    def get_path(self): return self.p
class F:
    def __init__(self, p, d): self.p, self.d = p, d
    def get_location(self): return Loc(self.p)
    def is_directory(self): return self.d
def labels(items):
    return [i.label for i in items[0].submenu.items] if items else []
wc, plain = sys.argv[2], sys.argv[3]
m = ext.TrunkLineMenu()
out = {
    "file": labels(m.get_file_items([F(wc + "/src/main.c", False)])),
    "old_api": labels(m.get_file_items("window", [F(wc + "/src/main.c", False)])),
    "folder": labels(m.get_file_items([F(wc + "/src", True)])),
    "background": labels(m.get_background_items(F(wc + "/src", True))),
    "plain_folder": labels(m.get_background_items(F(plain, True))),
    "plain_file": labels(m.get_file_items([F(plain + "/a.txt", False)])),
    "mixed": labels(m.get_file_items([F(wc + "/src/main.c", False), F(plain + "/a.txt", False)])),
    "remote": labels(m.get_file_items([type("R", (), {"get_location": lambda s: Loc(None), "is_directory": lambda s: False})()])),
}
items = m.get_file_items([F(wc + "/src/main.c", False), F(wc + "/-odd.c", False)])
next(i for i in items[0].submenu.items if i.label == "Commit…").handler(None)
out["ran"] = __import__("fakegi").ran
out["named"] = hasattr(m, "get_name_and_desc") and any(b.__name__ == "NameAndDescProvider" for b in type(m).__mro__)
print(json.dumps(out))
`

// The same extension serves Nautilus, Nemo and Caja.
func TestPythonExtension(t *testing.T) {
	for _, fm := range []string{"Nautilus", "Nemo", "Caja"} {
		t.Run(fm, func(t *testing.T) { testPythonExtension(t, fm) })
	}
}

func testPythonExtension(t *testing.T, fm string) {
	py, err := exec.LookPath("python3")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs python3 (Linux)")
	}
	dir := t.TempDir()
	wc, plain := filepath.Join(dir, "wc"), filepath.Join(dir, "plain")
	for _, d := range []string{filepath.Join(wc, ".svn"), filepath.Join(wc, "src"), plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ext := filepath.Join(dir, "trunkline.py")
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("trunkline.py", string(nautilusExtension(fm, []string{"/opt/trunk line/trunkline"})))
	write("fakegi.py", fakeGI)
	write("run.py", "import fakegi\n"+exerciseMenu)
	out, err := exec.Command(py, filepath.Join(dir, "run.py"), ext, wc, plain).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	list := func(k string) []string {
		var s []string
		for _, v := range got[k].([]any) {
			s = append(s, v.(string))
		}
		return s
	}
	for k, want := range map[string][]string{
		"file":         {"Commit…", "Show log", "Diff", "Blame", "Add", "Revert…", "Lock…", "Release lock", "Open in TrunkLine"},
		"folder":       {"Commit…", "Update", "Show log", "Add", "Revert…", "Open in TrunkLine"},
		"background":   {"Commit…", "Update", "Show log", "Revert…", "Open in TrunkLine"},
		"plain_folder": {"Check out or import…"},
		"plain_file":   nil,
		"mixed":        nil,
		"remote":       nil,
	} {
		if g := list(k); !slices.Equal(g, want) {
			t.Errorf("%s: got %q, want %q", k, g, want)
		}
	}
	if g := list("old_api"); !slices.Equal(g, list("file")) {
		t.Errorf("old API: got %q", g)
	}
	ran, _ := json.Marshal(got["ran"])
	want, _ := json.Marshal([][]string{{"/opt/trunk line/trunkline", "commit", "--", wc + "/src/main.c", wc + "/-odd.c"}})
	if string(ran) != string(want) {
		t.Errorf("ran %s, want %s", ran, want)
	}
	if named, _ := got["named"].(bool); named != (fm == "Nemo") {
		t.Errorf("listed by name in the plugin settings: %v", named)
	}
}

func TestIntegrate(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	saved := integrations
	t.Cleanup(func() { integrations = saved })
	integrations = []integration{saved[0]}
	integrations[0].present = func() bool { return true }
	p := integrations[0].files(nil)[0].path

	var out strings.Builder
	if code := runIntegrate(nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), writtenBy) || strings.Contains(string(b), "__ACTIONS__") {
		t.Fatalf("wrote %q (%v)", b, err)
	}
	// Run again: replaces its own file.
	if code := runIntegrate(nil, &out); code != 0 {
		t.Fatalf("again: exit %d: %s", code, out.String())
	}
	out.Reset()
	if code := runIntegrate([]string{"--remove"}, &out); code != 0 || !strings.Contains(out.String(), "removed") {
		t.Fatalf("remove: exit %d: %s", code, out.String())
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	out.Reset()
	runIntegrate([]string{"--remove"}, &out)
	if !strings.Contains(out.String(), "No TrunkLine menus to remove") {
		t.Errorf("nothing to remove: %s", out.String())
	}
	// Someone else's trunkline.py stays.
	if err := os.WriteFile(p, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runIntegrate(nil, &out); code == 0 {
		t.Error("overwrote a file it didn't write")
	}
	if b, _ := os.ReadFile(p); string(b) != "# mine\n" {
		t.Errorf("changed to %q", b)
	}
}

func TestDolphinMenus(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	files := dolphinMenus([]string{"/opt/trunk line/trunkline"})
	want := map[string][2]string{ // file: MimeType, Actions
		"trunkline-files.desktop":   {"all/allfiles;", "commit;log;diff;blame;add;revert;lock;unlock;open;"},
		"trunkline-folders.desktop": {"inode/directory;", "commit;update;log;add;revert;open;"},
	}
	for _, f := range files {
		w, ok := want[filepath.Base(f.path)]
		if !ok || filepath.Base(filepath.Dir(f.path)) != "servicemenus" {
			t.Errorf("unexpected file %s", f.path)
			continue
		}
		s := string(f.content)
		for _, line := range []string{"Type=Service", "MimeType=" + w[0], "Actions=" + w[1], "X-KDE-Submenu=TrunkLine",
			"Name=Commit…", `Exec="/opt/trunk line/trunkline" commit -- %F`} {
			if !strings.Contains(s, "\n"+line+"\n") {
				t.Errorf("%s: no line %q in\n%s", f.path, line, s)
			}
		}
		if f.mode&0o111 == 0 {
			t.Errorf("%s: not executable (KF6 skips it)", f.path)
		}
	}
}

func TestExecArg(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/trunkline": "/usr/bin/trunkline",
		"/a b/trunkline":     `"/a b/trunkline"`,
		"/opt/$x":            `"/opt/\\$x"`,
		`/a\b`:               `"/a\\\\b"`,
		"/50%/t":             "/50%%/t",
		"":                   `""`,
	} {
		if got := execArg(in); got != want {
			t.Errorf("execArg(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestIntegrateDolphin(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	saved := integrations
	t.Cleanup(func() { integrations = saved })
	integrations = []integration{byName(t, saved, "Dolphin (KDE)")}
	integrations[0].present = func() bool { return true }
	var out strings.Builder
	if code := runIntegrate(nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, f := range integrations[0].files(nil) {
		fi, err := os.Stat(f.path)
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", f.path, fi, err)
		}
	}
	out.Reset()
	if code := runIntegrate([]string{"--remove"}, &out); code != 0 || strings.Count(out.String(), ".desktop") != 2 {
		t.Errorf("remove: exit %d: %s", code, out.String())
	}
}

func byName(t *testing.T, list []integration, name string) integration {
	for _, in := range list {
		if in.name == name {
			return in
		}
	}
	t.Fatalf("no integration %q", name)
	return integration{}
}

func TestIntegratePaths(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	for name, want := range map[string]string{
		"Nautilus (GNOME Files)": "/data/nautilus-python/extensions/trunkline.py",
		"Nemo (Cinnamon)":        "/data/nemo-python/extensions/trunkline.py",
		"Caja (MATE)":            "/data/caja-python/extensions/trunkline.py",
	} {
		f := byName(t, integrations, name).files([]string{"trunkline"})
		if len(f) != 1 || f[0].path != want {
			t.Errorf("%s: %+v", name, f)
			continue
		}
		fm := strings.Fields(name)[0]
		if !strings.Contains(string(f[0].content), `FILE_MANAGER = "`+fm+`"`) {
			t.Errorf("%s: not made for %s", name, fm)
		}
	}
}
