package main

// File manager menus: one list of actions (menuActions), written into each
// file manager's own format by `trunkline integrate`. Every item only runs
// `trunkline <action> <paths>`; the window does the rest (cli.go).

import (
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"trunkline/internal/update"
)

// menuAction is one item of the TrunkLine menu in a file manager.
type menuAction struct {
	Action string   `json:"action"` // the command line's action (cliActions)
	Label  string   `json:"label"`
	Icon   string   `json:"icon"` // a freedesktop icon name, for menus that show icons
	On     []string `json:"on"`   // what it's for: "file", "folder", "background" (inside an open folder)
	WC     bool     `json:"wc"`   // shown inside a working copy (true) or outside one (false)
}

var (
	onAll     = []string{"file", "folder", "background"}
	onFolders = []string{"folder", "background"}
	onFiles   = []string{"file"}
)

// menuActions is the menu, in order. Which items show can only depend on
// cheap facts (file or folder, in a working copy or not): menus are built on
// every right-click, so they never run svn.
var menuActions = []menuAction{
	{"commit", "Commit…", "document-send", onAll, true},
	{"update", "Update", "view-refresh", onFolders, true},
	{"log", "Show log", "document-open-recent", onAll, true},
	{"diff", "Diff", "edit-find-replace", onFiles, true},
	{"blame", "Blame", "format-justify-left", onFiles, true},
	{"add", "Add", "list-add", []string{"file", "folder"}, true},
	{"revert", "Revert…", "edit-undo", onAll, true},
	{"lock", "Lock…", "changes-prevent", onFiles, true},
	{"unlock", "Release lock", "changes-allow", onFiles, true},
	{"open", "Open in TrunkLine", "trunkline", onAll, true},
	// Outside a working copy, open offers a checkout into the folder or an import of it.
	{"open", "Check out or import…", "trunkline", onFolders, false},
}

//go:embed integrate/nautilus.py
var nautilusTemplate string

// writtenBy marks the files integrate writes: it only replaces or removes a
// file that has it, never one of the user's own.
const writtenBy = "Written by `trunkline integrate`"

// integration is one file manager's menu: per-user files.
type integration struct {
	name    string
	present func() bool                       // is the file manager installed?
	files   func(command []string) []menuFile // its menu files, running this command
	note    func(removed bool) string         // what else to do, if anything
}

// menuFile is one file a menu needs: ours alone (content), or one shared
// with the user, where merge adds our part (add) or takes it out (!add) and
// leaves the rest as it was.
type menuFile struct {
	path    string
	content []byte
	mode    fs.FileMode
	merge   func(old []byte, add bool) ([]byte, error)
	start   func() []byte // a shared file's contents when the user has none yet
}

var integrations = []integration{
	{
		name:    "Nautilus (GNOME Files)",
		present: installed("nautilus"),
		files:   pythonExtension("Nautilus", "nautilus-python"),
		note:    pythonNote("Nautilus", "nautilus", "python3-nautilus", "nautilus-python"),
	},
	{
		name:    "Nemo (Cinnamon)",
		present: installed("nemo"),
		files:   pythonExtension("Nemo", "nemo-python"),
		note:    pythonNote("Nemo", "nemo", "nemo-python", "nemo-python"),
	},
	{
		name:    "Caja (MATE)",
		present: installed("caja"),
		files:   pythonExtension("Caja", "caja-python"),
		note:    pythonNote("Caja", "caja", "python3-caja", "caja-python"),
	},
	{
		name:    "Thunar (XFCE)",
		present: installed("thunar"),
		files:   thunarMenu,
		note: func(bool) string {
			return "Thunar reads it when it starts: thunar -q, then open Thunar again."
		},
	},
	{
		name:    "PCManFM-Qt / PCManFM (LXQt, LXDE)",
		present: installed("pcmanfm-qt", "pcmanfm"),
		files:   fileManagerActions,
		note: func(bool) string {
			return "PCManFM-Qt reads it when it starts: close its windows and open it again."
		},
	},
	{
		name:    "elementary Files",
		present: installed("io.elementary.files"),
		files:   contracts,
		note: func(bool) string {
			return "Files shows them the next time it opens a menu."
		},
	},
	{
		name:    "Dolphin (KDE)",
		present: installed("dolphin", "konqueror"),
		files:   dolphinMenus,
		note: func(bool) string {
			return "Dolphin reads it the next time it opens a menu (or reopen its window)."
		},
	},
}

