package main

// The long operations (checkout, export, import, update), which stream svn's
// output to the page and can be cancelled.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

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
		a.emit(evOpDone)
	}()
	return f(ctx, func(l string) { a.emit(evOpLine, l) })
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
// depth is "" (everything) or svn.DepthImmediates (the top folder's files
// and empty subfolders, to choose more from later: SetFolders).
func (a *App) Checkout(u, dir, depth string) (*config.WorkingCopy, error) {
	if depth != "" && depth != svn.DepthImmediates {
		return nil, &appError{Kind: "other", Message: "Unknown checkout depth " + depth}
	}
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(u)
	err = a.longOp(func(ctx context.Context, line func(string)) error {
		if depth != "" {
			return c.CheckoutDepth(ctx, a.opts(host), u, dir, depth, line)
		}
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
