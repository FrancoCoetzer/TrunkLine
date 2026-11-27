package main

// Files in a working copy: moving and renaming (and spotting renames done
// outside svn), and choosing folders in a sparse checkout.

import (
	"bytes"
	"context"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"trunkline/internal/svn"
)

// --- Renaming and moving --------------------------------------------------------------

// inWC is wc joined with rel (a path from the page, with /), refusing one
// that leads outside the working copy.
func inWC(wc, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return "", &appError{Kind: "other", Message: "Give a file or folder in the working copy."}
	}
	p := filepath.Join(wc, filepath.FromSlash(rel))
	r, err := filepath.Rel(wc, p)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", &appError{Kind: "other", Message: rel + " isn't inside this working copy."}
	}
	return p, nil
}

// MoveFile renames or moves a file or folder in the working copy (paths
// relative to it): svn move, so the history comes along, as do local
// changes. A new file (not in svn yet) is just renamed on disk.
func (a *App) MoveFile(wc, rel, newRel string) error {
	src, err := inWC(wc, rel)
	if err != nil {
		return err
	}
	dst, err := inWC(wc, newRel)
	if err != nil {
		return err
	}
	if src == dst {
		return &appError{Kind: "other", Message: "The new name is the same as the old one."}
	}
	if _, err := os.Stat(src); err != nil {
		return &appError{Kind: "other", Message: rel + " isn't there."}
	}
	// A change of letter case only is the same file on Windows: allow it.
	if _, err := os.Stat(dst); err == nil && !strings.EqualFold(src, dst) {
		return &appError{Kind: "other", Message: newRel + " already exists."}
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	changes, _, err := c.Status(a.ctx, a.opts(host), src, false)
	if err != nil {
		return a.wrap(err, host)
	}
	if len(changes) > 0 && changes[0].Path == "." && changes[0].Item == "unversioned" {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Rename(src, dst)
	}
	return a.wrap(c.Move(a.ctx, a.opts(host), src, dst), host)
}

// RenamePair is a file or folder that looks renamed or moved outside svn
// (in Explorer, say): From is missing, To is new.
type RenamePair struct {
	From string `json:"from"` // relative to the working copy, with /
	To   string `json:"to"`
	Dir  bool   `json:"dir"`
	Same bool   `json:"same"` // the content is unchanged (a file)
}

// RenameCandidates finds missing files and folders that seem to have been
// renamed or moved to a new one: a file with the same content as the
// missing one had, or else the only new file with the same name; a folder
// with the same names in it, or the only new folder with the same name.
// Recording them as moves (RecordRenames) keeps their history.
func (a *App) RenameCandidates(wc string) ([]RenamePair, error) {
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
	var missing []svn.Change
	var fresh []newItem
	for _, ch := range changes {
		switch ch.Item {
		case "missing":
			missing = append(missing, ch)
		case "unversioned":
			fresh = append(fresh, newItem{ch.Path, ch.IsDir})
			if ch.IsDir {
				// svn lists a new folder, not what's in it: look inside.
				fresh = append(fresh, walkNew(wc, ch.Path, 2000)...)
			}
		}
	}
	pairs := []RenamePair{}
	if len(missing) == 0 || len(fresh) == 0 || len(missing) > 500 {
		return pairs, nil
	}
	// What each missing folder had in it (svn lists its missing contents too).
	children := map[string]map[string]bool{}
	for _, m := range missing {
		if i := strings.LastIndex(m.Path, "/"); i > 0 {
			p := m.Path[:i]
			if children[p] == nil {
				children[p] = map[string]bool{}
			}
			children[p][m.Path[i+1:]] = true
		}
	}
	used := map[string]bool{}
	for _, m := range missing {
		// Missing things inside a missing folder go with the folder.
		if slices.ContainsFunc(pairs, func(p RenamePair) bool { return p.Dir && strings.HasPrefix(m.Path, p.From+"/") }) {
			continue
		}
		mDir := isDirInBase(a.ctx, c, o, filepath.Join(wc, filepath.FromSlash(m.Path)))
		var base []byte
		if !mDir {
			base, _ = c.Cat(a.ctx, o, filepath.Join(wc, filepath.FromSlash(m.Path)))
		}
		var byName []string
		found, same := "", false
		for _, f := range fresh {
			if used[f.path] || f.dir != mDir || slices.ContainsFunc(pairs, func(p RenamePair) bool { return p.Dir && strings.HasPrefix(f.path, p.To+"/") }) {
				continue
			}
			full := filepath.Join(wc, filepath.FromSlash(f.path))
			if !mDir && base != nil {
				if b, err := os.ReadFile(full); err == nil && bytes.Equal(b, base) {
					found, same = f.path, true
					break
				}
			}
			if mDir && len(children[m.Path]) > 0 && sameNames(full, children[m.Path]) {
				found = f.path
				break
			}
			if path.Base(f.path) == path.Base(m.Path) {
				byName = append(byName, f.path)
			}
		}
		if found == "" && len(byName) == 1 {
			found = byName[0]
		}
		if found != "" {
			used[found] = true
			pairs = append(pairs, RenamePair{From: m.Path, To: found, Dir: mDir, Same: same})
		}
	}
	return pairs, nil
}

// newItem is a file or folder not in svn yet (path relative to the working
// copy, with /).
type newItem struct {
	path string
	dir  bool
}

// walkNew lists what's in a new folder of the working copy (rel, with /),
// up to limit entries.
func walkNew(wc, rel string, limit int) []newItem {
	var out []newItem
	root := filepath.Join(wc, filepath.FromSlash(rel))
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if len(out) >= limit {
			return filepath.SkipAll
		}
		r, _ := filepath.Rel(wc, p)
		out = append(out, newItem{filepath.ToSlash(r), d.IsDir()})
		return nil
	})
	return out
}

