package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

// Version is stamped by the Makefile (-X main.Version=...).
var Version = ""

// Events the frontend listens for.
const evOpLine = "op:line" // string: a line of checkout/update output
const evOpDone = "op:done" // nothing: the long operation finished

// App holds the state behind the methods bound to the frontend
// (window.go.main.App.<Method>, each returning a Promise).
type App struct {
	ctx    context.Context
	cfg    *config.Config
	svn    *svn.Client // nil when svn is not installed
	svnErr error

	mu       sync.Mutex
	creds    map[string]svn.Creds // host -> login, for this session only
	opCancel context.CancelFunc   // the running checkout/update, if any
}

func NewApp() (*App, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("reading the settings in %s: %w", dir, err)
	}
	a := &App{cfg: cfg, creds: map[string]svn.Creds{}}
	a.svn, a.svnErr = svn.Find()
	return a, nil
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// appError is what a failed call rejects with in the frontend: JSON the page
// parses, so it can ask for a login or a certificate decision and retry.
type appError struct {
	Kind    string `json:"kind"` // auth, cert, conflict, locked, canceled, other
	Host    string `json:"host,omitempty"`
	Message string `json:"message"`
}

func (e *appError) Error() string {
	b, _ := json.Marshal(e)
	return string(b)
}

func (a *App) wrap(err error, host string) error {
	if err == nil {
		return nil
	}
	kinds := map[svn.Kind]string{
		svn.KindAuth: "auth", svn.KindCert: "cert", svn.KindConflict: "conflict",
		svn.KindLocked: "locked", svn.KindCanceled: "canceled",
	}
	kind, ok := kinds[svn.KindOf(err)]
	if !ok {
		kind = "other"
	}
	msg := err.Error()
	if kind == "canceled" {
		msg = "Canceled"
	}
	return &appError{Kind: kind, Host: host, Message: msg}
}

// hostOf is the server part of a repository URL ("" for file://).
func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Host
}

// opts are the settings for a call to the server at host.
func (a *App) opts(host string) svn.Opts {
	a.mu.Lock()
	o := svn.Opts{Creds: a.creds[host]}
	a.mu.Unlock()
	a.cfg.Read(func(c *config.Config) { o.TrustCert = c.Trusted(host) })
	return o
}

// wcHost is the server of a saved working copy.
func (a *App) wcHost(wc string) string {
	host := ""
	a.cfg.Read(func(c *config.Config) {
		for _, w := range c.WorkingCopies {
			if filepath.Clean(w.Path) == filepath.Clean(wc) {
				host = hostOf(w.RepoRoot)
			}
		}
	})
	return host
}

func (a *App) client() (*svn.Client, error) {
	if a.svn == nil {
		return nil, &appError{Kind: "nosvn", Message: a.svnErr.Error()}
	}
	return a.svn, nil
}

// --- Start-up -------------------------------------------------------------

// Startup is what the page needs to draw itself.
type Startup struct {
	Version       string               `json:"version"`
	SvnPath       string               `json:"svnPath"`
	SvnVersion    string               `json:"svnVersion"`
	SvnError      string               `json:"svnError"`
	OS            string               `json:"os"`
	WorkingCopies []config.WorkingCopy `json:"workingCopies"`
	Servers       []config.Server      `json:"servers"`
}

func (a *App) Startup() Startup {
	s := Startup{Version: a.Version(), OS: goruntime.GOOS}
	if a.svn == nil {
		s.SvnError = a.svnErr.Error()
	} else {
		s.SvnPath = a.svn.Path
		v, err := a.svn.Version(a.ctx)
		if err != nil {
			s.SvnError = err.Error()
		}
		s.SvnVersion = v
	}
	a.cfg.Read(func(c *config.Config) {
		s.WorkingCopies = slices.Clone(c.WorkingCopies)
		s.Servers = slices.Clone(c.Servers)
	})
	return s
}

// Version is the running version.
func (a *App) Version() string {
	if Version == "" {
		return "dev"
	}
	return Version
}

// --- Logins and certificates ----------------------------------------------

// Login sets the username and password for host for the rest of the session.
// svn itself also caches them if a secure store (GNOME Keyring, KWallet, the
// Windows credential store) is available.
func (a *App) Login(host, username, password string) error {
	a.mu.Lock()
	a.creds[host] = svn.Creds{Username: username, Password: password}
	a.mu.Unlock()
	return a.cfg.Update(func(c *config.Config) { c.Usernames[host] = username })
}

// LastUsername is the username last used for host, to fill the login dialog.
func (a *App) LastUsername(host string) string {
	name := ""
	a.cfg.Read(func(c *config.Config) { name = c.Usernames[host] })
	return name
}