// dataHome is $XDG_DATA_HOME, or ~/.local/share.
func dataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// installed returns whether any of these programs is on PATH.
func installed(programs ...string) func() bool {
	return func() bool {
		for _, p := range programs {
			if _, err := exec.LookPath(p); err == nil {
				return true
			}
		}
		return false
	}
}

// pythonExtension is the menu as a Python extension of fm (Nautilus, Nemo or
// Caja), in dir (nautilus-python, ...) under the user's data folder.
func pythonExtension(fm, dir string) func([]string) []menuFile {
	return func(command []string) []menuFile {
		p := filepath.Join(dataHome(), dir, "extensions", "trunkline.py")
		return []menuFile{{path: p, content: nautilusExtension(fm, command), mode: 0o644}}
	}
}

// pythonNote says to restart the file manager (program), and how to install
// its Python support (dir: the library's folder, e.g. nemo-python) if missing.
func pythonNote(fm, program, debPackage, dir string) func(bool) string {
	return func(removed bool) string {
		if !removed && !hasPythonSupport(program, dir) {
			return fmt.Sprintf("The menu needs %s's Python support: sudo apt install %s\n"+
				"(Fedora: sudo dnf install %s), then: %s -q", fm, debPackage, dir, program)
		}
		return fmt.Sprintf("Restart %s to see the change: %s -q", fm, program)
	}
}

// hasPythonSupport tells whether the library that loads a file manager's
// Python extensions (libnautilus-python.so, ...) is installed.
func hasPythonSupport(program, dir string) bool {
	for _, g := range []string{"/usr/lib/*/" + program + "/extensions-*/lib" + dir + ".so", "/usr/lib*/" + program + "/extensions-*/lib" + dir + ".so"} {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			return true
		}
	}
	return false
}

