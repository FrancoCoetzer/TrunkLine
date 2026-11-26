package svn

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Commit is who last changed something, and when.
type Commit struct {
	Revision int       `json:"revision"`
	Author   string    `json:"author"`
	Date     time.Time `json:"date"`
}

type xmlCommit struct {
	Revision string `xml:"revision,attr"`
	Author   string `xml:"author"`
	Date     string `xml:"date"`
}

func (x *xmlCommit) commit() Commit {
	if x == nil {
		return Commit{}
	}
	c := Commit{Author: x.Author}
	c.Revision, _ = strconv.Atoi(x.Revision)
	c.Date, _ = time.Parse(time.RFC3339Nano, x.Date)
	return c
}

// Info describes a working copy or a URL.
type Info struct {
	Path        string `json:"path"`        // the working copy root ("" for a URL)
	Kind        string `json:"kind"`        // "dir" or "file"
	URL         string `json:"url"`         // where it lives in the repository
	RelativeURL string `json:"relativeUrl"` // the same, as ^/trunk
	RepoRoot    string `json:"repoRoot"`
	UUID        string `json:"uuid"`
	Revision    int    `json:"revision"` // the working copy's revision
	LastChange  Commit `json:"lastChange"`
}

// Info runs svn info on a working copy path or a URL.
func (c *Client) Info(ctx context.Context, o Opts, target string) (*Info, error) {
	out, err := c.run(ctx, o, nil, "info", "--xml", "--", target)
	if err != nil {
		return nil, err
	}
	var x struct {
		Entries []struct {
			Kind        string `xml:"kind,attr"`
			Revision    string `xml:"revision,attr"`
			URL         string `xml:"url"`
			RelativeURL string `xml:"relative-url"`
			Repo        struct {
				Root string `xml:"root"`
				UUID string `xml:"uuid"`
			} `xml:"repository"`
			WC struct {
				Root string `xml:"wcroot-abspath"`
			} `xml:"wc-info"`
			Commit *xmlCommit `xml:"commit"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(out, &x); err != nil || len(x.Entries) == 0 {
		return nil, errorf("info", err)
	}
	e := x.Entries[0]
	i := &Info{
		Path: filepath.FromSlash(e.WC.Root), Kind: e.Kind, URL: e.URL, RelativeURL: e.RelativeURL,
		RepoRoot: e.Repo.Root, UUID: e.Repo.UUID, LastChange: e.Commit.commit(),
	}
	i.Revision, _ = strconv.Atoi(e.Revision)
	return i, nil
}

// Change is one line of svn status.
type Change struct {
	Path         string `json:"path"`  // relative to the working copy root, with /
	Item         string `json:"item"`  // modified, added, deleted, unversioned, missing, conflicted, replaced, obstructed, ...
	Props        string `json:"props"` // none, normal, modified, conflicted
	IsDir        bool   `json:"isDir"`
	TreeConflict bool   `json:"treeConflict"`
	Copied       bool   `json:"copied"`
	Locked       bool   `json:"locked"` // the working copy is locked (needs cleanup)
	Changelist   string `json:"changelist,omitempty"`
	RepoItem     string `json:"repoItem,omitempty"` // with remote: what changed on the server
	Revision     int    `json:"revision"`
}

// Conflicted says whether the change needs resolving before a commit.
func (ch Change) Conflicted() bool {
	return ch.Item == "conflicted" || ch.Props == "conflicted" || ch.TreeConflict
}

// Status lists the working copy's changes: everything svn status shows,
// without the normal files. remote (svn status -u) also asks the server what
// an update would bring in, and is slower.
func (c *Client) Status(ctx context.Context, o Opts, wc string, remote bool) ([]Change, int, error) {
	args := []string{"status", "--xml"}
	if remote {
		args = append(args, "-u")
	}
	out, err := c.run(ctx, o, nil, append(args, "--", wc)...)
	if err != nil {
		return nil, 0, err
	}
	type entry struct {
		Path string `xml:"path,attr"`
		WC   struct {
			Item     string `xml:"item,attr"`
			Props    string `xml:"props,attr"`
			Revision string `xml:"revision,attr"`
			Tree     string `xml:"tree-conflicted,attr"`
			Copied   string `xml:"copied,attr"`
			Locked   string `xml:"wc-locked,attr"`
		} `xml:"wc-status"`
		Repo *struct {
			Item string `xml:"item,attr"`
		} `xml:"repos-status"`
	}
	var x struct {
		Targets []struct {
			Entries []entry `xml:"entry"`
			Against *struct {
				Revision string `xml:"revision,attr"`
			} `xml:"against"`
		} `xml:"target"`
		Changelists []struct {
			Name    string  `xml:"name,attr"`
			Entries []entry `xml:"entry"`
		} `xml:"changelist"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, 0, errorf("status", err)
	}
	root, _ := filepath.Abs(wc)
	head := 0
	var changes []Change
	add := func(e entry, cl string) {
		ch := Change{
			Path: rel(root, e.Path), Item: e.WC.Item, Props: e.WC.Props, Changelist: cl,
			TreeConflict: e.WC.Tree == "true", Copied: e.WC.Copied == "true", Locked: e.WC.Locked == "true",
		}
		ch.Revision, _ = strconv.Atoi(e.WC.Revision)
		if e.Repo != nil && e.Repo.Item != "none" {
			ch.RepoItem = e.Repo.Item
		}
		if ch.Item == "normal" && (ch.Props == "none" || ch.Props == "normal") && !ch.TreeConflict && ch.RepoItem == "" && !ch.Locked {
			return // only shown because -u was given and nothing differs
		}
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(ch.Path))); err == nil {
			ch.IsDir = st.IsDir()
		}
		changes = append(changes, ch)
	}
	for _, t := range x.Targets {
		for _, e := range t.Entries {
			add(e, "")
		}
		if t.Against != nil {
			head, _ = strconv.Atoi(t.Against.Revision)
		}
	}
	for _, cl := range x.Changelists {
		for _, e := range cl.Entries {
			add(e, cl.Name)
		}
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, head, nil
}

