package main

// The command line: `trunkline commit file.c`, `trunkline log .`, and the
// file manager menus that run the same. One window at a time: a second launch
// hands its arguments to the running window (options.SingleInstanceLock) and
// exits, so right-clicking ten files doesn't open ten windows.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

// cliActions are the actions the command line takes, in the order the help
// lists them. The page starts each one (runAction in app.js).
var cliActions = []struct{ name, help string }{
	{"open", "open the working copy (the default: trunkline <path>)"},
	{"commit", "the Changes tab with these files ticked, ready for a message"},
	{"update", "update the working copy"},
	{"log", "the history of the working copy, or of one file or folder"},
	{"diff", "the diff of a changed file"},
	{"blame", "who last changed each line of a file"},
	{"lock", "lock files (asks for a note)"},
	{"unlock", "release your locks on files"},
	{"revert", "undo local changes (asks first)"},
	{"add", "add new files to svn"},
}

// cliRequest is what the arguments ask for, before the paths are looked up.
type cliRequest struct {
	Action string
	Paths  []string // as given: relative to the working directory, or absolute
}

// Action is a request with its working copy found: what the page receives.
type Action struct {
	Action  string   `json:"action"`
	WC      string   `json:"wc,omitempty"`      // the working copy root
	Paths   []string `json:"paths,omitempty"`   // relative to WC, with /; "" is WC itself
	Skipped []string `json:"skipped,omitempty"` // paths in another working copy, or none
	Error   string   `json:"error,omitempty"`
}

var errHelp = errors.New("help")
var errVersion = errors.New("version")

// devFlags are the flags a dev build's Wails reads (make dev passes
// -assetdir and -devserver); they and their values are not ours.
var devFlags = []string{"assetdir", "devserver", "frontenddevserverurl", "loglevel"}

// parseArgs reads the command line (without the program name). No arguments
// is no request: just open the window. errHelp and errVersion ask main to
// print the help or the version and exit.
func parseArgs(args []string) (*cliRequest, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		case a == "-h" || a == "--help" || a == "help" && len(rest) == 0:
			return nil, errHelp
		case a == "-v" || a == "--version" || a == "version" && len(rest) == 0:
			return nil, errVersion
		case strings.HasPrefix(a, "-") && len(a) > 1:
			name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if !slices.Contains(devFlags, name) {
				return nil, fmt.Errorf("unknown option %s", a)
			}
			if !hasValue {
				i++ // its value
			}
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return nil, nil
	}
	r := &cliRequest{Action: "open", Paths: rest}
	if slices.ContainsFunc(cliActions, func(c struct{ name, help string }) bool { return c.name == rest[0] }) {
		r.Action, r.Paths = rest[0], rest[1:]
	}
	if len(r.Paths) == 0 {
		r.Paths = []string{"."}
	}
	return r, nil
}

// cliHelp is what --help prints.
func cliHelp() string {
	var b strings.Builder
	b.WriteString("TrunkLine: a desktop SVN client.\n\n")
	b.WriteString("Usage:\n  trunkline                    open the window\n")
	b.WriteString("  trunkline <path>             open the working copy that holds <path>\n")
	b.WriteString("  trunkline <action> [path...] start an action on files or folders (default: .)\n\nActions:\n")
	for _, c := range cliActions {
		fmt.Fprintf(&b, "  %-8s %s\n", c.name, c.help)
	}
	b.WriteString("\nIf TrunkLine is already running, its window opens the action.\n\n")
	b.WriteString("Options:\n  -h, --help     this help\n  -v, --version  the version\n")
	return b.String()
}

// wcRootOf finds the working copy that holds path: the nearest folder at or
// above it with a .svn folder (svn 1.7 and newer keep one, at the root). It
// needs no svn call, and works for files svn doesn't know yet.
func wcRootOf(path string) string {
	dir := path
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".svn")); err == nil && fi.IsDir() {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
}

// resolve finds the working copy of a request's paths (relative ones from
// cwd) and adds it to the list if it's new. Paths in another working copy
// than the first path's are skipped: one window shows one working copy.
func (a *App) resolve(r *cliRequest, cwd string) *Action {
	act := &Action{Action: r.Action}
	c, err := a.client()
	if err != nil {
		act.Error = err.Error()
		return act
	}
	root := ""
	for _, p := range r.Paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		abs = svn.LongPath(filepath.Clean(abs))
		wr := wcRootOf(abs)
		if root == "" {
			if wr == "" {
				act.Error = abs + " is not in an SVN working copy."
				return act
			}
			root = wr
		}
		if wr != root {
			act.Skipped = append(act.Skipped, p)
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			act.Skipped = append(act.Skipped, p)
			continue
		}
		if rel = filepath.ToSlash(rel); rel == "." {
			rel = ""
		}
		if !slices.Contains(act.Paths, rel) {
			act.Paths = append(act.Paths, rel)
		}
	}
	info, err := c.Info(a.ctx, svn.Opts{}, root)
	if err != nil {
		act.Error = a.wrap(err, "").Error()
		return act
	}
	act.WC = root
	if info.Path != "" {
		act.WC = info.Path
	}
	wc := config.WorkingCopy{Name: filepath.Base(act.WC), Path: act.WC, URL: info.URL, RepoRoot: info.RepoRoot}
	_ = a.cfg.Update(func(c *config.Config) {
		// Keep the name the user gave it.
		for _, w := range c.WorkingCopies {
			if filepath.Clean(w.Path) == filepath.Clean(wc.Path) {
				wc.Name = w.Name
			}
		}
		c.AddWorkingCopy(wc)
	})
	return act
}

// PendingAction is the action the command line asked for at start-up, once
// (nil when there was none). The page calls it when it has loaded.
func (a *App) PendingAction() *Action {
	a.mu.Lock()
	r := a.pending
	a.pending = nil
	a.mu.Unlock()
	if r == nil {
		return nil
	}
	cwd, _ := os.Getwd()
	return a.resolve(r, cwd)
}

// secondInstance takes the arguments of a second launch: it brings the window
// forward and sends the page the action.
func (a *App) secondInstance(d options.SecondInstanceData) {
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
	r, err := parseArgs(d.Args)
	if err != nil || r == nil {
		return
	}
	go a.emit("action", a.resolve(r, d.WorkingDirectory))
}
