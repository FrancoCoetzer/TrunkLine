package main

// Branches and tags: listing, creating, switching, deleting, undoing a
// commit, relocating, merging, and the revision graph.

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

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

// --- The revision graph ------------------------------------------------------------------

// GraphLimit is how many of the newest revisions the graph shows.
const GraphLimit = 300

// RevisionGraph is the revision graph of the working copy's project: its
// trunk and branches as lanes, their last GraphLimit commits, branch points,
// merges and tags.
func (a *App) RevisionGraph(wc string) (*svn.Graph, error) {
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
	l := svn.ParseLayout(info.URL)
	if l.Kind == "" {
		return nil, &appError{Kind: "other", Message: "This repository doesn't use trunk / branches / tags folders, so there are no branches to draw."}
	}
	prefix, err := url.PathUnescape(strings.TrimPrefix(l.Base, info.RepoRoot))
	if err != nil {
		prefix = strings.TrimPrefix(l.Base, info.RepoRoot)
	}
	entries, err := c.Log(a.ctx, o, l.Base, 0, GraphLimit)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	g := svn.BuildGraph(prefix, entries)
	// Merges: svn log -g of each lane (a deleted one as it was just before).
	for i, lane := range g.Lanes {
		if i >= 40 {
			break
		}
		segs := strings.Split(lane.Path, "/")
		for j, s := range segs {
			segs[j] = url.PathEscape(s)
		}
		u := info.RepoRoot + strings.Join(segs, "/")
		if lane.To > 0 {
			u += "@" + strconv.Itoa(lane.To-1)
		}
		merged, err := c.MergedInto(a.ctx, o, u, GraphLimit)
		if err != nil {
			continue // a lane outside the window, or gone: no merges to draw
		}
		g.AddMerges(prefix, lane.Path, merged)
	}
	return g, nil
}
