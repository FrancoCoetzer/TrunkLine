package svn

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"slices"
	"strings"
)

// GetProp is the value of property name on a working copy path, or "" when
// it isn't set.
func (c *Client) GetProp(ctx context.Context, o Opts, name, path string) (string, error) {
	out, err := c.run(ctx, o, nil, "propget", "--no-newline", name, "--", path)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.has([]string{"W200017"}) { // not set
			return "", nil
		}
		return "", err
	}
	// svn keeps svn:* properties with LF line endings but prints them with the
	// system's (CRLF on Windows): give the stored value.
	return strings.ReplaceAll(string(out), "\r\n", "\n"), nil
}

// SetProp sets property name on a working copy path (a local change, to be
// committed); an empty value removes it.
func (c *Client) SetProp(ctx context.Context, o Opts, name, value, path string) error {
	if value == "" {
		_, err := c.run(ctx, o, nil, "propdel", "-q", name, "--", path)
		return err
	}
	f, err := messageFile(value)
	if err != nil {
		return err
	}
	defer os.Remove(f)
	_, err = c.run(ctx, o, nil, "propset", "-q", name, "-F", f, "--", path)
	return err
}

// UpdateFolderOnly brings just the folder dir itself (its properties and
// revision) up to date, not the files or folders in it. svn only commits a
// property change on a folder that is up to date.
func (c *Client) UpdateFolderOnly(ctx context.Context, o Opts, dir string) error {
	_, err := c.run(ctx, o, nil, "update", "-q", "--depth", "empty", "--accept", "postpone", "--", dir)
	return err
}

// AddIgnore adds pattern to dir's svn:ignore (that folder only) or, with
// recursive, its svn:global-ignores (that folder and every folder below).
// It returns false when the pattern was already there.
func (c *Client) AddIgnore(ctx context.Context, o Opts, dir, pattern string, recursive bool) (bool, error) {
	prop := "svn:ignore"
	if recursive {
		prop = "svn:global-ignores"
	}
	// So the property change can be committed (and is made on the latest value).
	if err := c.UpdateFolderOnly(ctx, o, dir); err != nil {
		return false, err
	}
	cur, err := c.GetProp(ctx, o, prop, dir)
	if err != nil {
		return false, err
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(cur, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if slices.Contains(lines, pattern) {
		return false, nil
	}
	return true, c.SetProp(ctx, o, prop, strings.Join(append(lines, pattern), "\n")+"\n", dir)
}

// Relocate points the working copy wc at the same repository under a new
// address: every URL starting with from is rewritten to start with to (for
// example http://svn.old/repo to https://svn.new/repo). svn refuses when the
// repository at the new address isn't the same one (a different UUID).
func (c *Client) Relocate(ctx context.Context, o Opts, from, to, wc string) error {
	_, err := c.run(ctx, o, nil, "relocate", "--", from, to, wc)
	return err
}

// IsOtherRepository says whether err is svn refusing a relocate because the
// repository at the new address is a different one (E195009, a UUID mismatch).
func IsOtherRepository(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.has([]string{"E195009"})
}

// Prop is one property of a file or folder.
type Prop struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ListProps is every property set on a working copy path (as changed
// locally, not yet committed), sorted by name.
func (c *Client) ListProps(ctx context.Context, o Opts, path string) ([]Prop, error) {
	out, err := c.run(ctx, o, nil, "proplist", "-v", "--xml", "--", path)
	if err != nil {
		return nil, err
	}
	var x struct {
		Props []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:",chardata"`
		} `xml:"target>property"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("proplist", err)
	}
	props := []Prop{}
	for _, p := range x.Props {
		props = append(props, Prop{p.Name, strings.ReplaceAll(p.Value, "\r\n", "\n")})
	}
	slices.SortFunc(props, func(a, b Prop) int { return strings.Compare(a.Name, b.Name) })
	return props, nil
}
