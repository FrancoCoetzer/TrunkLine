package main

// Committing and the Changes list's other actions: add, revert, resolve, clean
// up, ignore, changelists, and the merge editor's conflicts.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"trunkline/internal/svn"
)

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

// --- Merging a conflict by hand -----------------------------------------------------------

// MergeDoc is a conflicted file for the merge editor.
type MergeDoc struct {
	Chunks []svn.Chunk `json:"chunks"`
	EOL    string      `json:"eol"`   // the file's line ending
	Final  bool        `json:"final"` // it ends with one
}

// MergeConflict reads a working copy file with a text conflict into plain
// text and conflicts, for the merge editor.
func (a *App) MergeConflict(wc, rel string) (*MergeDoc, error) {
	p, err := inWC(wc, rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() || st.Size() > 16<<20 {
		return nil, &appError{Kind: "other", Message: rel + " can't be merged here (a folder, or over 16 MB): keep mine or take theirs."}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 || !utf8.Valid(b) {
		return nil, &appError{Kind: "other", Message: rel + " isn't a text file svn could mark: keep mine or take theirs."}
	}
	chunks, eol, final := svn.ParseConflicts(string(b))
	if !slices.ContainsFunc(chunks, func(c svn.Chunk) bool { return c.Conflict }) {
		return nil, &appError{Kind: "other", Message: rel + " has no conflict markers left: if it's as you want it, use \"I edited the file: mark resolved\"."}
	}
	return &MergeDoc{Chunks: chunks, EOL: eol, Final: final}, nil
}

// SaveMerge writes the merged text (lines joined with "\n"; written with
// the file's own line endings) to the conflicted file and marks it
// resolved. It refuses text that still has conflict markers.
func (a *App) SaveMerge(wc, rel, text, eol string) error {
	p, err := inWC(wc, rel)
	if err != nil {
		return err
	}
	if eol != "\r\n" {
		eol = "\n"
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", eol)
	if chunks, _, _ := svn.ParseConflicts(text); slices.ContainsFunc(chunks, func(c svn.Chunk) bool { return c.Conflict }) {
		return &appError{Kind: "other", Message: "The merged text still has conflict markers (<<<<<<<, =======, >>>>>>>): take them out first."}
	}
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(text), st.Mode().Perm()); err != nil {
		return err
	}
	return a.Resolve(wc, "working", []string{rel}, false)
}
