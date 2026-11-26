package svn

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// LockInfo is a lock on a file: who holds it, why, and since when.
type LockInfo struct {
	Owner   string    `json:"owner"`
	Comment string    `json:"comment"`
	Created time.Time `json:"created"`
	token   string
}

type xmlLock struct {
	Token   string `xml:"token"`
	Owner   string `xml:"owner"`
	Comment string `xml:"comment"`
	Created string `xml:"created"`
}

func (x *xmlLock) lock() *LockInfo {
	if x == nil {
		return nil
	}
	l := &LockInfo{Owner: x.Owner, Comment: x.Comment, token: x.Token}
	l.Created, _ = time.Parse(time.RFC3339Nano, x.Created)
	return l
}

// Lock states of a working copy file, as svn status's lock column has them.
const (
	LockMine   = "mine"   // K: this working copy holds the lock
	LockOther  = "other"  // O: someone else (or another working copy) holds it
	LockStolen = "stolen" // T: this working copy held it, someone took it over
	LockBroken = "broken" // B: this working copy held it, someone removed it
)

// lockState works out a file's lock state from the lock token this working
// copy holds (wc) and, when the server was asked (remote), the lock the
// repository has (repo). It returns the state and the lock to show.
func lockState(wc, repo *LockInfo, remote bool) (string, *LockInfo) {
	switch {
	case !remote && wc != nil:
		return LockMine, wc
	case wc != nil && repo != nil && wc.token == repo.token:
		return LockMine, wc
	case wc != nil && repo != nil:
		return LockStolen, repo
	case wc != nil:
		return LockBroken, wc
	case repo != nil:
		return LockOther, repo
	}
	return "", nil
}

// Lock locks paths (working copy files) so only this working copy can commit
// them; for a svn:needs-lock file it also makes the file writable. steal
// takes over a lock someone else holds. When some paths are already locked
// by someone else (and not steal), the others are still locked and the
// error is KindLockConflict.
func (c *Client) Lock(ctx context.Context, o Opts, comment string, steal bool, paths ...string) error {
	f, err := messageFile(comment)
	if err != nil {
		return err
	}
	defer os.Remove(f)
	args := []string{"lock", "-F", f, "--encoding", "UTF-8"}
	if steal {
		args = append(args, "--force")
	}
	_, err = c.run(ctx, o, nil, append(append(args, "--"), paths...)...)
	return err
}

// Unlock releases locks on paths; breakOthers also removes a lock someone
// else holds (or one this working copy has no token for).
func (c *Client) Unlock(ctx context.Context, o Opts, breakOthers bool, paths ...string) error {
	args := []string{"unlock"}
	if breakOthers {
		args = append(args, "--force")
	}
	_, err := c.run(ctx, o, nil, append(append(args, "--"), paths...)...)
	return err
}

// NeedsLock lists the files under wc that have svn:needs-lock (relative to
// wc, with /): files that are read-only until locked, usually ones that can't
// be merged, like CAD models or Office documents.
func (c *Client) NeedsLock(ctx context.Context, o Opts, wc string) ([]string, error) {
	out, err := c.run(ctx, o, nil, "propget", "-R", "--xml", "svn:needs-lock", "--", wc)
	if err != nil {
		return nil, err
	}
	var x struct {
		Targets []struct {
			Path string `xml:"path,attr"`
		} `xml:"target"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("propget", err)
	}
	root, _ := filepath.Abs(wc)
	paths := []string{}
	for _, t := range x.Targets {
		paths = append(paths, rel(root, t.Path))
	}
	return paths, nil
}

var lockedByRe = regexp.MustCompile(`Path '([^']*)' is already locked by user '([^']*)'`)

// LockedBy lists, from a failed Lock's error, the repository paths that
// someone else had locked and who: [path, owner] pairs.
func LockedBy(err error) [][2]string {
	var e *Error
	if !errors.As(err, &e) {
		return nil
	}
	var out [][2]string
	for _, m := range lockedByRe.FindAllStringSubmatch(e.Msg, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}
