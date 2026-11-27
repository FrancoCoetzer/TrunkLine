package main

// Blame of a working copy file or a repository URL.

import (
	"path/filepath"
	"strings"

	"trunkline/internal/svn"
)

// --- Blame ----------------------------------------------------------------------

// BlameResult is a file's lines with who last changed each.
type BlameResult struct {
	Title    string          `json:"title"`    // the file, as ^/trunk/a.c (@rev for an older version)
	RepoRoot string          `json:"repoRoot"` // for looking up a revision's message (Log)
	Lines    []svn.BlameLine `json:"lines"`
}

// BlameFile blames a working copy file: its committed version, without the
// local edits.
func (a *App) BlameFile(wc, rel string) (*BlameResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	path := filepath.Join(wc, filepath.FromSlash(rel))
	info, err := c.Info(a.ctx, a.opts(host), path)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if info.Kind != "file" {
		return nil, &appError{Kind: "other", Message: rel + " is a folder: blame works on files."}
	}
	lines, err := c.Blame(a.ctx, a.opts(host), path)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	return &BlameResult{Title: info.RelativeURL, RepoRoot: info.RepoRoot, Lines: nonNil(lines)}, nil
}

// BlameURL blames a repository file, u (optionally url@rev).
func (a *App) BlameURL(u string) (*BlameResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	host := hostOf(u)
	info, err := c.Info(a.ctx, a.opts(host), u)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	if info.Kind != "file" {
		return nil, &appError{Kind: "other", Message: "That is a folder: blame works on files."}
	}
	lines, err := c.Blame(a.ctx, a.opts(host), u)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	title := info.RelativeURL
	if i := strings.LastIndex(u, "@"); i > strings.LastIndex(u, "/") {
		title += u[i:]
	}
	return &BlameResult{Title: title, RepoRoot: info.RepoRoot, Lines: nonNil(lines)}, nil
}

func nonNil(l []svn.BlameLine) []svn.BlameLine {
	if l == nil {
		return []svn.BlameLine{}
	}
	return l
}