// TrustHost accepts host's certificate from now on.
func (a *App) TrustHost(host string) error {
	return a.cfg.Update(func(c *config.Config) { c.Trust(host) })
}

// --- Working copies and servers --------------------------------------------

// PickFolder asks for a folder.
func (a *App) PickFolder(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

// AddWorkingCopy adds an existing working copy to the sidebar.
func (a *App) AddWorkingCopy(path string) (*config.WorkingCopy, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	info, err := c.Info(a.ctx, svn.Opts{}, path)
	if err != nil {
		if strings.Contains(err.Error(), "E155007") {
			return nil, &appError{Kind: "other", Message: path + " is not an SVN working copy."}
		}
		return nil, a.wrap(err, "")
	}
	wc := config.WorkingCopy{Name: filepath.Base(info.Path), Path: info.Path, URL: info.URL, RepoRoot: info.RepoRoot}
	return &wc, a.cfg.Update(func(c *config.Config) { c.AddWorkingCopy(wc) })
}

// RemoveWorkingCopy takes a working copy off the sidebar; its files stay.
func (a *App) RemoveWorkingCopy(path string) error {
	return a.cfg.Update(func(c *config.Config) { c.RemoveWorkingCopy(path) })
}

// RenameWorkingCopy changes the name the sidebar shows.
func (a *App) RenameWorkingCopy(path, name string) error {
	return a.cfg.Update(func(c *config.Config) {
		for i, w := range c.WorkingCopies {
			if filepath.Clean(w.Path) == filepath.Clean(path) {
				c.WorkingCopies[i].Name = name
			}
		}
	})
}

// AddServer saves a repository URL to browse, after checking it answers.
func (a *App) AddServer(name, u string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if _, err := c.List(a.ctx, a.opts(hostOf(u)), u); err != nil {
		return a.wrap(err, hostOf(u))
	}
	if name == "" {
		name = u
	}
	return a.cfg.Update(func(c *config.Config) { c.AddServer(config.Server{Name: name, URL: u}) })
}

// RemoveServer forgets a saved repository URL.
func (a *App) RemoveServer(u string) error {
	return a.cfg.Update(func(c *config.Config) { c.RemoveServer(u) })
}

// --- Reading -------------------------------------------------------------

// Info describes a working copy, refreshing its saved URL (it changes after
// a switch or relocate).
func (a *App) Info(wc string) (*svn.Info, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	info, err := c.Info(a.ctx, a.opts(host), wc)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	_ = a.cfg.Update(func(c *config.Config) {
		for i, w := range c.WorkingCopies {
			if filepath.Clean(w.Path) == filepath.Clean(wc) {
				c.WorkingCopies[i].URL, c.WorkingCopies[i].RepoRoot = info.URL, info.RepoRoot
			}
		}
	})
	return info, nil
}

// StatusResult is a working copy's changes.
type StatusResult struct {
	Changes []svn.Change `json:"changes"`
	Head    int          `json:"head"` // the server's HEAD, with remote
}

// Status lists a working copy's changes; remote also asks the server what an
// update would bring.
func (a *App) Status(wc string, remote bool) (*StatusResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	changes, head, err := c.Status(a.ctx, a.opts(host), wc, remote)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if changes == nil {
		changes = []svn.Change{}
	}
	return &StatusResult{Changes: changes, Head: head}, nil
}

// Diff is a working copy file's local changes. Unversioned files show as
// all new.
func (a *App) Diff(wc, rel string) (string, error) {
	c, err := a.client()
	if err != nil {
		return "", err
	}
	path := filepath.Join(wc, filepath.FromSlash(rel))
	host := a.wcHost(wc)
	changes, _, err := c.Status(a.ctx, a.opts(host), path, false)
	if err == nil && len(changes) == 1 && changes[0].Item == "unversioned" {
		return newFileDiff(path, rel)
	}
	d, err := c.Diff(a.ctx, a.opts(host), path, 0)
	return d, a.wrap(err, host)
}

// newFileDiff shows an unversioned file as a diff that adds every line.
func newFileDiff(path, rel string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", nil
	}
	if st.Size() > svn.MaxDiff {
		return "(new file, too big to show)", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if slices.Contains(b[:min(len(b), 8000)], 0) {
		return "(new binary file)", nil
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var sb strings.Builder
	fmt.Fprintf(&sb, "Index: %s (new, not yet added)\n===================================================================\n--- /dev/null\n+++ %s\n@@ -0,0 +1,%d @@\n", rel, rel, len(lines))
	for _, l := range lines {
		sb.WriteString("+" + l + "\n")
	}
	return sb.String(), nil
}

// Log lists revisions of a working copy or URL, newest first, from revision
// from (0 for the newest) back.
func (a *App) Log(target string, from, limit int) ([]svn.LogEntry, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(target)
	if host == "" {
		host = a.wcHost(target)
	}
	entries, err := c.Log(a.ctx, a.opts(host), target, from, limit)
	return entries, a.wrap(err, host)
}

// RevisionDiff is what revision rev changed under target (a repository URL).
func (a *App) RevisionDiff(target string, rev int) (string, error) {
	c, err := a.client()
	if err != nil {
		return "", err
	}
	host := hostOf(target)
	d, err := c.Diff(a.ctx, a.opts(host), target, rev)
	return d, a.wrap(err, host)
}

// List lists a repository folder.
func (a *App) List(u string) ([]svn.Entry, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(u)
	entries, err := c.List(a.ctx, a.opts(host), u)
	if entries == nil {
		entries = []svn.Entry{}
	}
	return entries, a.wrap(err, host)
}

// RepoInfo describes a URL (its repository root, for the browser's path bar).
func (a *App) RepoInfo(u string) (*svn.Info, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(u)
	info, err := c.Info(a.ctx, a.opts(host), u)
	return info, a.wrap(err, host)
}

// --- Long operations ----------------------------------------------------------

// longOp runs f with a context Cancel can stop, one at a time.
func (a *App) longOp(f func(ctx context.Context, line func(string)) error) error {
	a.mu.Lock()
	if a.opCancel != nil {
		a.mu.Unlock()
		return &appError{Kind: "other", Message: "Another checkout, update, export or import is still running."}
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.opCancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.opCancel = nil
		a.mu.Unlock()
		cancel()
		runtime.EventsEmit(a.ctx, evOpDone)
	}()
	return f(ctx, func(l string) { runtime.EventsEmit(a.ctx, evOpLine, l) })
}

// Cancel stops the running checkout or update.
func (a *App) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.opCancel != nil {
		a.opCancel()
	}
}

// Checkout checks u out into dir and adds it to the sidebar. An interrupted
// checkout can be finished with an update.
func (a *App) Checkout(u, dir string) (*config.WorkingCopy, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(u)
	err = a.longOp(func(ctx context.Context, line func(string)) error {
		return c.Checkout(ctx, a.opts(host), u, dir, line)
	})
	if err != nil {
		return nil, a.wrap(err, host)
	}
	return a.AddWorkingCopy(dir)
}

// Export copies u's files into dir, a new folder, as plain files that are
// not a working copy.
func (a *App) Export(u, dir string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		return &appError{Kind: "other", Message: dir + " already exists. Pick a new folder name."}
	}
	host := hostOf(u)
	return a.wrap(a.longOp(func(ctx context.Context, line func(string)) error {
		return c.Export(ctx, a.opts(host), u, dir, line)
	}), host)
}

