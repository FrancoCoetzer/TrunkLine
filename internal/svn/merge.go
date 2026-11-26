package svn

import (
	"context"
	"strconv"
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
