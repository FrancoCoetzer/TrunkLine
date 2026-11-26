package svn

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Merge applies the change made in one revision of src to the working copy
// wc, as uncommitted local changes. A negative change undoes that revision
// (a reverse merge): Merge(src, -12, wc) takes r12's changes back out.
// Conflicts are left for the user (--accept postpone).
//
// svn refuses to merge into a working copy whose files are at different
// revisions (normal right after a commit): KindNeedUpdate.
func (c *Client) Merge(ctx context.Context, o Opts, src string, change int, wc string, line func(string)) error {
	return c.stream(ctx, o, line, "merge", "-c", strconv.Itoa(change), "--accept", "postpone", "--", src, wc)
}

// Eligible lists the revisions of src (a branch, tag or trunk URL) that
// haven't been merged into the working copy wc yet, oldest first. svn works
// this out from the svn:mergeinfo it records at each merge, so revisions
// merged before (in full or cherry-picked) aren't listed again.
func (c *Client) Eligible(ctx context.Context, o Opts, src, wc string) ([]int, error) {
	out, err := c.run(ctx, o, nil, "mergeinfo", "--show-revs", "eligible", "--", src, wc)
	if err != nil {
		return nil, err
	}
	revs := []int{}
	for _, l := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(strings.TrimPrefix(l, "r")); err == nil {
			revs = append(revs, n)
		}
	}
	return revs, nil
}

// LogRevisions is the log of exactly the revisions revs of target, newest
// first.
func (c *Client) LogRevisions(ctx context.Context, o Opts, target string, revs []int) ([]LogEntry, error) {
	if len(revs) == 0 {
		return []LogEntry{}, nil
	}
	args := []string{"log", "--xml", "-v"}
	for _, r := range revs {
		args = append(args, "-r", strconv.Itoa(r))
	}
	entries, err := c.log(ctx, o, args, target, 0)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b LogEntry) int { return b.Revision - a.Revision })
	return entries, nil
}

// MergeFrom merges src into the working copy wc, as local changes to check
// and commit. With no revs it's svn's automatic merge: everything of src not
// merged yet (keeping a branch up to date with trunk, or bringing a finished
// branch back). With revs it merges just those revisions (cherry-picking).
// Conflicts are left for the user. svn refuses when the working copy is at
// mixed revisions (KindNeedUpdate) or, for some merges, has local changes
// (KindNeedClean).
func (c *Client) MergeFrom(ctx context.Context, o Opts, src, wc string, revs []int, line func(string)) error {
	args := []string{"merge", "--accept", "postpone"}
	if len(revs) > 0 {
		var cs []string
		for _, r := range revs {
			cs = append(cs, strconv.Itoa(r))
		}
		args = append(args, "-c", strings.Join(cs, ","))
	}
	return c.stream(ctx, o, line, append(args, "--", src, wc)...)
}

var mergeRecordRe = regexp.MustCompile(`(?m)^(Added|Modified|Deleted): svn:mergeinfo\r?$`)

// MergeRecordChanged says whether the working copy path (a folder or file)
// has a local change to svn:mergeinfo: the record a merge leaves of which
// revisions it brought in. Committing that record without the merged changes
// themselves would tell svn they're merged when they aren't.
func (c *Client) MergeRecordChanged(ctx context.Context, o Opts, path string) (bool, error) {
	out, err := c.run(ctx, o, nil, "diff", "--properties-only", "--depth", "empty", "--", path)
	if err != nil {
		return false, err
	}
	return mergeRecordRe.Match(out), nil
}