// Import commits the contents of the local folder dir to the repository
// folder u (made, with any missing parents) and returns the new revision.
// It refuses a u that already has files, so nothing is mixed into existing
// work. inPlace then makes dir itself a working copy of u (and adds it to
// the sidebar); otherwise dir is left as it was.
func (a *App) Import(dir, u, message string, inPlace bool) (int, error) {
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(message) == "" {
		return 0, &appError{Kind: "other", Message: "Write a commit message first."}
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return 0, &appError{Kind: "other", Message: dir + " is not a folder."}
	}
	host := hostOf(u)
	entries, err := c.List(a.ctx, a.opts(host), u)
	switch k := svn.KindOf(err); {
	case err == nil && len(entries) > 0:
		return 0, &appError{Kind: "other", Message: u + " already exists and has files in it. Pick a new folder name."}
	case k == svn.KindAuth || k == svn.KindCert:
		return 0, a.wrap(err, host)
	}
	rev := 0
	err = a.longOp(func(ctx context.Context, line func(string)) error {
		var err error
		rev, err = c.Import(ctx, a.opts(host), dir, u, message, line)
		if err != nil || !inPlace {
			return err
		}
		line("")
		line("Making " + dir + " a working copy:")
		return c.CheckoutInPlace(ctx, a.opts(host), u, dir, line)
	})
	if err != nil {
		if rev > 0 {
			// The import itself went in: say so, the checkout is what failed.
			return rev, &appError{Kind: "other", Message: fmt.Sprintf(
				"Imported as revision %d, but making the folder a working copy failed: %s", rev, err)}
		}
		return 0, a.wrap(err, host)
	}
	if inPlace {
		if _, err := a.AddWorkingCopy(dir); err != nil {
			return rev, err
		}
	}
	return rev, nil
}

