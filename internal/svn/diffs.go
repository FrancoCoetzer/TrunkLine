package svn

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Changed is one file or folder that differs between two places in the
// repository (svn diff --summarize).
type Changed struct {
	Path  string `json:"path"`  // relative to the compared folder, with /, "" for the folder itself
	Item  string `json:"item"`  // added, deleted, modified, none (only properties changed)
	Props string `json:"props"` // none, modified
	Kind  string `json:"kind"`  // file, dir
}

// DiffSummary lists what differs from old to new: two repository URLs, each
// with an optional peg revision (url@123), usually the same folder on two
// branches, two tags, or at two revisions.
func (c *Client) DiffSummary(ctx context.Context, o Opts, old, new string) ([]Changed, error) {
	out, err := c.run(ctx, o, nil, "diff", "--summarize", "--xml", "--old", old, "--new", new)
	if err != nil {
		return nil, err
	}
	var x struct {
		Paths []struct {
			Path  string `xml:",chardata"`
			Item  string `xml:"item,attr"`
			Props string `xml:"props,attr"`
			Kind  string `xml:"kind,attr"`
		} `xml:"paths>path"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("diff", err)
	}
	base := strings.TrimRight(stripPeg(old), "/")
	changed := []Changed{}
	for _, p := range x.Paths {
		rel := strings.TrimPrefix(strings.TrimPrefix(p.Path, base), "/")
		if u, err := url.PathUnescape(rel); err == nil {
			rel = u
		}
		changed = append(changed, Changed{Path: rel, Item: p.Item, Props: p.Props, Kind: p.Kind})
	}
	slices.SortFunc(changed, func(a, b Changed) int { return strings.Compare(a.Path, b.Path) })
	return changed, nil
}

// DiffBetween is the diff from old to new (repository URLs, with optional
// peg revisions), everything that differs.
func (c *Client) DiffBetween(ctx context.Context, o Opts, old, new string) (string, error) {
	return c.diffOldNew(ctx, o, old, new)
}

// DiffPath is the diff of one path from DiffSummary between old and new;
// item is its Item there. A file added or deleted between the two shows as
// all lines added or removed.
func (c *Client) DiffPath(ctx context.Context, o Opts, old, new, path, item string) (string, error) {
	switch item {
	case "added", "deleted":
		u := new
		if item == "deleted" {
			u = old
		}
		target := u // comparing a file: the path is ""
		if path != "" {
			target = stripPeg(u) + "/" + encodeURLPath(path) + u[len(stripPeg(u)):]
		}
		text, err := c.run(ctx, o, nil, "cat", "--", target)
		if err != nil {
			return "", err
		}
		return WholeFileDiff(text, path, item == "added"), nil
	}
	// --depth empty: a folder's own change (its properties), not everything
	// in it; for a file it changes nothing.
	if path == "" {
		return c.diffOldNew(ctx, o, old, new, "--depth", "empty")
	}
	return c.diffOldNew(ctx, o, old, new, "--depth", "empty", "--", path)
}

func (c *Client) diffOldNew(ctx context.Context, o Opts, old, new string, more ...string) (string, error) {
	args := append([]string{"diff", "--internal-diff", "-x", "-u", "--old", old, "--new", new}, more...)
	out, err := c.run(ctx, o, nil, args...)
	if err != nil {
		return "", err
	}
	return cutDiff(utf8Headers(out)), nil
}

// cutDiff is a diff as text, cut at MaxDiff.
func cutDiff(out []byte) string {
	if len(out) > MaxDiff {
		return string(out[:MaxDiff]) + "\n\n[diff cut at 4 MB]\n"
	}
	return string(out)
}

// The lines of a diff that name paths, which svn writes in the system's
// encoding (the rest is the files' own bytes).
var diffHeaderRe = regexp.MustCompile(`^(Index: |diff --git |--- |\+\+\+ |Property changes on: |copy from |copy to |rename from |rename to )`)

// utf8Headers makes the path lines of a diff UTF-8 (svn.exe on Windows
// writes them in the ANSI code page), leaving the files' contents alone.
func utf8Headers(diff []byte) []byte {
	if utf8.Valid(diff) {
		return diff
	}
	lines := bytes.SplitAfter(diff, []byte("\n"))
	for i, l := range lines {
		if diffHeaderRe.Match(l) && !utf8.Valid(l) {
			lines[i] = []byte(fromNative(l))
		}
	}
	return bytes.Join(lines, nil)
}

// nativeHeaders is a patch with its path lines in the system's encoding,
// as svn patch reads them (a UTF-8 patch, from TrunkLine or Linux, works on
// Windows). A path the system's encoding can't hold is an error.
func nativeHeaders(patch []byte) ([]byte, error) {
	lines := bytes.SplitAfter(patch, []byte("\n"))
	for i, l := range lines {
		if !diffHeaderRe.Match(l) || !utf8.Valid(l) || isASCII(l) {
			continue
		}
		b, ok := toNative(string(l))
		if !ok {
			return nil, fmt.Errorf("the patch names a file (%s) whose name this Windows can't pass to svn: its characters aren't in the system's code page",
				strings.TrimSpace(string(l)))
		}
		lines[i] = b
	}
	return bytes.Join(lines, nil), nil
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// WholeFileDiff shows a whole file as a diff that adds (or removes) every line.
func WholeFileDiff(b []byte, path string, added bool) string {
	what := "deleted"
	if added {
		what = "new"
	}
	switch {
	case len(b) > MaxDiff:
		return "(" + what + " file, too big to show)"
	case slices.Contains(b[:min(len(b), 8000)], 0):
		return "(" + what + " binary file)"
	case len(b) == 0:
		return "(" + what + " empty file)"
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), "\n")
	var sb strings.Builder
	sign, from, to, hunk := "+", "/dev/null", path, fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines))
	if !added {
		sign, from, to, hunk = "-", path, "/dev/null", fmt.Sprintf("@@ -1,%d +0,0 @@", len(lines))
	}
	fmt.Fprintf(&sb, "Index: %s (%s)\n===================================================================\n--- %s\n+++ %s\n%s\n", path, what, from, to, hunk)
	for _, l := range lines {
		sb.WriteString(sign + l + "\n")
	}
	return sb.String()
}

// stripPeg is a URL without its peg revision (url@123).
func stripPeg(u string) string {
	if i := strings.LastIndex(u, "@"); i > strings.LastIndex(u, "/") {
		return u[:i]
	}
	return u
}

// encodeURLPath escapes each segment of a relative path, for use under a URL.
func encodeURLPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// --- Patches ---------------------------------------------------------------------

// MakePatch is a patch of the local changes to paths (relative to the
// working copy wc, with /), in svn's git-style format: it carries new and
// deleted files, binary files and property changes, and svn patch applies it
// in any working copy of the same folder. reposPath is wc's path in the
// repository ("trunk", "branches/x/src"), which svn puts in front of every
// path and is taken out, so the patch's paths start at the working copy.
func (c *Client) MakePatch(ctx context.Context, o Opts, wc, reposPath string, paths []string) (string, error) {
	o.Dir = wc
	args := []string{"diff", "--git", "--internal-diff", "--depth", "empty", "--"}
	out, err := c.run(ctx, o, nil, append(args, paths...)...)
	if err != nil {
		return "", err
	}
	return relativePatch(string(utf8Headers(out)), reposPath), nil
}

// The lines of a git-style diff that name a path from the repository root.
var patchPathRe = regexp.MustCompile(`^(diff --git a/|--- a/|\+\+\+ b/|Property changes on: |copy from |copy to |rename from |rename to )(.*)$`)

// relativePatch takes prefix/ off the paths in a git-style diff's headers.
func relativePatch(diff, prefix string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return diff
	}
	lines := strings.SplitAfter(diff, "\n")
	for i, l := range lines {
		m := patchPathRe.FindStringSubmatch(strings.TrimRight(l, "\r\n"))
		if m == nil {
			continue
		}
		rest := m[2]
		if m[1] == "diff --git a/" { // a/<p> b/<p>
			rest = strings.Replace(strings.TrimPrefix(rest, prefix+"/"), " b/"+prefix+"/", " b/", 1)
		} else {
			rest = strings.TrimPrefix(rest, prefix+"/")
		}
		lines[i] = m[1] + rest + l[len(strings.TrimRight(l, "\r\n")):]
	}
	return strings.Join(lines, "")
}

// Patched is what svn patch did (or would do) to one path.
type Patched struct {
	Path     string   `json:"path"`     // relative to the working copy, with /
	Action   string   `json:"action"`   // added, deleted, updated, merged (already there), conflict, skipped
	Props    string   `json:"props"`    // "", or the same words for the properties
	Rejected bool     `json:"rejected"` // some changes didn't fit: they're in <path>.svnpatch.rej
	Notes    []string `json:"notes"`    // svn's notes on each part: applied with fuzz, rejected, already applied
}

// ErrNotPatch is returned when a file has nothing svn patch can apply.
var ErrNotPatch = errors.New("no changes found in that file: is it a patch?")

var patchActions = map[byte]string{'A': "added", 'D': "deleted", 'U': "updated", 'G': "merged", 'C': "conflict", ' ': ""}

// ApplyPatch applies the patch file to the working copy wc (dryRun: only
// says what it would do). Parts that don't fit are rejected: the file gets
// the rest, and the rejected parts go in <file>.svnpatch.rej.
func (c *Client) ApplyPatch(ctx context.Context, o Opts, wc, file string, dryRun bool) ([]Patched, error) {
	if st, err := os.Stat(file); err != nil {
		return nil, err
	} else if st.IsDir() {
		return nil, errors.New(file + " is a folder")
	}
	// svn patch reads the patch's paths in the system's encoding: on Windows,
	// a copy with them in the ANSI code page.
	text, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	native, err := nativeHeaders(text)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(native, text) {
		f, err := os.CreateTemp("", "trunkline-*.patch")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		_, err = f.Write(native)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		file = f.Name()
	}
	args := []string{"patch"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	out, err := c.run(ctx, o, nil, append(args, "--", file, wc)...)
	if err != nil {
		return nil, err
	}
	res := parsePatchOutput(out, wc)
	if len(res) == 0 {
		return nil, ErrNotPatch
	}
	return res, nil
}

func parsePatchOutput(out []byte, wc string) []Patched {
	var res []Patched
	for _, line := range bytes.Split(bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n")), []byte("\n")) {
		l := string(line)
		if !utf8.Valid(line) {
			l = fromNative(line) // svn.exe reports paths in the ANSI code page
		}
		switch {
		case strings.HasPrefix(l, ">"):
			if len(res) > 0 {
				n := strings.TrimSpace(l[1:])
				res[len(res)-1].Notes = append(res[len(res)-1].Notes, n)
				if strings.HasPrefix(n, "rejected") {
					res[len(res)-1].Rejected = true
				}
			}
		case strings.HasPrefix(l, "Skipped"):
			// Skipped missing target: 'path'
			p := l
			if i := strings.Index(l, "'"); i >= 0 {
				p = strings.TrimSuffix(l[i+1:], "'")
			}
			res = append(res, Patched{Path: rel(wc, p), Action: "skipped"})
		case len(l) > 3 && l[2] == ' ' && strings.ContainsRune("ADUGC ", rune(l[0])) && strings.ContainsRune("ADUGC ", rune(l[1])) && strings.TrimSpace(l[:2]) != "":
			p := strings.TrimSpace(l[2:])
			res = append(res, Patched{Path: rel(wc, p), Action: patchActions[l[0]], Props: patchActions[l[1]], Rejected: l[0] == 'C' || l[1] == 'C'})
		}
	}
	return res
}