// rel is p relative to root, with forward slashes, or "." for the root.
func rel(root, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// LogEntry is one revision.
type LogEntry struct {
	Commit
	Message string        `json:"message"`
	Paths   []ChangedPath `json:"paths"`
}

// ChangedPath is one path a revision touched.
type ChangedPath struct {
	Path     string `json:"path"`   // repository path, /trunk/a.c
	Action   string `json:"action"` // A, M, D or R
	Kind     string `json:"kind"`   // file, dir or ""
	CopyFrom string `json:"copyFrom,omitempty"`
	CopyRev  int    `json:"copyRev,omitempty"`
}

// Log lists up to limit revisions of target (a working copy path or a URL),
// newest first, starting at revision from (0 for HEAD) and going back.
func (c *Client) Log(ctx context.Context, o Opts, target string, from, limit int) ([]LogEntry, error) {
	args := []string{"log", "--xml", "-v", "-l", strconv.Itoa(limit)}
	if from > 0 {
		args = append(args, "-r", strconv.Itoa(from)+":1")
	} else {
		args = append(args, "-r", "HEAD:1")
	}
	out, err := c.run(ctx, o, nil, append(args, "--", target)...)
	if err != nil {
		return nil, err
	}
	var x struct {
		Entries []struct {
			xmlCommit
			Msg   string `xml:"msg"`
			Paths []struct {
				Path     string `xml:",chardata"`
				Action   string `xml:"action,attr"`
				Kind     string `xml:"kind,attr"`
				CopyFrom string `xml:"copyfrom-path,attr"`
				CopyRev  string `xml:"copyfrom-rev,attr"`
			} `xml:"paths>path"`
		} `xml:"logentry"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("log", err)
	}
	entries := make([]LogEntry, 0, len(x.Entries))
	for _, e := range x.Entries {
		le := LogEntry{Commit: e.xmlCommit.commit(), Message: e.Msg, Paths: []ChangedPath{}}
		for _, p := range e.Paths {
			cp := ChangedPath{Path: p.Path, Action: p.Action, Kind: p.Kind, CopyFrom: p.CopyFrom}
			cp.CopyRev, _ = strconv.Atoi(p.CopyRev)
			le.Paths = append(le.Paths, cp)
		}
		slices.SortFunc(le.Paths, func(a, b ChangedPath) int { return strings.Compare(a.Path, b.Path) })
		entries = append(entries, le)
	}
	return entries, nil
}

// Entry is one item in a repository folder.
type Entry struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // file or dir
	Size   int64  `json:"size"`
	Commit Commit `json:"commit"`
}

// List lists a repository folder (svn list URL).
func (c *Client) List(ctx context.Context, o Opts, url string) ([]Entry, error) {
	out, err := c.run(ctx, o, nil, "list", "--xml", "--", url)
	if err != nil {
		return nil, err
	}
	var x struct {
		Entries []struct {
			Kind   string     `xml:"kind,attr"`
			Name   string     `xml:"name"`
			Size   int64      `xml:"size"`
			Commit *xmlCommit `xml:"commit"`
		} `xml:"list>entry"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("list", err)
	}
	entries := make([]Entry, 0, len(x.Entries))
	for _, e := range x.Entries {
		entries = append(entries, Entry{Name: e.Name, Kind: e.Kind, Size: e.Size, Commit: e.Commit.commit()})
	}
	// folders first, then by name
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Kind != b.Kind {
			if a.Kind == "dir" {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return entries, nil
}

// MaxDiff caps a diff's size; a bigger one is cut and says so.
const MaxDiff = 4 << 20

// Diff is the unified diff of a working copy path against its base
// (rev 0), or of what revision rev changed (target then a URL or path).
func (c *Client) Diff(ctx context.Context, o Opts, target string, rev int) (string, error) {
	args := []string{"diff", "--internal-diff", "-x", "-u"}
	if rev > 0 {
		args = append(args, "-c", strconv.Itoa(rev))
	}
	out, err := c.run(ctx, o, nil, append(args, "--", target)...)
	if err != nil {
		return "", err
	}
	if len(out) > MaxDiff {
		return string(out[:MaxDiff]) + "\n\n[diff cut at 4 MB]\n", nil
	}
	return string(out), nil
}

// Checkout checks url out into dir, reporting each line of svn's output.
func (c *Client) Checkout(ctx context.Context, o Opts, url, dir string, line func(string)) error {
	return c.stream(ctx, o, line, "checkout", "--", url, dir)
}

// CheckoutInPlace makes the existing folder dir a working copy of url
// (checkout --force): files already there become versioned files, and any
// that differ from the repository show as modified. Used right after
// importing dir, when they all match.
func (c *Client) CheckoutInPlace(ctx context.Context, o Opts, url, dir string, line func(string)) error {
	return c.stream(ctx, o, line, "checkout", "--force", "--", url, dir)
}

// Export copies url's files into dir with no .svn folder: a plain copy that
// is not a working copy. dir must not exist yet (svn creates it).
func (c *Client) Export(ctx context.Context, o Opts, url, dir string, line func(string)) error {
	return c.stream(ctx, o, line, "export", "--", url, dir)
}

// Import commits the contents of the local folder dir to url (created, with
// any missing parents) and returns the new revision. Files matching svn's
// global-ignores (*.o, *.pyc, ...) are left out, as with svn add. dir does
// not become a working copy.
func (c *Client) Import(ctx context.Context, o Opts, dir, url, message string, line func(string)) (int, error) {
	f, err := messageFile(message)
	if err != nil {
		return 0, err
	}
	defer os.Remove(f)
	rev := 0
	err = c.stream(ctx, o, func(l string) {
		if n := committedRevision(l); n > 0 {
			rev = n
		}
		if line != nil {
			line(l)
		}
	}, "import", "-F", f, "--encoding", "UTF-8", "--", dir, url)
	return rev, err
}

// messageFile writes a commit message to a temporary file for -F (so a
// message can be any text, including one starting with "-").
func messageFile(message string) (string, error) {
	f, err := os.CreateTemp("", "trunkline-msg-*.txt")
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(message)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// Update brings the working copy up to HEAD. Conflicts are left for the user
// (--accept postpone) rather than stopping the update.
func (c *Client) Update(ctx context.Context, o Opts, wc string, line func(string)) error {
	return c.stream(ctx, o, line, "update", "--accept", "postpone", "--", wc)
}

// Add schedules unversioned paths (folders with their contents, minus
// svn:ignore and global-ignores) for addition.
func (c *Client) Add(ctx context.Context, o Opts, paths ...string) error {
	_, err := c.run(ctx, o, nil, append([]string{"add", "--force", "--parents", "--"}, paths...)...)
	return err
}

// Delete schedules paths for deletion (also for files already gone from disk).
func (c *Client) Delete(ctx context.Context, o Opts, paths ...string) error {
	_, err := c.run(ctx, o, nil, append([]string{"delete", "--force", "--"}, paths...)...)
	return err
}

// Revert throws away local changes to paths.
func (c *Client) Revert(ctx context.Context, o Opts, paths ...string) error {
	_, err := c.run(ctx, o, nil, append([]string{"revert", "--depth", "infinity", "--"}, paths...)...)
	return err
}

// Resolve marks conflicts on paths resolved, keeping accept: "working" (the
// file as the user left it), "mine-full" or "theirs-full".
func (c *Client) Resolve(ctx context.Context, o Opts, accept string, paths ...string) error {
	_, err := c.run(ctx, o, nil, append([]string{"resolve", "--accept", accept, "--"}, paths...)...)
	return err
}

// Cleanup releases a working copy left locked by an interrupted operation.
func (c *Client) Cleanup(ctx context.Context, o Opts, wc string) error {
	_, err := c.run(ctx, o, nil, "cleanup", "--", wc)
	return err
}

// Commit commits exactly paths (--depth empty: a listed folder does not take
// its unlisted contents along) with message, and returns the new revision.
func (c *Client) Commit(ctx context.Context, o Opts, message string, paths []string) (int, error) {
	f, err := messageFile(message)
	if err != nil {
		return 0, err
	}
	defer os.Remove(f)
	args := []string{"commit", "--depth", "empty", "-F", f, "--encoding", "UTF-8", "--"}
	out, err := c.run(ctx, o, nil, append(args, paths...)...)
	if err != nil {
		return 0, err
	}
	return committedRevision(string(out)), nil
}

// committedRevision finds "Committed revision 42." in commit's output.
func committedRevision(out string) int {
	const p = "Committed revision "
	i := strings.LastIndex(out, p)
	if i < 0 {
		return 0
	}
	s := out[i+len(p):]
	s = s[:strings.IndexFunc(s+".", func(r rune) bool { return r < '0' || r > '9' })]
	n, _ := strconv.Atoi(s)
	return n
}
