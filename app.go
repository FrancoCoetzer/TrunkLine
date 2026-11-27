package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"

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

	updating sync.Mutex  // one app update at a time
	notifyOK bool        // the system offers desktop notifications
	pending  *cliRequest // the command line's action, until the page asks (PendingAction)
	noEvents bool        // tests: there's no window to send events to
}

// emit sends an event to the page.
func (a *App) emit(name string, data ...any) {
	if !a.noEvents {
		runtime.EventsEmit(a.ctx, name, data...)
	}
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
	// Desktop notifications (new commits): where the system doesn't offer
	// them, Notify just does nothing.
	a.notifyOK = runtime.InitializeNotifications(ctx) == nil
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
	Home          string               `json:"home"`         // the user's home folder, shown as ~
	UpdateCheck   bool                 `json:"updateCheck"`  // check for new releases automatically
	SettingsNote  string               `json:"settingsNote"` // the settings file couldn't be read: what happened
	Notify        bool                 `json:"notify"`       // notify about new commits
	Theme         string               `json:"theme"`        // "dark", "light" or "system"
	Font          string               `json:"font"`         // "mono" or "regular"
	Zoom          int                  `json:"zoom"`         // percent
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
		s.SettingsNote = c.Note
		s.Notify = !c.NoNotify
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
	// Kept by its long name on Windows: svn trips over short (8.3) ones.
	path = svn.LongPath(path)
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
