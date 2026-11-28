package main

// File manager menus: one list of actions (menuActions), written into each
// file manager's own format by `trunkline integrate`. Every item only runs
// `trunkline <action> <paths>`; the window does the rest (cli.go).

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// integration is one file manager's menu: a per-user file.
type integration struct {
	name    string
	present func() bool               // is the file manager installed?
	path    func() string             // where its menu file goes
	content func([]string) []byte     // the file, running this command
	note    func(removed bool) string // what else to do, if anything
}

var integrations = []integration{
	{
		name:    "Nautilus (GNOME Files)",
		present: func() bool { _, err := exec.LookPath("nautilus"); return err == nil },
		path:    func() string { return filepath.Join(dataHome(), "nautilus-python", "extensions", "trunkline.py") },
		content: nautilusExtension,
		note: func(removed bool) string {
			s := "Restart Nautilus to see the change: nautilus -q"
			if !removed && !nautilusPython() {
				s = "The menu needs Nautilus's Python support: sudo apt install python3-nautilus\n" +
					"(Fedora: sudo dnf install nautilus-python), then: nautilus -q"
			}
			return s
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

// nautilusPython tells whether nautilus-python, which loads Python extensions, is installed.
func nautilusPython() bool {
	for _, g := range []string{"/usr/lib/*/nautilus/extensions-4/libnautilus-python.so", "/usr/lib*/nautilus/extensions-4/libnautilus-python.so"} {
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

// nautilusExtension is the Python extension for Nautilus (and, later, Nemo and Caja).
func nautilusExtension(command []string) []byte {
	cmd, _ := json.Marshal(command)
	acts, _ := json.Marshal(menuActions)
	s := strings.Replace(nautilusTemplate, "__COMMAND__", string(cmd), 1)
	return []byte(strings.Replace(s, "__ACTIONS__", string(acts), 1))
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
	for _, in := range integrations {
		p := in.path()
		if !remove && !in.present() {
			continue
		}
		found = true
		done, err := integrateOne(in, p, remove)
		switch {
		case err != nil:
			fmt.Fprintf(out, "%s: %v\n", in.name, err)
			code = 1
		case done && remove:
			fmt.Fprintf(out, "%s: removed %s\n  %s\n", in.name, p, indent(in.note(true)))
		case done:
			fmt.Fprintf(out, "%s: wrote %s\n  %s\n", in.name, p, indent(in.note(false)))
		case remove:
			found = false
		}
	}
	if !found {
		if remove {
			fmt.Fprintln(out, "No TrunkLine menus to remove.")
		} else {
			fmt.Fprintln(out, "No file manager TrunkLine can add a menu to was found. Supported so far: Nautilus.")
		}
	}
	return code
}

func indent(s string) string { return strings.ReplaceAll(s, "\n", "\n  ") }

// integrateOne writes or removes one menu file. done is false when there was
// nothing to remove.
func integrateOne(in integration, path string, remove bool) (done bool, err error) {
	old, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
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
	return true, os.WriteFile(path, in.content(menuCommand()), 0o644)
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