// sameNames says whether folder dir holds exactly the names in want.
func sameNames(dir string, want map[string]bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(want) {
		return false
	}
	for _, e := range entries {
		if !want[e.Name()] {
			return false
		}
	}
	return true
}

// isDirInBase says whether a working copy path (maybe missing from disk) is
// a folder in svn.
func isDirInBase(ctx context.Context, c *svn.Client, o svn.Opts, p string) bool {
	info, err := c.Info(ctx, o, p)
	return err == nil && info.Kind == "dir"
}

// RecordRenames records files and folders renamed outside svn as svn moves:
// each one goes back to its old name for a moment, then svn moves it, so
// its history comes along (and its changes stay).
func (a *App) RecordRenames(wc string, pairs []RenamePair) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	for _, p := range pairs {
		from, err := inWC(wc, p.From)
		if err != nil {
			return err
		}
		to, err := inWC(wc, p.To)
		if err != nil {
			return err
		}
		if _, err := os.Stat(from); err == nil {
			return &appError{Kind: "other", Message: p.From + " is there again: not moving it."}
		}
		if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
			return err
		}
		if err := os.Rename(to, from); err != nil {
			return err
		}
		if err := c.Move(a.ctx, a.opts(host), from, to); err != nil {
			os.Rename(from, to) // put it back as the user left it
			return a.wrap(err, host)
		}
	}
	return nil
}

// --- Choosing folders (sparse working copies) ---------------------------------------------

// Folder is a subfolder in the repository, and how much of it this working
// copy has.
type Folder struct {
	Name  string `json:"name"`
	Depth string `json:"depth"` // svn's depth, or "" when the working copy doesn't have it
}

// Folders lists the repository's subfolders of the working copy folder rel
// ("" for the top), each with how much of it is checked out.
func (a *App) Folders(wc, rel string) ([]Folder, error) {
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
	u := info.URL
	dir := wc
	if rel != "" {
		if dir, err = inWC(wc, rel); err != nil {
			return nil, err
		}
		segs := strings.Split(rel, "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		u += "/" + strings.Join(segs, "/")
	}
	entries, err := c.List(a.ctx, o, u)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	depths := map[string]string{}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		if depths, err = c.Depths(a.ctx, o, dir); err != nil {
			return nil, a.wrap(err, host)
		}
	}
	folders := []Folder{}
	for _, e := range entries {
		if e.Kind == "dir" {
			folders = append(folders, Folder{Name: e.Name, Depth: depths[e.Name]})
		}
	}
	return folders, nil
}

// SetFolders fetches the folders in fetch (all of each) and removes those
// in remove from this working copy (they stay in the repository); paths
// relative to it. A folder with local changes isn't removed: commit or
// revert them first.
func (a *App) SetFolders(wc string, fetch, remove []string) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	for _, r := range remove {
		p, err := inWC(wc, r)
		if err != nil {
			return err
		}
		changes, _, err := c.Status(a.ctx, o, p, false)
		if err != nil {
			return a.wrap(err, host)
		}
		for _, ch := range changes {
			if ch.Item != "normal" && ch.Item != "external" || ch.Props == "modified" {
				return &appError{Kind: "other", Message: r + " has local changes (" + path.Join(r, ch.Path) + "): commit or revert them before removing it from this working copy."}
			}
		}
	}
	return a.wrap(a.longOp(func(ctx context.Context, line func(string)) error {
		for _, r := range remove {
			p, _ := inWC(wc, r)
			line("Removing " + r + " from this working copy")
			if err := c.SetDepth(ctx, o, p, svn.DepthExclude, line); err != nil {
				return err
			}
		}
		for _, f := range fetch {
			p, err := inWC(wc, f)
			if err != nil {
				return err
			}
			if err := c.SetDepth(ctx, o, p, svn.DepthInfinity, line); err != nil {
				return err
			}
		}
		return nil
	}), host)
}
