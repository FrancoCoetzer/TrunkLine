package main

// Reading a working copy and a repository: info, status, diffs, the log,
// listings, and the incoming-changes count.

import (
	"os"
	"path/filepath"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

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
