package svn

import (
	"context"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Layout is where a URL sits in the usual trunk/branches/tags layout:
//
//	<base>/trunk/<rest>
//	<base>/branches/<name>/<rest>
//	<base>/tags/<name>/<rest>
//
// Base is the project folder (the repository root, or a folder in it when
// one repository holds several projects). Rest is the subfolder that was
// checked out, if not the whole trunk or branch, so a switch can keep it.
type Layout struct {
	Base string `json:"base"` // project URL, without a trailing /
	Kind string `json:"kind"` // trunk, branch, tag, or "" when the URL isn't in such a layout
	Name string `json:"name"` // "trunk", or the branch or tag name (decoded)
	Rest string `json:"rest"` // "" or "/src/lib" (still URL-encoded)
}

// Kinds of a Layout.
const (
	KindTrunk  = "trunk"
	KindBranch = "branch"
	KindTag    = "tag"
)

// ParseLayout finds the trunk, branch or tag in u. The first "trunk",
// "branches/<name>" or "tags/<name>" in the path wins, so a project folder
// may sit anywhere below the repository root.
func ParseLayout(u string) Layout {
	u = strings.TrimRight(u, "/")
	p, err := url.Parse(u)
	if err != nil {
		return Layout{}
	}
	path := p.EscapedPath()
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	prefix := strings.TrimSuffix(u, path) // scheme://host
	join := func(s []string) string {
		if len(s) == 0 {
			return ""
		}
		return "/" + strings.Join(s, "/")
	}
	for i, s := range segs {
		switch {
		case s == "trunk":
			return Layout{Base: prefix + join(segs[:i]), Kind: KindTrunk, Name: "trunk", Rest: join(segs[i+1:])}
		case (s == "branches" || s == "tags") && i+1 < len(segs):
			kind := KindBranch
			if s == "tags" {
				kind = KindTag
			}
			name, err := url.PathUnescape(segs[i+1])
			if err != nil {
				name = segs[i+1]
			}
			return Layout{Base: prefix + join(segs[:i]), Kind: kind, Name: name, Rest: join(segs[i+2:])}
		}
	}
	return Layout{}
}

// URLOf is the URL of the trunk, a branch or a tag of the same project, at
// the same subfolder (Rest) as l.
func (l Layout) URLOf(kind, name string) string {
	switch kind {
	case KindTrunk:
		return l.Base + "/trunk" + l.Rest
	case KindBranch:
		return l.Base + "/branches/" + url.PathEscape(name) + l.Rest
	case KindTag:
		return l.Base + "/tags/" + url.PathEscape(name) + l.Rest
	}
	return ""
}

var branchNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ValidBranchName says whether name is safe as a branch or tag folder:
// letters, digits, ".", "_" and "-", starting with a letter or digit, no "..".
func ValidBranchName(name string) bool {
	return branchNameRe.MatchString(name) && !strings.Contains(name, "..") && !strings.HasSuffix(name, ".")
}

// Copy makes dst a copy of src in the repository, in one commit, and returns
// the new revision. src may carry a peg revision (url@123). Missing parent
// folders of dst are made. This is how svn makes branches and tags: the copy
// is cheap, and keeps src's history.
func (c *Client) Copy(ctx context.Context, o Opts, src, dst, message string) (int, error) {
	f, err := messageFile(message)
	if err != nil {
		return 0, err
	}
	defer os.Remove(f)
	out, err := c.run(ctx, o, nil, "copy", "--parents", "-F", f, "--encoding", "UTF-8", "--", src, dst)
	if err != nil {
		return 0, err
	}
	return committedRevision(string(out)), nil
}

// DeleteURL removes a folder from the repository in one commit (it stays in
// the history, and can be copied back from an older revision).
func (c *Client) DeleteURL(ctx context.Context, o Opts, u, message string) (int, error) {
	f, err := messageFile(message)
	if err != nil {
		return 0, err
	}
	defer os.Remove(f)
	out, err := c.run(ctx, o, nil, "delete", "-F", f, "--encoding", "UTF-8", "--", u)
	if err != nil {
		return 0, err
	}
	return committedRevision(string(out)), nil
}

// Switch moves the working copy wc to another branch, tag or trunk (u),
// keeping local changes. Conflicts with the incoming changes are left for
// the user (--accept postpone), as with Update.
func (c *Client) Switch(ctx context.Context, o Opts, wc, u string, line func(string)) error {
	return c.stream(ctx, o, line, "switch", "--accept", "postpone", "--", u, wc)
}

// Exists says whether u exists in the repository.
func (c *Client) Exists(ctx context.Context, o Opts, u string) (bool, error) {
	_, err := c.Info(ctx, o, u)
	if err == nil {
		return true, nil
	}
	// A missing path: "W170000: URL '...' non-existent in revision N".
	var e *Error
	if errors.As(err, &e) && e.has([]string{"W170000", "E170000"}) {
		return false, nil
	}
	return false, err
}
