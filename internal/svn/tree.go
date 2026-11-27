package svn

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

// --- Moving -----------------------------------------------------------------------

// Move renames or moves src to dst in a working copy (both paths in it),
// keeping the history: svn records dst as a copy of src. Local changes come
// along. dst's folder is put under svn first if it's new or missing: svn 1.14
// on Windows fails "move --parents" into a new folder (E155004: the working
// copy is locked, by its own move).
func (c *Client) Move(ctx context.Context, o Opts, src, dst string) error {
	src, dst = LongPath(src), LongPath(dst) // see LongPath: short names trip svn's locks
	if err := c.ensureFolder(ctx, o, filepath.Dir(dst)); err != nil {
		return err
	}
	_, err := c.run(ctx, o, nil, "move", "--", src, dst)
	return err
}

// ensureFolder puts dir (and new folders above it) under svn: svn mkdir when
// it isn't on disk, svn add of just the folder when it's there but new. A
// folder already in svn is left alone (add --force).
func (c *Client) ensureFolder(ctx context.Context, o Opts, dir string) error {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		_, err = c.run(ctx, o, nil, "add", "--depth", "empty", "--parents", "--force", "--", dir)
		return err
	}
	_, err := c.run(ctx, o, nil, "mkdir", "--parents", "--", dir)
	return err
}

// Cat is a working copy file's content as last updated or committed (its
// BASE): also for a file deleted from disk.
func (c *Client) Cat(ctx context.Context, o Opts, path string) ([]byte, error) {
	return c.run(ctx, o, nil, "cat", "-r", "BASE", "--", path)
}

// --- Changing the repository directly -------------------------------------------------

// MkdirURL makes folder u in the repository (and missing parents) in one
// commit, and returns the new revision.
func (c *Client) MkdirURL(ctx context.Context, o Opts, u, message string) (int, error) {
	f, err := messageFile(message)
	if err != nil {
		return 0, err
	}
	defer os.Remove(f)
	out, err := c.run(ctx, o, nil, "mkdir", "--parents", "-F", f, "--encoding", "UTF-8", "--", u)
	if err != nil {
		return 0, err
	}
	return committedRevision(string(out)), nil
}

// MoveURL renames or moves from to to in the repository in one commit (both
// URLs in the same repository), keeping the history, and returns the new
// revision.
func (c *Client) MoveURL(ctx context.Context, o Opts, from, to, message string) (int, error) {
	f, err := messageFile(message)
	if err != nil {
		return 0, err
	}
	defer os.Remove(f)
	out, err := c.run(ctx, o, nil, "move", "--parents", "-F", f, "--encoding", "UTF-8", "--", from, to)
	if err != nil {
		return 0, err
	}
	return committedRevision(string(out)), nil
}

// --- Sparse working copies ----------------------------------------------------------

// Depths of a working copy: what of a folder is checked out.
const (
	DepthInfinity   = "infinity"   // everything in it
	DepthImmediates = "immediates" // its files and (empty) subfolders
	DepthFiles      = "files"      // its files only
	DepthEmpty      = "empty"      // nothing in it yet
	DepthExclude    = "exclude"    // left out of the working copy on purpose
)