// menuCommand is how a menu starts TrunkLine: this program, or through
// Flatpak or snap when it was installed that way.
func menuCommand() []string {
	switch update.Install {
	case "flatpak":
		return []string{"flatpak", "run", "io.github.FrancoCoetzer.TrunkLine"}
	case "snap":
		return []string{"snap", "run", "trunkline"}
	}
	exe, err := os.Executable()
	if err != nil {
		return []string{"trunkline"}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return []string{exe}
}

// nautilusExtension is the Python extension for fm: Nautilus, Nemo or Caja.
func nautilusExtension(fm string, command []string) []byte {
	cmd, _ := json.Marshal(command)
	acts, _ := json.Marshal(menuActions)
	return []byte(strings.NewReplacer("__FM__", fm, "__COMMAND__", string(cmd), "__ACTIONS__", string(acts)).Replace(nautilusTemplate))
}

// runIntegrate is `trunkline integrate [--remove]`: it writes (or removes) the
// menu for each file manager found, says what it did, and returns the exit code.
func runIntegrate(args []string, out io.Writer) int {
	remove := false
	for _, a := range args {
		switch a {
		case "--remove":
			remove = true
		case "-h", "--help":
			fmt.Fprint(out, "Usage: trunkline integrate [--remove]\n\n"+
				"Adds a TrunkLine menu to the right-click menu of the file managers\n"+
				"installed for you (this user only); --remove takes it out again.\n")
			return 0
		default:
			fmt.Fprintf(out, "trunkline integrate: unknown option %s\n", a)
			return 2
		}
	}
	code, found := 0, false
	removed := false
	for _, in := range integrations {
		if !remove && !in.present() {
			continue
		}
		var done []string
		failed := false
		for _, f := range in.files(menuCommand()) {
			ok, err := integrateOne(f, remove)
			if err != nil {
				fmt.Fprintf(out, "%s: %v\n", in.name, err)
				failed = true
			} else if ok {
				done = append(done, f.path)
			}
		}
		if failed {
			code = 1
		}
		if !remove {
			found = true
		}
		if len(done) == 0 {
			continue
		}
		removed = removed || remove
		verb := "wrote"
		if remove {
			verb = "removed"
		}
		fmt.Fprintf(out, "%s: %s %s\n  %s\n", in.name, verb,
			strings.Join(done, "\n  "+strings.Repeat(" ", len(verb)+1)), indent(in.note(remove)))
	}
	if remove {
		found = removed || code != 0
	}
	if !found {
		if remove {
			fmt.Fprintln(out, "No TrunkLine menus to remove.")
		} else {
			switch runtime.GOOS {
			case "linux":
				fmt.Fprintln(out, "No file manager TrunkLine can add a menu to was found. Supported so far: Nautilus, Nemo, Caja, Thunar, Dolphin, PCManFM-Qt, elementary Files.")
			case "windows":
				fmt.Fprintln(out, "File manager menus are only on Linux so far; a TrunkLine menu in Windows Explorer is planned.")
			default:
				fmt.Fprintln(out, "File manager menus are only on Linux so far.")
			}
		}
	}
	return code
}

func indent(s string) string { return strings.ReplaceAll(s, "\n", "\n  ") }

// integrateOne writes or removes one menu file. done is false when there was
// nothing to remove.
func integrateOne(f menuFile, remove bool) (done bool, err error) {
	path := f.path
	old, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if f.merge != nil {
		return mergeOne(f, old, exists, remove)
	}
	if exists && !strings.Contains(string(old), writtenBy) {
		return false, fmt.Errorf("%s is not one TrunkLine wrote; leaving it alone", path)
	}
	if remove {
		if !exists {
			return false, nil
		}
		return true, os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, f.content, f.mode); err != nil {
		return false, err
	}
	return true, os.Chmod(path, f.mode) // WriteFile only sets it on a new file
}

// mergeOne adds our part to a file shared with the user, or takes it out.
func mergeOne(f menuFile, old []byte, exists, remove bool) (bool, error) {
	if remove && !exists {
		return false, nil
	}
	if !exists && f.start != nil {
		old = f.start()
	}
	next, err := f.merge(old, !remove)
	if err != nil {
		return false, fmt.Errorf("%s: %v; leaving it alone", f.path, err)
	}
	if exists && string(next) == string(old) {
		return !remove, nil // already as it should be
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(f.path, next, f.mode)
}

// configHome is $XDG_CONFIG_HOME, or ~/.config.
func configHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// systemConfig is the first copy of name in $XDG_CONFIG_DIRS (or /etc/xdg).
func systemConfig(name string) []byte {
	dirs := os.Getenv("XDG_CONFIG_DIRS")
	if dirs == "" {
		dirs = "/etc/xdg"
	}
	for _, d := range filepath.SplitList(dirs) {
		if b, err := os.ReadFile(filepath.Join(d, name)); err == nil {
			return b
		}
	}
	return nil
}

// thunarMenu is Thunar's: custom actions in uca.xml, a file the user also
// edits (Edit > Configure custom actions) and Thunar rewrites. Ours are the
// actions whose unique-id starts with "trunkline-"; everything else stays as
// it was. Thunar reads the user's uca.xml instead of the system one, so a new
// file starts as a copy of the system one (its "Open Terminal Here"). Like
// Dolphin's, the actions can't check for a working copy.
func thunarMenu(command []string) []menuFile {
	return []menuFile{{
		path:  filepath.Join(configHome(), "Thunar", "uca.xml"),
		mode:  0o644,
		start: func() []byte { return systemConfig(filepath.Join("Thunar", "uca.xml")) },
		merge: func(old []byte, add bool) ([]byte, error) {
			ours := ""
			if add {
				ours = thunarActions(command)
			}
			return mergeUCA(old, ours)
		},
	}}
}

const thunarID = "<unique-id>trunkline-"

// thunarActions are our uca.xml <action> elements, in Thunar's own layout.
func thunarActions(command []string) string {
	var cmd []string
	for _, c := range command {
		cmd = append(cmd, shellQuote(c))
	}
	// Only what XML text needs, so the file stays readable when edited by hand.
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	help := map[string]string{}
	for _, c := range cliActions {
		help[c.name] = c.help
	}
	var b strings.Builder
	for _, a := range menuActions {
		if !a.WC {
			continue // an action can't check; outside a working copy "open" offers checkout or import
		}
		fmt.Fprintf(&b, "\t<action>\n\t\t<icon>%s</icon>\n\t\t<name>%s</name>\n\t\t<submenu>TrunkLine</submenu>\n"+
			"\t\t<unique-id>trunkline-%s</unique-id>\n\t\t<command>%s</command>\n\t\t<description>TrunkLine: %s</description>\n"+
			"\t\t<range></range>\n\t\t<patterns>*</patterns>\n",
			esc(a.Icon), esc(a.Label), a.Action, esc(strings.Join(cmd, " ")+" "+a.Action+" -- %F"), esc(help[a.Action]))
		if slices.Contains(a.On, "folder") || slices.Contains(a.On, "background") {
			b.WriteString("\t\t<directories/>\n")
		}
		if slices.Contains(a.On, "file") {
			b.WriteString("\t\t<audio-files/>\n\t\t<image-files/>\n\t\t<other-files/>\n\t\t<text-files/>\n\t\t<video-files/>\n")
		}
		b.WriteString("\t</action>\n")
	}
	return b.String()
}

// mergeUCA takes our actions out of a uca.xml and, if ours isn't empty, adds
// them at the end. Everything else is kept byte for byte.
func mergeUCA(old []byte, ours string) ([]byte, error) {
	s := string(old)
	if strings.TrimSpace(s) == "" {
		s = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<actions>\n</actions>\n"
	}
	s = strings.Replace(s, "<actions/>", "<actions>\n</actions>", 1)
	// Take ours out: each <action>...</action> with our unique-id, with the
	// indent before it and the line end after it.
	var out strings.Builder
	for {
		i := strings.Index(s, "<action>")
		if i < 0 {
			out.WriteString(s)
			break
		}
		j := strings.Index(s[i:], "</action>")
		if j < 0 {
			return nil, errors.New("an <action> is not closed")
		}
		end := i + j + len("</action>")
		if !strings.Contains(s[i:end], thunarID) {
			out.WriteString(s[:end])
			s = s[end:]
			continue
		}
		start := i
		for start > 0 && (s[start-1] == ' ' || s[start-1] == '\t') {
			start--
		}
		if strings.HasPrefix(s[end:], "\n") {
			end++
		}
		out.WriteString(s[:start])
		s = s[end:]
	}
	s = out.String()
	if ours != "" {
		k := strings.LastIndex(s, "</actions>")
		if k < 0 {
			return nil, errors.New("no <actions> element: not a Thunar custom actions file")
		}
		s = s[:k] + ours + s[k:]
	}
	// Still well-formed XML?
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("would not be valid XML (%v)", err)
		}
	}
	return []byte(s), nil
}

