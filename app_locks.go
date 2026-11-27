package main

// Locks: the files that need one, who holds which, locking and unlocking.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"trunkline/internal/svn"
)

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
