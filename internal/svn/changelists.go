package svn

import (
	"context"
	"strings"
	"unicode"
)

// Changelists group local changes under a name, so they can be committed
// separately. They live only in this working copy (the server never sees
// them). Only files can be in one, and a file is in at most one. A commit
// takes the committed files out of their changelist; a revert doesn't.

// ValidChangelistName says whether name can be a changelist's name: not
// empty, at most 60 characters, no control characters.
func ValidChangelistName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || len([]rune(name)) > 60 {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// SetChangelist puts the files paths in changelist name (moving them out of
// any other), or with name "" takes them out of their changelist.
func (c *Client) SetChangelist(ctx context.Context, o Opts, name string, paths ...string) error {
	args := []string{"changelist", "-q"}
	if name == "" {
		args = append(args, "--remove", "--")
	} else {
		args = append(args, "--", name)
	}
	_, err := c.run(ctx, o, nil, append(args, paths...)...)
	return err
}
