package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"trunkline/internal/config"
	"trunkline/internal/svn"
	"trunkline/internal/update"
)

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

	updating sync.Mutex // one app update at a time
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

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	update.CleanupOld()
	go a.watchUpdates()
}

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
		svn.KindLocked: "locked", svn.KindCanceled: "canceled", svn.KindLockConflict: "lockconflict", svn.KindNeedUpdate: "needupdate", svn.KindNeedClean: "needclean",
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
	Home          string               `json:"home"`        // the user's home folder, shown as ~
	UpdateCheck   bool                 `json:"updateCheck"` // check for new releases automatically
	Theme         string               `json:"theme"`       // "dark", "light" or "system"
	Font          string               `json:"font"`        // "mono" or "regular"
	Zoom          int                  `json:"zoom"`        // percent
}

func (a *App) Startup() Startup {
	s := Startup{Version: a.Version(), OS: goruntime.GOOS}
	s.Home, _ = os.UserHomeDir()
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
		s.UpdateCheck = !c.NoUpdateCheck
		s.Theme, s.Font = lookOf(c)
		s.Zoom = 100
		if c.Zoom >= minZoom && c.Zoom <= maxZoom {
			s.Zoom = c.Zoom
		}
	})
	return s
}

// Version is the running version.
func (a *App) Version() string {
	if update.Version == "" {
		return "dev"
	}
	return update.Version
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
	return svn.WholeFileDiff(b, rel, true), nil
}

