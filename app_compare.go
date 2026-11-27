package main

// Comparing revisions, branches and tags, and patches (save and apply).

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"trunkline/internal/svn"
)

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
