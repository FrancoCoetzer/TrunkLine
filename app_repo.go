package main

// Changing the repository directly from the browser: new folder, move,
// delete, and copying a URL.

import (
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// --- Changing the repository directly ------------------------------------------------

// RepoMkdir makes a folder at URL u (and missing parents) in one commit.
func (a *App) RepoMkdir(u, message string) (int, error) {
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	host := hostOf(u)
	rev, err := c.MkdirURL(a.ctx, a.opts(host), u, message)
	return rev, a.wrap(err, host)
}

// RepoMove renames or moves from to to in the repository in one commit.
func (a *App) RepoMove(from, to, message string) (int, error) {
	if strings.TrimRight(from, "/") == strings.TrimRight(to, "/") {
		return 0, &appError{Kind: "other", Message: "The new name is the same as the old one."}
	}
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	host := hostOf(from)
	o := a.opts(host)
	if ok, err := c.Exists(a.ctx, o, to); err != nil {
		return 0, a.wrap(err, host)
	} else if ok {
		return 0, &appError{Kind: "other", Message: "Something with that name is already there."}
	}
	rev, err := c.MoveURL(a.ctx, o, from, to, message)
	return rev, a.wrap(err, host)
}

// RepoDelete deletes u from the repository in one commit (it stays in the
// history).
func (a *App) RepoDelete(u, message string) (int, error) {
	c, err := a.client()
	if err != nil {
		return 0, err
	}
	host := hostOf(u)
	rev, err := c.DeleteURL(a.ctx, a.opts(host), u, message)
	return rev, a.wrap(err, host)
}

// CopyText puts text on the clipboard.
func (a *App) CopyText(text string) error {
	return runtime.ClipboardSetText(a.ctx, text)
}