// Log lists revisions of a working copy or URL, newest first, from revision
// from (0 for the newest) back.
// search, when not "", keeps only revisions whose message, author or changed
// paths contain it (see svn.LogSearch).
func (a *App) Log(target string, from, limit int, search string) ([]svn.LogEntry, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(target)
	if host == "" {
		host = a.wcHost(target)
	}
	entries, err := c.LogSearch(a.ctx, a.opts(host), target, from, limit, search)
	if entries == nil {
		entries = []svn.LogEntry{}
	}
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
//
// Files that still have conflict markers in them (a conflict marked resolved
// without editing it) are refused, unless allowMarkers: the user confirmed
// the markers are meant to be there.
// CommitOptions are the choices a commit can override.
type CommitOptions struct {
	AllowMarkers      bool `json:"allowMarkers"`      // commit files that still have conflict markers
	KeepLocks         bool `json:"keepLocks"`         // keep this working copy's locks on the committed files
	AllowPartialMerge bool `json:"allowPartialMerge"` // commit a merge record without all the other changes
}

func (a *App) Commit(wc, message string, rel []string, opt CommitOptions) (int, error) {
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
	if !opt.AllowMarkers {
		if err := markersError(wc, rel, "Commit"); err != nil {
			return 0, err
		}
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
	if !opt.AllowPartialMerge {
		if err := a.partialMergeError(wc, list, changes); err != nil {
			return 0, err
		}
	}
	rev, err := c.CommitKeepLocks(a.ctx, o, message, abs(wc, list), opt.KeepLocks)
	if svn.KindOf(err) == svn.KindLockConflict {
		return 0, &appError{Kind: "lockconflict", Host: host, Message: "Commit stopped: you no longer hold the lock " +
			"on a file you changed. Someone took it over or removed it (see the Locks tab).\n\n" + err.Error()}
	}
	return rev, a.wrap(err, host)
}

// partialMergeError is a "partialmerge" error when the commit (targets) takes
// a merge record (svn:mergeinfo) along but leaves other local changes behind,
// which is what a merge leaves: committing only part of it would record
// revisions as merged that aren't. nil when that's not the case.
func (a *App) partialMergeError(wc string, targets []string, changes []svn.Change) error {
	c, _ := a.client()
	o := a.opts(a.wcHost(wc))
	merge := false
	for _, t := range targets {
		if ok, err := c.MergeRecordChanged(a.ctx, o, filepath.Join(wc, filepath.FromSlash(t))); err == nil && ok {
			merge = true
			break
		}
	}
	if !merge {
		return nil
	}
	var left []string
	for _, ch := range changes {
		if ch.Item != "unversioned" && ch.Item != "external" && !slices.Contains(targets, ch.Path) &&
			(ch.Item != "normal" || ch.Props == "modified" || ch.TreeConflict) {
			left = append(left, ch.Path)
		}
	}
	if len(left) == 0 {
		return nil
	}
	shown := left[:min(len(left), 8)]
	more := ""
	if len(left) > len(shown) {
		more = fmt.Sprintf("\n  … and %d more", len(left)-len(shown))
	}
	return &appError{Kind: "partialmerge", Message: "This commit includes a merge record (which revisions were merged), " +
		"but leaves these changes out:\n\n  " + strings.Join(shown, "\n  ") + more + "\n\n" +
		"A merge should be committed whole: committing the record without them tells svn those revisions are " +
		"merged when they aren't, and a later merge would skip them. Tick everything and commit again."}
}

// markersError is a "markers" error listing the files under wc that still
// have conflict markers, or nil when none do.
func markersError(wc string, rel []string, action string) error {
	found := svn.FilesWithConflictMarkers(abs(wc, rel))
	if len(found) == 0 {
		return nil
	}
	root := filepath.Clean(wc) + string(filepath.Separator)
	var list []string
	for i, f := range found {
		if i == 5 {
			list = append(list, fmt.Sprintf("… and %d more", len(found)-5))
			break
		}
		path, line, _ := strings.Cut(strings.TrimPrefix(f, root), ":")
		list = append(list, fmt.Sprintf("%s (line %s)", filepath.ToSlash(path), line))
	}
	return &appError{Kind: "markers", Message: fmt.Sprintf(
		"%s stopped: conflict markers (<<<<<<<, =======, >>>>>>>) are still in:\n\n  %s\n\n"+
			"Edit the file to the text you want and delete the marker lines, or resolve with "+
			"\"keep mine\" or \"take theirs\".", action, strings.Join(list, "\n  "))}
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

// Resolve marks conflicts resolved: accept is "working" (the file as the user
// edited it), "mine-full" or "theirs-full". "working" is refused while the
// file still has conflict markers, unless allowMarkers.
func (a *App) Resolve(wc, accept string, rel []string, allowMarkers bool) error {
	if !slices.Contains([]string{"working", "mine-full", "theirs-full"}, accept) {
		return errors.New("unknown resolution " + accept)
	}
	if accept == "working" && !allowMarkers {
		if err := markersError(wc, rel, "Mark resolved"); err != nil {
			return err
		}
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

// --- Updates ------------------------------------------------------------------

// Events for updates.
const (
	evUpdateAvailable = "update:available" // update.Info, from the automatic check
	evUpdateProgress  = "update:progress"  // string, one per install step
)

// How often the automatic check runs. GitHub allows 60 unauthenticated API
// calls an hour per address, which a whole office behind one address shares.
const (
	updateFirstCheck = 10 * time.Second
	updateEvery      = 6 * time.Hour
)

// watchUpdates checks for a new release shortly after start, then every
// updateEvery, and tells the window when there is one.
func (a *App) watchUpdates() {
	wait := updateFirstCheck
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateEvery
		off := false
		a.cfg.Read(func(c *config.Config) { off = c.NoUpdateCheck })
		if off {
			continue
		}
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		info, err := update.New().Check(ctx)
		cancel()
		if err == nil && info.Available {
			runtime.EventsEmit(a.ctx, evUpdateAvailable, info)
		}
	}
}

// CheckUpdate looks for a newer release now.
func (a *App) CheckUpdate() (update.Info, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	info, err := update.New().Check(ctx)
	if err != nil {
		return info, &appError{Kind: "other", Message: "Couldn't check for updates: " + err.Error()}
	}
	return info, nil
}

// RepoURL is the app's GitHub page.
func (a *App) RepoURL() string { return "https://github.com/" + update.Repo }

// InstallKind is how this copy was installed: deb, windows or source.
func (a *App) InstallKind() string { return update.InstallKind() }

// SetUpdateCheck turns the automatic check on or off.
func (a *App) SetUpdateCheck(on bool) error {
	return a.cfg.Update(func(c *config.Config) { c.NoUpdateCheck = !on })
}

// lookOf is the saved theme ("dark", "light", "system") and font ("mono",
// "regular"), with anything unknown read as the default.
func lookOf(c *config.Config) (theme, font string) {
	theme, font = "system", "mono"
	if c.Theme == "dark" || c.Theme == "light" {
		theme = c.Theme
	}
	if c.Font == "regular" {
		font = c.Font
	}
	return theme, font
}

// windowColour is the window's background before the page has painted: the
// light theme's when it was picked, otherwise the dark theme's (the page
// sets it again once it knows the system's setting).
func (a *App) windowColour() *options.RGBA {
	theme := "system"
	a.cfg.Read(func(c *config.Config) { theme, _ = lookOf(c) })
	if theme == "light" {
		return &options.RGBA{R: 0xf4, G: 0xf2, B: 0xf9, A: 255}
	}
	return &options.RGBA{R: 0x16, G: 0x15, B: 0x1f, A: 255}
}

// The window's size (zoom) in percent can be from minZoom to maxZoom.
const minZoom, maxZoom = 50, 200

// SetLook saves the theme ("system", "dark" or "light"), the font ("mono" or
// "regular") and the zoom (percent).
func (a *App) SetLook(theme, font string, zoom int) error {
	if !slices.Contains([]string{"system", "dark", "light"}, theme) || !slices.Contains([]string{"mono", "regular"}, font) ||
		zoom < minZoom || zoom > maxZoom {
		return fmt.Errorf("unknown look %q, %q, %d%%", theme, font, zoom)
	}
	return a.cfg.Update(func(c *config.Config) {
		c.Theme, c.Font, c.Zoom = theme, font, zoom
		if zoom == 100 {
			c.Zoom = 0
		}
		if theme == "system" {
			c.Theme = ""
		}
		if font == "mono" {
			c.Font = ""
		}
	})
}

// InstallUpdate installs release tag, then starts the new version and quits.
func (a *App) InstallUpdate(tag string) error {
	if !a.updating.TryLock() {
		return &appError{Kind: "other", Message: "An update is already being installed."}
	}
	defer a.updating.Unlock()
	restart, err := update.New().Install(a.ctx, tag, func(s string) { runtime.EventsEmit(a.ctx, evUpdateProgress, s) })
	if err != nil {
		return &appError{Kind: "other", Message: err.Error()}
	}
	if restart != "" {
		if err := update.Restart(restart); err != nil {
			return &appError{Kind: "other", Message: "Installed " + tag + ", but restarting failed: " + err.Error() + ". Start TrunkLine again."}
		}
	}
	runtime.Quit(a.ctx)
	return nil
}

// OpenReleasePage opens this app's repository page, or a page in it (a
// release, from the update check), in the browser. Nothing else.
func (a *App) OpenReleasePage(u string) error {
	base := "https://github.com/" + update.Repo
	if u != base && !strings.HasPrefix(u, base+"/") {
		return &appError{Kind: "other", Message: "Not a page of this app's repository: " + u}
	}
	runtime.BrowserOpenURL(a.ctx, u)
	return nil
}

// --- Blame ----------------------------------------------------------------------

// BlameResult is a file's lines with who last changed each.
type BlameResult struct {
	Title    string          `json:"title"`    // the file, as ^/trunk/a.c (@rev for an older version)
	RepoRoot string          `json:"repoRoot"` // for looking up a revision's message (Log)
	Lines    []svn.BlameLine `json:"lines"`
}

// BlameFile blames a working copy file: its committed version, without the
// local edits.
func (a *App) BlameFile(wc, rel string) (*BlameResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	path := filepath.Join(wc, filepath.FromSlash(rel))
	info, err := c.Info(a.ctx, a.opts(host), path)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if info.Kind != "file" {
		return nil, &appError{Kind: "other", Message: rel + " is a folder: blame works on files."}
	}
	lines, err := c.Blame(a.ctx, a.opts(host), path)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	return &BlameResult{Title: info.RelativeURL, RepoRoot: info.RepoRoot, Lines: nonNil(lines)}, nil
}

// BlameURL blames a repository file, u (optionally url@rev).
func (a *App) BlameURL(u string) (*BlameResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(u)
	info, err := c.Info(a.ctx, a.opts(host), u)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if info.Kind != "file" {
		return nil, &appError{Kind: "other", Message: "That is a folder: blame works on files."}
	}
	lines, err := c.Blame(a.ctx, a.opts(host), u)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	title := info.RelativeURL
	if i := strings.LastIndex(u, "@"); i > strings.LastIndex(u, "/") {
		title += u[i:]
	}
	return &BlameResult{Title: title, RepoRoot: info.RepoRoot, Lines: nonNil(lines)}, nil
}

func nonNil(l []svn.BlameLine) []svn.BlameLine {
	if l == nil {
		return []svn.BlameLine{}
	}
	return l
}

// --- Locks ----------------------------------------------------------------------

// LockedFile is a file on the Locks tab: one that needs a lock to be edited,
// or one that is locked.
type LockedFile struct {
	Path      string        `json:"path"`      // relative to the working copy, with /
	NeedsLock bool          `json:"needsLock"` // svn:needs-lock: read-only until locked
	State     string        `json:"state"`     // "" (not locked), mine, other, stolen, broken
	Lock      *svn.LockInfo `json:"lock,omitempty"`
	Modified  bool          `json:"modified"` // has local changes
}

// Locks lists the working copy's needs-lock files and every locked file,
// asking the server who holds which lock.
func (a *App) Locks(wc string) ([]LockedFile, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	needs, err := c.NeedsLock(a.ctx, o, wc)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	changes, _, err := c.Status(a.ctx, o, wc, true)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	files := map[string]*LockedFile{}
	for _, p := range needs {
		files[p] = &LockedFile{Path: p, NeedsLock: true}
	}
	for _, ch := range changes {
		f, ok := files[ch.Path]
		if !ok {
			if ch.LockState == "" {
				continue
			}
			f = &LockedFile{Path: ch.Path}
			files[ch.Path] = f
		}
		f.State, f.Lock = ch.LockState, ch.Lock
		f.Modified = ch.Item == "modified" || ch.Props == "modified"
	}
	out := make([]LockedFile, 0, len(files))
	for _, f := range files {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(x, y LockedFile) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

// LockFiles locks files (relative to wc) so only this working copy can commit
// them. steal takes over locks someone else holds. Without steal, files
// someone else has locked are reported as a "lockconflict" error saying who;
// the others are locked anyway.
func (a *App) LockFiles(wc string, rel []string, comment string, steal bool) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	if len(rel) == 0 {
		return &appError{Kind: "other", Message: "Pick a file to lock."}
	}
	host := a.wcHost(wc)
	err = c.Lock(a.ctx, a.opts(host), comment, steal, abs(wc, rel)...)
	if svn.KindOf(err) == svn.KindLockConflict {
		var who []string
		for _, pw := range svn.LockedBy(err) {
			who = append(who, fmt.Sprintf("%s (locked by %s)", pw[0], pw[1]))
		}
		if len(who) == 0 {
			who = []string{err.Error()}
		}
		return &appError{Kind: "lockconflict", Host: host, Message: "Someone else holds the lock on:\n\n  " + strings.Join(who, "\n  ")}
	}
	return a.wrap(err, host)
}

// UnlockFiles releases locks on files (relative to wc). breakOthers also
// removes locks this working copy doesn't hold.
func (a *App) UnlockFiles(wc string, rel []string, breakOthers bool) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	if len(rel) == 0 {
		return &appError{Kind: "other", Message: "Pick a file to unlock."}
	}
	host := a.wcHost(wc)
	return a.wrap(c.Unlock(a.ctx, a.opts(host), breakOthers, abs(wc, rel)...), host)
}

// PickFileIn asks for a file inside wc and returns its path relative to wc
// ("" if canceled).
func (a *App) PickFileIn(wc, title string) (string, error) {
	p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title, DefaultDirectory: wc})
	if err != nil || p == "" {
		return "", err
	}
	r, err := filepath.Rel(wc, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", &appError{Kind: "other", Message: p + " is not inside this working copy."}
	}
	return filepath.ToSlash(r), nil
}

// --- Branches -------------------------------------------------------------------

// Branch is the trunk, a branch or a tag on the Branches tab.
type Branch struct {
	Kind    string     `json:"kind"` // trunk, branch, tag
	Name    string     `json:"name"`
	URL     string     `json:"url"`     // what a switch goes to (same subfolder as the working copy)
	Commit  svn.Commit `json:"commit"`  // its last change
	Current bool       `json:"current"` // the working copy is on it
}

// BranchesResult is the Branches tab.
type BranchesResult struct {
	Layout   svn.Layout `json:"layout"` // Kind "" when the repository doesn't use trunk/branches/tags
	URL      string     `json:"url"`    // the working copy's URL
	Revision int        `json:"revision"`
	Branches []Branch   `json:"branches"` // trunk first, then branches, then tags
	Changes  int        `json:"changes"`  // local changes that a switch would carry along
}

// Branches lists the working copy's project's trunk, branches and tags.
func (a *App) Branches(wc string) (*BranchesResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	info, err := c.Info(a.ctx, o, wc)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	r := &BranchesResult{Layout: svn.ParseLayout(info.URL), URL: info.URL, Revision: info.Revision, Branches: []Branch{}}
	changes, _, err := c.Status(a.ctx, o, wc, false)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	for _, ch := range changes {
		if ch.Item != "unversioned" && ch.Item != "normal" && ch.Item != "external" || ch.Props == "modified" {
			r.Changes++
		}
	}
	l := r.Layout
	if l.Kind == "" {
		return r, nil
	}
	// The project folder: trunk's last change comes from its entry there.
	top, err := c.List(a.ctx, o, l.Base)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	add := func(kind, name string, commit svn.Commit) {
		r.Branches = append(r.Branches, Branch{Kind: kind, Name: name, URL: l.URLOf(kind, name), Commit: commit,
			Current: kind == l.Kind && name == l.Name})
	}
	for _, e := range top {
		if e.Name == "trunk" && e.Kind == "dir" {
			add(svn.KindTrunk, "trunk", e.Commit)
		}
	}
	for _, folder := range []struct{ dir, kind string }{{"branches", svn.KindBranch}, {"tags", svn.KindTag}} {
		if !slices.ContainsFunc(top, func(e svn.Entry) bool { return e.Name == folder.dir && e.Kind == "dir" }) {
			continue
		}
		entries, err := c.List(a.ctx, o, l.Base+"/"+folder.dir)
		if err != nil {
			return nil, a.wrap(err, host)
		}
		for _, e := range entries {
			if e.Kind == "dir" {
				add(folder.kind, e.Name, e.Commit)
			}
		}
	}
	return r, nil
}

// CreateBranch copies the whole trunk or branch the working copy is on to a
// new branch or tag, in one commit, and returns the new revision. from is
// "head" (the latest on the server) or "wc" (the working copy's revision).
// The working copy isn't switched (SwitchTo does that).
func (a *App) CreateBranch(wc, kind, name, from, message string) (int, error) {
	if kind != svn.KindBranch && kind != svn.KindTag {
		return 0, &appError{Kind: "other", Message: "Unknown kind " + kind}
	}
	if !svn.ValidBranchName(name) {
		return 0, &appError{Kind: "other", Message: "Use letters, digits, \".\", \"_\" and \"-\" for the name, starting with a letter or digit (no spaces or slashes)."}
	}
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	info, err := c.Info(a.ctx, o, wc)
	if err != nil {
		return 0, a.wrap(err, host)
	}
	l := svn.ParseLayout(info.URL)
	if l.Kind == "" {
		return 0, &appError{Kind: "other", Message: "This working copy isn't in a trunk/branches/tags layout, so TrunkLine doesn't know where branches go."}
	}
	whole := svn.Layout{Base: l.Base} // the whole trunk or branch, not just the checked-out subfolder
	src := whole.URLOf(l.Kind, l.Name)
	switch rev, err := strconv.Atoi(strings.TrimPrefix(from, "r")); {
	case from == "head":
	case from == "wc":
		src += "@" + strconv.Itoa(info.Revision)
	case strings.HasPrefix(from, "r") && err == nil && rev > 0: // a revision from the history
		src += "@" + strconv.Itoa(rev)
	default:
		return 0, &appError{Kind: "other", Message: "Unknown source " + from}
	}
	dst := whole.URLOf(kind, name)
	exists, err := c.Exists(a.ctx, o, dst)
	if err != nil {
		return 0, a.wrap(err, host)
	}
	if exists {
		return 0, &appError{Kind: "other", Message: fmt.Sprintf("A %s called %q already exists. Pick another name.", kind, name)}
	}
	if strings.TrimSpace(message) == "" {
		message = fmt.Sprintf("Create %s %s from %s", kind, name, l.Name)
	}
	rev, err := c.Copy(a.ctx, o, src, dst, message)
	return rev, a.wrap(err, host)
}

// SwitchTo moves the working copy to u (a trunk, branch or tag URL from
// Branches, or any URL in the same repository), keeping local changes.
func (a *App) SwitchTo(wc, u string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	err = a.longOp(func(ctx context.Context, line func(string)) error {
		return c.Switch(ctx, a.opts(host), wc, strings.TrimRight(strings.TrimSpace(u), "/"), line)
	})
	if err != nil {
		return a.wrap(err, host)
	}
	_, err = a.Info(wc) // saves the new URL
	return err
}

// DeleteBranch removes a branch or tag from the repository (it stays in the
// history). The one the working copy is on can't be deleted.
func (a *App) DeleteBranch(wc, kind, name, message string) (int, error) {
	if kind != svn.KindBranch && kind != svn.KindTag {
		return 0, &appError{Kind: "other", Message: "Only branches and tags can be deleted here."}
	}
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	info, err := c.Info(a.ctx, o, wc)
	if err != nil {
		return 0, a.wrap(err, host)
	}
	l := svn.ParseLayout(info.URL)
	if l.Kind == "" {
		return 0, &appError{Kind: "other", Message: "This working copy isn't in a trunk/branches/tags layout."}
	}
	if l.Kind == kind && l.Name == name {
		return 0, &appError{Kind: "other", Message: "This working copy is on " + name + ". Switch to another branch first."}
	}
	if strings.TrimSpace(message) == "" {
		message = fmt.Sprintf("Delete %s %s", kind, name)
	}
	rev, err := c.DeleteURL(a.ctx, o, svn.Layout{Base: l.Base}.URLOf(kind, name), message)
	return rev, a.wrap(err, host)
}

// --- Undo a commit ----------------------------------------------------------------

// UndoRevision takes the changes of revision rev back out of the working
// copy (a reverse merge), as uncommitted local changes to review and commit.
// The working copy must be updated first when it's at mixed revisions or
// older than rev: a "needupdate" error.
func (a *App) UndoRevision(wc string, rev int) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	if rev < 1 {
		return &appError{Kind: "other", Message: "Pick a revision to undo."}
	}
	host := a.wcHost(wc)
	info, err := c.Info(a.ctx, a.opts(host), wc)
	if err != nil {
		return a.wrap(err, host)
	}
	if rev > info.Revision {
		return &appError{Kind: "needupdate", Message: fmt.Sprintf(
			"r%d is newer than your working copy (r%d): update first, so the undo applies to files that have that change.", rev, info.Revision)}
	}
	err = a.longOp(func(ctx context.Context, line func(string)) error {
		return c.Merge(ctx, a.opts(host), info.URL, -rev, wc, line)
	})
	if svn.KindOf(err) == svn.KindNeedUpdate {
		return &appError{Kind: "needupdate", Message: "Your working copy has files at different revisions " +
			"(normal after a commit): svn needs it updated before it can undo a commit."}
	}
	return a.wrap(err, host)
}

// --- Ignore ---------------------------------------------------------------------

// Ignore stops unversioned paths (relative to wc) showing as changes, by
// adding a pattern to their folder's svn:ignore (or svn:global-ignores with
// recursive, which also covers the folders below). byExt ignores every file
// with the same extension ("*.log") instead of the exact names. The folder's
// property change is a local change, committed like any other so everyone
// gets the same ignores. It returns the folders whose ignore list changed
// (relative to wc, "." for the top), to commit.
func (a *App) Ignore(wc string, rel []string, byExt, recursive bool) ([]string, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	if len(rel) == 0 {
		return nil, &appError{Kind: "other", Message: "Pick the files or folders to ignore."}
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	changes, _, err := c.Status(a.ctx, o, wc, false)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	item := map[string]string{}
	for _, ch := range changes {
		item[ch.Path] = ch.Item
	}
	type rule struct{ dir, pattern string }
	var rules []rule
	for _, r := range rel {
		switch it, listed := item[r]; {
		case !listed:
			return nil, &appError{Kind: "other", Message: r + " isn't listed as a new file: it may be ignored already (svn ignores some, like *.o, by default)."}
		case it != "unversioned":
			return nil, &appError{Kind: "other", Message: r + " is already in the repository: ignoring only hides files that aren't. " +
				"To stop tracking it, delete it from svn first."}
		}
		name := path.Base(r)
		pattern := name
		if byExt {
			ext := path.Ext(name)
			if ext == "" || ext == name {
				return nil, &appError{Kind: "other", Message: name + " has no extension to ignore by."}
			}
			pattern = "*" + ext
		} else if strings.ContainsAny(name, "*?[]") {
			return nil, &appError{Kind: "other", Message: name + " has characters (* ? [ ]) that svn reads as a pattern: ignore by extension, or edit svn:ignore by hand."}
		}
		rule := rule{dir: path.Dir(r), pattern: pattern}
		if !slices.Contains(rules, rule) {
			rules = append(rules, rule)
		}
	}
	if recursive {
		// A rule that covers subfolders makes the same rule below it redundant:
		// keep one per pattern, on the folder that contains all the others.
		byPattern := map[string]string{}
		var order []string
		for _, r := range rules {
			d, ok := byPattern[r.pattern]
			if !ok {
				order = append(order, r.pattern)
				byPattern[r.pattern] = r.dir
				continue
			}
			byPattern[r.pattern] = commonDir(d, r.dir)
		}
		rules = rules[:0]
		for _, p := range order {
			rules = append(rules, rule{dir: byPattern[p], pattern: p})
		}
	}
	var folders []string
	for _, r := range rules {
		ok, err := c.AddIgnore(a.ctx, o, filepath.Join(wc, filepath.FromSlash(r.dir)), r.pattern, recursive)
		if err != nil {
			return folders, a.wrap(err, host)
		}
		if ok && !slices.Contains(folders, r.dir) {
			folders = append(folders, r.dir)
		}
	}
	if folders == nil {
		folders = []string{}
	}
	return folders, nil
}

// commonDir is the deepest folder containing both a and b (paths relative to
// the working copy, with /, "." for the top).
func commonDir(a, b string) string {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	var common []string
	for i := 0; i < len(as) && i < len(bs) && as[i] == bs[i]; i++ {
		common = append(common, as[i])
	}
	if len(common) == 0 {
		return "."
	}
	return strings.Join(common, "/")
}

// --- Relocate ---------------------------------------------------------------------

// Relocate points the working copy at its repository's new address (the
// server moved, or http became https): newRoot replaces the repository root.
// Saved repositories under the old root are rewritten too. It returns the
// working copy's new URL.
func (a *App) Relocate(wc, newRoot string) (string, error) {
	c, err := a.client()
	if err != nil {
		return "", err
	}
	newRoot = strings.TrimRight(strings.TrimSpace(newRoot), "/")
	u, err := url.Parse(newRoot)
	if err != nil || !slices.Contains([]string{"http", "https", "svn", "svn+ssh", "file"}, u.Scheme) {
		return "", &appError{Kind: "other", Message: "Give the repository's new address, starting with https://, http://, svn:// or svn+ssh://."}
	}
	host := a.wcHost(wc)
	info, err := c.Info(a.ctx, a.opts(host), wc)
	if err != nil {
		return "", a.wrap(err, host)
	}
	oldRoot := info.RepoRoot
	if newRoot == oldRoot {
		return "", &appError{Kind: "other", Message: "That's the address it already uses."}
	}
	newHost := hostOf(newRoot)
	if err := c.Relocate(a.ctx, a.opts(newHost), oldRoot, newRoot, wc); err != nil {
		if svn.IsOtherRepository(err) {
			return "", &appError{Kind: "other", Message: "The repository at " + newRoot + " isn't the same repository " +
				"(svn compares their IDs), so the working copy can't move there. Check the address.\n\n" + err.Error()}
		}
		return "", a.wrap(err, newHost)
	}
	_ = a.cfg.Update(func(c *config.Config) {
		for i, s := range c.Servers {
			if s.URL == oldRoot || strings.HasPrefix(s.URL, oldRoot+"/") {
				c.Servers[i].URL = newRoot + strings.TrimPrefix(s.URL, oldRoot)
			}
		}
	})
	info, err = a.Info(wc) // saves the new URL and root
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

// --- Incoming changes -----------------------------------------------------------

// Incoming counts the files that have newer versions on the server than in
// the working copy: what an Update would bring. It asks the server (svn
// status -u), so the window calls it in the background now and then.
func (a *App) Incoming(wc string) (int, error) {
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	host := a.wcHost(wc)
	changes, _, err := c.Status(a.ctx, a.opts(host), wc, true)
	if err != nil {
		return 0, a.wrap(err, host)
	}
	n := 0
	for _, ch := range changes {
		if ch.RepoItem != "" {
			n++
		}
	}
	return n, nil
}

// --- Merging ------------------------------------------------------------------------

// MaxMergeList caps how many not-yet-merged revisions the merge dialog lists.
const MaxMergeList = 300

// MergePlan is what merging src into the working copy would bring.
type MergePlan struct {
	Source    string         `json:"source"`
	Target    string         `json:"target"`    // the working copy's URL
	Revisions []svn.LogEntry `json:"revisions"` // not merged yet, newest first (up to MaxMergeList)
	Total     int            `json:"total"`     // how many there are in all
}

// MergePlan lists the revisions of src (a trunk, branch or tag URL, at the
// same subfolder as the working copy) that the working copy doesn't have yet.
func (a *App) MergePlan(wc, src string) (*MergePlan, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	info, err := c.Info(a.ctx, o, wc)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if strings.TrimRight(src, "/") == info.URL {
		return nil, &appError{Kind: "other", Message: "That's the branch this working copy is on."}
	}
	revs, err := c.Eligible(a.ctx, o, src, wc)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	p := &MergePlan{Source: src, Target: info.URL, Total: len(revs), Revisions: []svn.LogEntry{}}
	if len(revs) > MaxMergeList {
		revs = revs[len(revs)-MaxMergeList:] // the newest
	}
	if len(revs) > 0 {
		p.Revisions, err = c.LogRevisions(a.ctx, o, src, revs)
		if err != nil {
			return nil, a.wrap(err, host)
		}
	}
	return p, nil
}

// MergeInto merges src into the working copy, as local changes to check and
// commit: everything not merged yet (revs empty), or just revs
// (cherry-picking). The working copy must have no local changes, so the merge
// can be reviewed, and undone with Revert, on its own ("needclean"), and be
// at a single revision ("needupdate").
func (a *App) MergeInto(wc, src string, revs []int) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	changes, _, err := c.Status(a.ctx, o, wc, false)
	if err != nil {
		return a.wrap(err, host)
	}
	n := 0
	for _, ch := range changes {
		if ch.Item != "unversioned" && ch.Item != "external" && (ch.Item != "normal" || ch.Props == "modified" || ch.TreeConflict) {
			n++
		}
	}
	if n > 0 {
		what, them := fmt.Sprintf("%d uncommitted changes", n), "them"
		if n == 1 {
			what, them = "an uncommitted change", "it"
		}
		return &appError{Kind: "needclean", Message: "You have " + what + ". Commit or revert " + them + " first, " +
			"so the merge is the only change in your working copy: easy to check, and Revert undoes it cleanly."}
	}
	err = a.longOp(func(ctx context.Context, line func(string)) error {
		return c.MergeFrom(ctx, o, src, wc, revs, line)
	})
	switch svn.KindOf(err) {
	case svn.KindNeedUpdate:
		return &appError{Kind: "needupdate", Message: "Your working copy has files at different revisions " +
			"(normal after a commit): svn needs it updated before it can merge."}
	case svn.KindNeedClean:
		return &appError{Kind: "needclean", Message: "svn needs a working copy without local changes for this merge: commit or revert them first."}
	}
	return a.wrap(err, host)
}

// --- Changelists and properties ---------------------------------------------------

// SetChangelist puts the working copy files rel in changelist name (moving
// them out of any other), or with name "" takes them out of theirs. Only
// files that are in the repository (or added) can be in a changelist.
func (a *App) SetChangelist(wc string, rel []string, name string) error {
	if len(rel) == 0 {
		return &appError{Kind: "other", Message: "Pick the files first."}
	}
	if name != "" && !svn.ValidChangelistName(name) {
		return &appError{Kind: "other", Message: "A changelist name can't be empty, start or end with a space, or be longer than 60 characters."}
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	changes, _, err := c.Status(a.ctx, o, wc, false)
	if err != nil {
		return a.wrap(err, host)
	}
	item := map[string]string{}
	for _, ch := range changes {
		item[ch.Path] = ch.Item
	}
	for _, r := range rel {
		if st, err := os.Stat(filepath.Join(wc, filepath.FromSlash(r))); err == nil && st.IsDir() {
			return &appError{Kind: "other", Message: r + " is a folder: only files can be in a changelist."}
		}
		if it := item[r]; it == "unversioned" || it == "ignored" {
			return &appError{Kind: "other", Message: r + " isn't in svn yet: Add it first, then it can go in a changelist."}
		}
	}
	return a.wrap(c.SetChangelist(a.ctx, o, name, abs(wc, rel)...), host)
}

// PropsResult is a file's or folder's properties.
type PropsResult struct {
	Props []svn.Prop `json:"props"`
	IsDir bool       `json:"isDir"`
}

// Props lists the properties of a working copy file or folder (rel "." is
// the top folder), with local changes.
func (a *App) Props(wc, rel string) (*PropsResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(wc, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	props, err := c.ListProps(a.ctx, a.opts(host), p)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	return &PropsResult{Props: props, IsDir: st.IsDir()}, nil
}

var propNameRe = regexp.MustCompile(`^[A-Za-z_:][A-Za-z0-9_:.-]*$`)

// Properties svn manages itself, which aren't edited by hand.
var readOnlyProps = map[string]string{
	"svn:mergeinfo": "svn:mergeinfo records merges: it's changed by merging, not by hand.",
	"svn:special":   "svn:special marks symbolic links: svn sets it itself.",
}

// Properties svn only accepts on folders, or only on files.
var (
	folderProps = []string{"svn:ignore", "svn:global-ignores", "svn:externals", "svn:auto-props", "svn:inheritable-ignores"}
	fileProps   = []string{"svn:needs-lock", "svn:executable", "svn:eol-style", "svn:mime-type", "svn:keywords"}
	eolStyles   = []string{"native", "LF", "CRLF", "CR"}
)

// SetProps changes properties of a working copy file or folder: each name
// in set gets its value, and a value of "" removes the property. They are
// local changes, to be committed. What svn would refuse (svn:ignore on a
// file, an unknown svn:eol-style) is checked first, so a refusal doesn't
// leave only some of the changes made; svn checks the rest itself.
func (a *App) SetProps(wc, rel string, set map[string]string) error {
	p := filepath.Join(wc, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	names := slices.Sorted(maps.Keys(set))
	for _, n := range names {
		switch {
		case set[n] == "":
		case st.IsDir() && slices.Contains(fileProps, n):
			return &appError{Kind: "other", Message: n + " is for files: it can't be set on a folder."}
		case !st.IsDir() && slices.Contains(folderProps, n):
			return &appError{Kind: "other", Message: n + " is for folders: it can't be set on a file. Set it on the folder the file is in."}
		case n == "svn:eol-style" && !slices.Contains(eolStyles, set[n]):
			return &appError{Kind: "other", Message: "svn:eol-style can be native, LF, CRLF or CR."}
		}
		if !propNameRe.MatchString(n) {
			return &appError{Kind: "other", Message: fmt.Sprintf("%q isn't a valid property name: use letters, digits and _ : . - (starting with a letter, _ or :).", n)}
		}
		if why, ok := readOnlyProps[n]; ok {
			return &appError{Kind: "other", Message: why}
		}
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	if st.IsDir() {
		// svn only commits a property change on a folder that is up to date.
		if err := c.UpdateFolderOnly(a.ctx, o, p); err != nil {
			return a.wrap(err, host)
		}
	}
	for _, n := range names {
		if err := c.SetProp(a.ctx, o, n, set[n], p); err != nil {
			return a.wrap(err, host)
		}
	}
	return nil
}

// --- Comparing ---------------------------------------------------------------------

// atRev is u at revision rev (u@rev), or u itself (the latest) for rev 0.
func atRev(u string, rev int) string {
	if rev > 0 {
		return u + "@" + strconv.Itoa(rev)
	}
	return u
}

// Compare lists what differs from one place in the repository to another:
// URLs at revisions (0 for the latest), e.g. two tags, a branch and trunk,
// or one folder at two revisions.
func (a *App) Compare(fromURL string, fromRev int, toURL string, toRev int) ([]svn.Changed, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(fromURL)
	ch, err := c.DiffSummary(a.ctx, a.opts(host), atRev(fromURL, fromRev), atRev(toURL, toRev))
	return ch, a.wrap(err, host)
}

// CompareDiff is one changed path's diff in a Compare (path and item as it
// listed them).
func (a *App) CompareDiff(fromURL string, fromRev int, toURL string, toRev int, path, item string) (string, error) {
	c, err := a.client()
	if err != nil {
		return "", err
	}
	host := hostOf(fromURL)
	d, err := c.DiffPath(a.ctx, a.opts(host), atRev(fromURL, fromRev), atRev(toURL, toRev), path, item)
	return d, a.wrap(err, host)
}

// --- Patches -----------------------------------------------------------------------

// PatchSaved is what SavePatch wrote.
type PatchSaved struct {
	File    string   `json:"file"`
	Files   int      `json:"files"`   // how many paths are in it
	Skipped []string `json:"skipped"` // ticked paths that can't be in a patch yet (new, not added)
}

// SavePatch writes the local changes to rel (paths in wc) to file as a
// patch, which Apply patch (svn patch) applies to another working copy of
// the same folder. New files must be added (svn add) first; those that
// aren't are left out and listed. An existing file is only replaced with
// overwrite.
func (a *App) SavePatch(wc string, rel []string, file string, overwrite bool) (*PatchSaved, error) {
	if strings.TrimSpace(file) == "" {
		return nil, &appError{Kind: "other", Message: "Choose where to save the patch."}
	}
	if !filepath.IsAbs(file) {
		return nil, &appError{Kind: "other", Message: "Give the patch's full path (like the one Choose… fills in)."}
	}
	if st, err := os.Stat(file); err == nil {
		if st.IsDir() {
			return nil, &appError{Kind: "other", Message: file + " is a folder."}
		}
		if !overwrite {
			return nil, &appError{Kind: "exists", Message: file + " already exists."}
		}
	}
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	changes, _, err := c.Status(a.ctx, o, wc, false)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	item := map[string]string{}
	for _, ch := range changes {
		item[ch.Path] = ch.Item
	}
	res := &PatchSaved{File: file, Skipped: []string{}}
	var paths []string
	for _, r := range rel {
		switch item[r] {
		case "unversioned", "ignored", "missing":
			res.Skipped = append(res.Skipped, r)
		default:
			paths = append(paths, r)
		}
	}
	if len(paths) == 0 {
		return nil, &appError{Kind: "other", Message: "Nothing to put in a patch: new files need adding (Add) first."}
	}
	info, err := c.Info(a.ctx, o, wc)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	reposPath, err := url.PathUnescape(strings.TrimPrefix(info.RelativeURL, "^"))
	if err != nil {
		reposPath = strings.TrimPrefix(info.RelativeURL, "^")
	}
	patch, err := c.MakePatch(a.ctx, o, wc, reposPath, paths)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if strings.TrimSpace(patch) == "" {
		return nil, &appError{Kind: "other", Message: "Those files have no changes to put in a patch."}
	}
	if err := os.WriteFile(file, []byte(patch), 0o644); err != nil {
		return nil, err
	}
	res.Files = len(paths)
	return res, nil
}

// PickSaveFile asks where to save a file (a patch), starting with name in
// the home folder. "" when canceled.
func (a *App) PickSaveFile(title, name string) (string, error) {
	home, _ := os.UserHomeDir()
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: title, DefaultDirectory: home, DefaultFilename: name,
		Filters: []runtime.FileFilter{{DisplayName: "Patches (*.patch, *.diff)", Pattern: "*.patch;*.diff"}},
	})
}

// PickPatchFile asks for a patch file to open. "" when canceled.
func (a *App) PickPatchFile(title string) (string, error) {
	home, _ := os.UserHomeDir()
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title, DefaultDirectory: home,
		Filters: []runtime.FileFilter{
			{DisplayName: "Patches (*.patch, *.diff)", Pattern: "*.patch;*.diff"},
			{DisplayName: "All files", Pattern: "*"},
		},
	})
}

// ApplyPatch applies a patch file to the working copy wc, or with dryRun
// only says what it would do. Parts that don't fit are saved next to their
// file as <file>.svnpatch.rej.
func (a *App) ApplyPatch(wc, file string, dryRun bool) ([]svn.Patched, error) {
	if strings.TrimSpace(file) == "" {
		return nil, &appError{Kind: "other", Message: "Choose the patch file."}
	}
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	res, err := c.ApplyPatch(a.ctx, a.opts(host), wc, file, dryRun)
	if errors.Is(err, svn.ErrNotPatch) {
		return nil, &appError{Kind: "other", Message: "No changes found in " + filepath.Base(file) + ": is it a patch?"}
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, &appError{Kind: "other", Message: file + " doesn't exist."}
	}
	return res, a.wrap(err, host)
}