// shellQuote quotes one argument the way g_shell_parse_argv (which Thunar
// runs a command through) reads it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`*?[]#~=%&;|<>(){}!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// keyfileExec is an Exec value run through g_shell_parse_argv after the
// file manager's own field codes (%F), as libfm-qt does: shell quoting, % doubled,
// and the key file's own \\ escape.
func keyfileExec(command []string, action string) string {
	var cmd []string
	for _, c := range command {
		cmd = append(cmd, strings.ReplaceAll(strings.ReplaceAll(shellQuote(c), "%", "%%"), `\`, `\\`))
	}
	return strings.Join(cmd, " ") + " " + action + " -- %F"
}

// fileManagerActions are file-manager actions (the freedesktop DES-EMA spec)
// for PCManFM-Qt and PCManFM: one .desktop file per action, and a menu file
// that puts them in a TrunkLine submenu. TargetLocation puts the folder ones
// on the empty space inside a folder too. Like Dolphin's, they can't check for
// a working copy.
func fileManagerActions(command []string) []menuFile {
	dir := filepath.Join(dataHome(), "file-manager", "actions")
	var files []menuFile
	var ids []string
	for _, a := range menuActions {
		if !a.WC {
			continue
		}
		id := "trunkline-" + a.Action
		ids = append(ids, id)
		forFiles := slices.Contains(a.On, "file")
		forFolders := slices.Contains(a.On, "folder") || slices.Contains(a.On, "background")
		mime := "all/all;"
		switch {
		case !forFolders:
			mime = "all/allfiles;"
		case !forFiles:
			mime = "inode/directory;"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s from TrunkLine's list of actions; rewritten on the next run.\n"+
			"[Desktop Entry]\nType=Action\nName=%s\nIcon=%s\nTooltip=TrunkLine: %s\nProfiles=main;\n"+
			"TargetContext=true\nTargetLocation=%t\n\n[X-Action-Profile main]\nMimeTypes=%s\nSchemes=file;\nExec=%s\n",
			writtenBy, a.Label, a.Icon, a.Label, forFolders, mime, keyfileExec(command, a.Action))
		files = append(files, menuFile{path: filepath.Join(dir, id+".desktop"), content: []byte(b.String()), mode: 0o644})
	}
	menu := fmt.Sprintf("# %s from TrunkLine's list of actions; rewritten on the next run.\n"+
		"[Desktop Entry]\nType=Menu\nName=TrunkLine\nIcon=trunkline\nItemsList=%s;\nTargetContext=true\nTargetLocation=true\n",
		writtenBy, strings.Join(ids, ";"))
	return append(files, menuFile{path: filepath.Join(dir, "trunkline.desktop"), content: []byte(menu), mode: 0o644})
}

// contractActions are the ones elementary Files gets: it shows contracts as
// plain items, not in a submenu, so only the everyday ones, to keep its menu short.
var contractActions = []string{"commit", "update", "log", "open"}

// contracts are Contractor's .contract files, elementary Files' menu items.
// (GLib, which launches them, checks the program exists before it reads %%
// as %, so an install path with a % in it won't work here.)
// Contractor matches MimeType by prefix, "!" meaning "not": inode/directory
// is a folder, !inode anything else. An action for both gets one of each.
func contracts(command []string) []menuFile {
	dir := filepath.Join(dataHome(), "contractor")
	var cmd []string
	for _, c := range command {
		cmd = append(cmd, execArg(c))
	}
	var files []menuFile
	for _, a := range menuActions {
		if !a.WC || !slices.Contains(contractActions, a.Action) {
			continue
		}
		kinds := map[string]string{}
		if slices.Contains(a.On, "file") {
			kinds["files"] = "!inode;"
		}
		if slices.Contains(a.On, "folder") {
			kinds["folders"] = "inode/directory;"
		}
		for _, kind := range []string{"files", "folders"} {
			mime, ok := kinds[kind]
			if !ok {
				continue
			}
			name := "TrunkLine: " + a.Label
			if a.Action == "open" {
				name = a.Label // "Open in TrunkLine" says it already
			}
			content := fmt.Sprintf("# %s from TrunkLine's list of actions; rewritten on the next run.\n"+
				"[Contractor Entry]\nName=%s\nIcon=%s\nDescription=%s\nMimeType=%s\nExec=%s %s -- %%F\n",
				writtenBy, name, a.Icon, name, mime, strings.Join(cmd, " "), a.Action)
			files = append(files, menuFile{path: filepath.Join(dir, "trunkline-"+a.Action+"-"+kind+".contract"), content: []byte(content), mode: 0o644})
		}
	}
	return files
}

// dolphinMenus are Dolphin's service menus (KIO, KDE Frameworks 5.85 and
// later; KF6 only loads a user's own if it is executable). A service menu
// can't tell whether a path is in a working copy, so it lists the
// working-copy actions everywhere; outside one, TrunkLine offers a checkout
// or an import instead. One file for files, one for folders (which Dolphin
// also uses for the empty space inside a folder).
func dolphinMenus(command []string) []menuFile {
	dir := filepath.Join(dataHome(), "kio", "servicemenus")
	return []menuFile{
		{path: filepath.Join(dir, "trunkline-files.desktop"), content: dolphinMenu(command, "file", "all/allfiles;"), mode: 0o755},
		{path: filepath.Join(dir, "trunkline-folders.desktop"), content: dolphinMenu(command, "folder", "inode/directory;"), mode: 0o755},
	}
}

func dolphinMenu(command []string, on, mime string) []byte {
	var acts []menuAction
	var ids []string
	for _, a := range menuActions {
		if a.WC && slices.Contains(a.On, on) {
			acts = append(acts, a)
			ids = append(ids, a.Action)
		}
	}
	var cmd []string
	for _, c := range command {
		cmd = append(cmd, execArg(c))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# TrunkLine's menu in Dolphin (KDE): right-click > TrunkLine.\n"+
		"# %s from TrunkLine's list of actions; rewritten on the next run.\n"+
		"[Desktop Entry]\nType=Service\nMimeType=%s\nX-KDE-Protocols=file\n"+
		"X-KDE-Submenu=TrunkLine\nIcon=trunkline\nActions=%s;\n", writtenBy, mime, strings.Join(ids, ";"))
	for _, a := range acts {
		// "--": a path that starts with "-" is still a path
		fmt.Fprintf(&b, "\n[Desktop Action %s]\nName=%s\nIcon=%s\nExec=%s %s -- %%F\n",
			a.Action, a.Label, a.Icon, strings.Join(cmd, " "), a.Action)
	}
	return []byte(b.String())
}

// execArg quotes one argument for a .desktop file's Exec key: the Desktop
// Entry spec's quoting, then its string escapes; % is doubled.
func execArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\><~|&;$*?#()`") {
		return s
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(s)
	return `"` + strings.ReplaceAll(q, `\`, `\\`) + `"`
}

// menuActionsValid checks the list against the command line (for the tests).
func menuActionsValid() error {
	for _, m := range menuActions {
		if !slices.ContainsFunc(cliActions, func(c struct{ name, help string }) bool { return c.name == m.Action }) {
			return fmt.Errorf("menu action %q is not a command line action", m.Action)
		}
		for _, on := range m.On {
			if !slices.Contains(onAll, on) {
				return fmt.Errorf("menu action %q: unknown place %q", m.Action, on)
			}
		}
	}
	return nil
}