// Update brings a working copy up to date and returns svn's summary lines.
func (a *App) Update(wc string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	return a.wrap(a.longOp(func(ctx context.Context, line func(string)) error {
		return c.Update(ctx, a.opts(host), wc, line)
	}), host)
}

// --- Changing the working copy ---------------------------------------------------

func abs(wc string, rel []string) []string {
	out := make([]string, len(rel))
	for i, r := range rel {
		out[i] = filepath.Join(wc, filepath.FromSlash(r))
	}
	return out
}

// Commit commits the chosen paths (relative to wc) and returns the new
// revision. Unversioned paths are added first (folders with their contents),
// missing ones deleted, and a new parent folder is taken along, since svn
// cannot commit a file whose folder is not in the repository yet.
func (a *App) Commit(wc, message string, rel []string) (int, error) {
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(message) == "" {
		return 0, &appError{Kind: "other", Message: "Write a commit message first."}
	}
	if len(rel) == 0 {
		return 0, &appError{Kind: "other", Message: "Pick at least one change to commit."}
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	changes, _, err := c.Status(a.ctx, o, wc, false)
	if err != nil {
		return 0, a.wrap(err, host)
	}
	byPath := map[string]svn.Change{}
	for _, ch := range changes {
		byPath[ch.Path] = ch
	}

	var toAdd, toDelete, addedDirs []string
	for _, r := range rel {
		ch, ok := byPath[r]
		if !ok {
			continue
		}
		if ch.Conflicted() {
			return 0, &appError{Kind: "conflict", Message: r + " has a conflict. Resolve it before committing."}
		}
		switch ch.Item {
		case "unversioned":
			toAdd = append(toAdd, r)
			if ch.IsDir {
				addedDirs = append(addedDirs, r)
			}
		case "missing":
			toDelete = append(toDelete, r)
		}
	}
	if len(toAdd) > 0 {
		if err := c.Add(a.ctx, o, abs(wc, toAdd)...); err != nil {
			return 0, a.wrap(err, host)
		}
	}
	if len(toDelete) > 0 {
		if err := c.Delete(a.ctx, o, abs(wc, toDelete)...); err != nil {
			return 0, a.wrap(err, host)
		}
	}

	// What is now scheduled: the targets, the contents of folders just added,
	// and any added parent folders of the targets.
	changes, _, err = c.Status(a.ctx, o, wc, false)
	if err != nil {
		return 0, a.wrap(err, host)
	}
	byPath = map[string]svn.Change{}
	for _, ch := range changes {
		byPath[ch.Path] = ch
	}
	targets := map[string]bool{}
	for _, r := range rel {
		targets[r] = true
	}
	for _, ch := range changes {
		for _, d := range addedDirs {
			if strings.HasPrefix(ch.Path, d+"/") && ch.Item != "unversioned" {
				targets[ch.Path] = true
			}
		}
	}
	for t := range targets {
		for p := parent(t); p != "."; p = parent(p) {
			if ch, ok := byPath[p]; ok && (ch.Item == "added" || ch.Item == "replaced") {
				targets[p] = true
			}
		}
	}
	list := make([]string, 0, len(targets))
	for t := range targets {
		list = append(list, t)
	}
	slices.Sort(list)
	rev, err := c.Commit(a.ctx, o, message, abs(wc, list))
	return rev, a.wrap(err, host)
}

func parent(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	return p[:i]
}

// Add schedules unversioned paths for addition.
func (a *App) Add(wc string, rel []string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	return a.wrap(c.Add(a.ctx, a.opts(host), abs(wc, rel)...), host)
}

// Revert throws away local changes to paths. An unversioned file is not
// touched (svn revert leaves it alone).
func (a *App) Revert(wc string, rel []string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	return a.wrap(c.Revert(a.ctx, a.opts(host), abs(wc, rel)...), host)
}

// Resolve marks conflicts resolved: accept is "working", "mine-full" or
// "theirs-full".
func (a *App) Resolve(wc, accept string, rel []string) error {
	if !slices.Contains([]string{"working", "mine-full", "theirs-full"}, accept) {
		return errors.New("unknown resolution " + accept)
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	return a.wrap(c.Resolve(a.ctx, a.opts(host), accept, abs(wc, rel)...), host)
}

// Cleanup releases a locked working copy.
func (a *App) Cleanup(wc string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	return a.wrap(c.Cleanup(a.ctx, a.opts(host), wc), host)
}

// --- The desktop ------------------------------------------------------------------

// Open opens a working copy file or folder with the system's default app.
func (a *App) Open(wc, rel string) error {
	p := filepath.Join(wc, filepath.FromSlash(rel))
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", p)
	case "darwin":
		cmd = exec.Command("open", p)
	default:
		cmd = exec.Command("xdg-open", p)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