// Depths says, for the working copy folder dir and what's directly in it,
// how much is checked out: name -> depth ("" is dir itself). Folders the
// working copy doesn't have at all aren't listed.
func (c *Client) Depths(ctx context.Context, o Opts, dir string) (map[string]string, error) {
	out, err := c.run(ctx, o, nil, "info", "--xml", "--depth", "immediates", "--", dir)
	if err != nil {
		return nil, err
	}
	var x struct {
		Entries []struct {
			Path  string `xml:"path,attr"`
			Kind  string `xml:"kind,attr"`
			Depth string `xml:"wc-info>depth"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("info", err)
	}
	root, _ := filepath.Abs(dir)
	depths := map[string]string{}
	for _, e := range x.Entries {
		if e.Kind != "dir" {
			continue
		}
		name := rel(root, e.Path)
		if name == "." {
			name = ""
		}
		depths[name] = e.Depth
	}
	return depths, nil
}

// SetDepth checks out more or less of the working copy folder path:
// DepthInfinity fetches all of it (and the folders above it, if they're
// missing), DepthExclude removes it from this working copy (not from the
// repository). Conflicts are left for the user, as with Update.
func (c *Client) SetDepth(ctx context.Context, o Opts, path, depth string, line func(string)) error {
	args := []string{"update", "--set-depth", depth, "--accept", "postpone"}
	if depth != DepthExclude {
		args = append(args, "--parents")
	}
	return c.stream(ctx, o, line, append(args, "--", path)...)
}

// CheckoutDepth checks out url into dir with only depth of it (DepthEmpty,
// DepthFiles, DepthImmediates or DepthInfinity); SetDepth fetches more later.
func (c *Client) CheckoutDepth(ctx context.Context, o Opts, url, dir, depth string, line func(string)) error {
	return c.stream(ctx, o, line, "checkout", "--depth", depth, "--", url, dir)
}

// --- Externals ------------------------------------------------------------------------

// External is one line of an svn:externals property: the folder Dir (relative
// to the folder the property is on) is fetched from URL, at Rev ("" for the
// latest).
type External struct {
	URL string `json:"url"`
	Rev string `json:"rev"`
	Dir string `json:"dir"`
}

// ParseExternals reads an svn:externals value. It knows both formats svn
// accepts (URL first, with -r REV or URL@REV, and the old one with the
// folder first). ok is false when a line is something else, so the caller
// can leave the value to be edited as text; blank lines and # comments are
// left out.
func ParseExternals(value string) (ext []External, ok bool) {
	ext = []External{}
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return ext, false // comments would be lost
		}
		f, good := splitQuoted(line)
		if !good {
			return ext, false
		}
		var e External
		var rest []string
		for i := 0; i < len(f); i++ {
			switch {
			case f[i] == "-r" && i+1 < len(f):
				e.Rev = f[i+1]
				i++
			case strings.HasPrefix(f[i], "-r") && len(f[i]) > 2:
				e.Rev = f[i][2:]
			default:
				rest = append(rest, f[i])
			}
		}
		if len(rest) != 2 {
			return ext, false
		}
		if isExternalURL(rest[0]) {
			e.URL, e.Dir = rest[0], rest[1]
		} else if isExternalURL(rest[1]) {
			e.Dir, e.URL = rest[0], rest[1] // the old format: folder first
		} else {
			return ext, false
		}
		if i := strings.LastIndex(e.URL, "@"); i > strings.LastIndex(e.URL, "/") {
			if e.Rev == "" {
				e.Rev = e.URL[i+1:]
			}
			e.URL = e.URL[:i]
		}
		ext = append(ext, e)
	}
	return ext, true
}

// FormatExternals writes externals as an svn:externals value, in the
// current format: URL@REV (or URL) then the folder, quoted if it has spaces.
func FormatExternals(ext []External) string {
	var b strings.Builder
	for _, e := range ext {
		u := e.URL
		if e.Rev != "" {
			u += "@" + e.Rev
		}
		dir := e.Dir
		if strings.ContainsAny(dir, " \t") {
			dir = `"` + dir + `"`
		}
		b.WriteString(u + " " + dir + "\n")
	}
	return b.String()
}

// isExternalURL: an absolute URL, or one relative to the repository root
// (^/), the server (//, /), or the folder (../).
func isExternalURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "^/") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/")
}

// splitQuoted splits a line on spaces, keeping "quoted parts" together.
func splitQuoted(s string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	in, had := false, false
	for _, r := range s {
		switch {
		case r == '"':
			in, had = !in, true
		case (r == ' ' || r == '\t') && !in:
			if cur.Len() > 0 || had {
				out = append(out, cur.String())
				cur.Reset()
				had = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if in {
		return nil, false
	}
	if cur.Len() > 0 || had {
		out = append(out, cur.String())
	}
	return out, true
}
