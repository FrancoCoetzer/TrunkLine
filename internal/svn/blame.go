package svn

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// BlameLine is one line of a file with the revision that last changed it.
type BlameLine struct {
	Line int    `json:"line"` // 1-based
	Text string `json:"text"`
	Commit
}

// Blame limits: a bigger file is refused rather than sent to the window.
const (
	MaxBlameBytes = 8 << 20
	MaxBlameLines = 100000
)

// ErrBinary means svn treats the file as binary, so it has no lines to blame.
var ErrBinary = &Error{Kind: KindOther, Msg: "This is a binary file: it has no lines to blame."}

// Blame says, for each line of target, which revision last changed it.
// target is a working copy file or a repository URL, which may carry a peg
// revision (url@123).
//
// For a working copy file svn blames the file as it is on disk, local edits
// included: a line not committed yet has no revision (Revision 0). So the
// text must come from the same place as the blame: the file on disk for a
// working copy file, svn cat for a URL. (svn cat of a working copy file
// would give the committed version, which doesn't line up after an edit.)
func (c *Client) Blame(ctx context.Context, o Opts, target string) ([]BlameLine, error) {
	out, err := c.run(ctx, o, nil, "blame", "--xml", "--", target)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.has([]string{"E195012"}) { // binary
			return nil, ErrBinary
		}
		return nil, err
	}
	var x struct {
		Entries []struct {
			Line   string     `xml:"line-number,attr"`
			Commit *xmlCommit `xml:"commit"`
		} `xml:"target>entry"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("blame", err)
	}

	var text []byte
	if isURL(target) {
		text, err = c.run(ctx, o, nil, "cat", "--", target)
	} else {
		text, err = os.ReadFile(target)
	}
	if err != nil {
		return nil, err
	}
	if len(text) > MaxBlameBytes {
		return nil, &Error{Kind: KindOther, Msg: "This file is too big to blame here (over 8 MB)."}
	}
	if bytes.IndexByte(text[:min(len(text), 8000)], 0) >= 0 {
		return nil, ErrBinary
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n"), "\n")
	if len(text) == 0 {
		lines = nil
	}
	if len(lines) > MaxBlameLines {
		return nil, &Error{Kind: KindOther, Msg: "This file has too many lines to blame here."}
	}
	// Never show a line next to another line's revision: if the text and the
	// blame disagree (the file changed in between, unusual line endings),
	// say so instead.
	if len(lines) != len(x.Entries) {
		return nil, &Error{Kind: KindOther, Msg: "The file's lines didn't match svn's blame (did it change just now?). Try again."}
	}

	result := make([]BlameLine, len(lines))
	for i, l := range lines {
		if !utf8.ValidString(l) {
			l = strings.ToValidUTF8(l, "�")
		}
		result[i] = BlameLine{Line: i + 1, Text: l}
	}
	for _, e := range x.Entries {
		n, err := strconv.Atoi(e.Line)
		if err != nil || n < 1 || n > len(result) {
			continue
		}
		result[n-1].Commit = e.Commit.commit()
	}
	return result, nil
}

// isURL says whether target is a repository URL rather than a local path.
func isURL(target string) bool { return strings.Contains(target, "://") }
