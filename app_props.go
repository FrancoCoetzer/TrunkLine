package main

// Properties, and svn:externals as a table.

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"trunkline/internal/svn"
)

// PropsResult is a file's or folder's properties.
type PropsResult struct {
	Props []svn.Prop `json:"props"`
	IsDir bool       `json:"isDir"`
}

// Props lists the properties of a working copy file or folder (rel "." is
// the top folder), with local changes.
func (a *App) Props(wc, rel string) (*PropsResult, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(wc, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	host := a.wcHost(wc)
	props, err := c.ListProps(a.ctx, a.opts(host), p)
	if err != nil {
		return nil, a.wrap(err, host)
	}
	return &PropsResult{Props: props, IsDir: st.IsDir()}, nil
}

var propNameRe = regexp.MustCompile(`^[A-Za-z_:][A-Za-z0-9_:.-]*$`)

// Properties svn manages itself, which aren't edited by hand.
var readOnlyProps = map[string]string{
	"svn:mergeinfo": "svn:mergeinfo records merges: it's changed by merging, not by hand.",
	"svn:special":   "svn:special marks symbolic links: svn sets it itself.",
}

// Properties svn only accepts on folders, or only on files.
var (
	folderProps = []string{"svn:ignore", "svn:global-ignores", "svn:externals", "svn:auto-props", "svn:inheritable-ignores"}
	fileProps   = []string{"svn:needs-lock", "svn:executable", "svn:eol-style", "svn:mime-type", "svn:keywords"}
	eolStyles   = []string{"native", "LF", "CRLF", "CR"}
)

// SetProps changes properties of a working copy file or folder: each name
// in set gets its value, and a value of "" removes the property. They are
// local changes, to be committed. What svn would refuse (svn:ignore on a
// file, an unknown svn:eol-style) is checked first, so a refusal doesn't
// leave only some of the changes made; svn checks the rest itself.
func (a *App) SetProps(wc, rel string, set map[string]string) error {
	p := filepath.Join(wc, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	names := slices.Sorted(maps.Keys(set))
	for _, n := range names {
		switch {
		case set[n] == "":
		case st.IsDir() && slices.Contains(fileProps, n):
			return &appError{Kind: "other", Message: n + " is for files: it can't be set on a folder."}
		case !st.IsDir() && slices.Contains(folderProps, n):
			return &appError{Kind: "other", Message: n + " is for folders: it can't be set on a file. Set it on the folder the file is in."}
		case n == "svn:eol-style" && !slices.Contains(eolStyles, set[n]):
			return &appError{Kind: "other", Message: "svn:eol-style can be native, LF, CRLF or CR."}
		}
		if !propNameRe.MatchString(n) {
			return &appError{Kind: "other", Message: fmt.Sprintf("%q isn't a valid property name: use letters, digits and _ : . - (starting with a letter, _ or :).", n)}
		}
		if why, ok := readOnlyProps[n]; ok {
			return &appError{Kind: "other", Message: why}
		}
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	host := a.wcHost(wc)
	o := a.opts(host)
	if st.IsDir() {
		// svn only commits a property change on a folder that is up to date.
		if err := c.UpdateFolderOnly(a.ctx, o, p); err != nil {
			return a.wrap(err, host)
		}
	}
	for _, n := range names {
		if err := c.SetProp(a.ctx, o, n, set[n], p); err != nil {
			return a.wrap(err, host)
		}
	}
	return nil
}

// --- Externals ------------------------------------------------------------------------

// ExternalsResult is an svn:externals value read as rows, or OK false when
// it has something rows can't show (comments, an unknown form).
type ExternalsResult struct {
	Externals []svn.External `json:"externals"`
	OK        bool           `json:"ok"`
}

// ParseExternals reads an svn:externals value for the externals editor.
func (a *App) ParseExternals(value string) ExternalsResult {
	ext, ok := svn.ParseExternals(value)
	return ExternalsResult{ext, ok}
}

// FormatExternals writes rows from the externals editor as an
// svn:externals value. A row needs a URL and a folder.
func (a *App) FormatExternals(ext []svn.External) (string, error) {
	for i, e := range ext {
		e.URL, e.Dir, e.Rev = strings.TrimSpace(e.URL), strings.TrimSpace(e.Dir), strings.TrimSpace(e.Rev)
		if e.URL == "" || e.Dir == "" {
			return "", &appError{Kind: "other", Message: "Each external needs a URL and a folder name."}
		}
		if strings.ContainsAny(e.URL, " \t") {
			return "", &appError{Kind: "other", Message: "A URL can't have spaces: write them as %20."}
		}
		if e.Rev != "" && !regexp.MustCompile(`^[0-9]+$`).MatchString(e.Rev) {
			return "", &appError{Kind: "other", Message: "The revision is a number, or empty for the latest."}
		}
		ext[i] = e
	}
	return svn.FormatExternals(ext), nil
}
