package svn

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ConflictMarkers returns the line of the first conflict left in the file at
// path, or 0 when it has none. A conflict is what svn writes into a file it
// can't merge: a "<<<<<<<" line, later a "=======" line, later a ">>>>>>>"
// line, each at the start of a line and in that order (so a lone "======="
// under a Markdown heading doesn't count). Binary and very large files are
// skipped.
func ConflictMarkers(path string) (int, error) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > 64<<20 {
		return 0, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return 0, nil
	}
	const (
		none = iota
		sawStart
		sawMiddle
	)
	state, start := none, 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case isMarker(line, "<<<<<<<"):
			state, start = sawStart, n
		case state == sawStart && line == "=======":
			state = sawMiddle
		case state == sawMiddle && isMarker(line, ">>>>>>>"):
			return start, nil
		}
	}
	return 0, nil
}

// isMarker says whether line is marker alone or marker, a space and a label.
func isMarker(line, marker string) bool {
	rest, ok := strings.CutPrefix(line, marker)
	return ok && (rest == "" || rest[0] == ' ')
}

// FilesWithConflictMarkers checks each path (files, and every file under a
// folder, skipping .svn) and returns "path:line" for each file that still
// has conflict markers.
func FilesWithConflictMarkers(paths []string) []string {
	var found []string
	check := func(p string) {
		if n, err := ConflictMarkers(p); err == nil && n > 0 {
			found = append(found, p+":"+strconv.Itoa(n))
		}
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			check(p)
			continue
		}
		_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == ".svn" {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				check(q)
			}
			return nil
		})
	}
	return found
}

// Chunk is a piece of a conflicted file: plain text both sides agree on, or
// a conflict svn couldn't merge, with each side's lines.
type Chunk struct {
	Conflict    bool     `json:"conflict"`
	Text        []string `json:"text,omitempty"`        // plain text
	Mine        []string `json:"mine,omitempty"`        // the working copy's lines
	Base        []string `json:"base,omitempty"`        // the lines both started from, if svn wrote them
	Theirs      []string `json:"theirs,omitempty"`      // the incoming lines
	MineLabel   string   `json:"mineLabel,omitempty"`   // svn's label (".mine", ".working")
	TheirsLabel string   `json:"theirsLabel,omitempty"` // ".r42", ".merge-right.r7"
}

// ParseConflicts splits a file's text with svn's conflict markers into
// chunks:
//
//	<<<<<<< .mine
//	my lines
//	||||||| .r1       (the original lines; svn writes them)
//	their lines from the original
//	=======
//	their lines
//	>>>>>>> .r3
//
// Lines are without their line endings; eol is "\r\n" when the file uses
// them, else "\n", and final says whether the file ends with one. A marker
// left unclosed makes the rest plain text, as it is in the file.
func ParseConflicts(text string) (chunks []Chunk, eol string, final bool) {
	eol = "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	final = strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	chunks = []Chunk{}
	var plain []string
	flush := func() {
		if len(plain) > 0 {
			chunks = append(chunks, Chunk{Text: plain})
			plain = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		if !isMarker(lines[i], "<<<<<<<") {
			plain = append(plain, lines[i])
			continue
		}
		// Read to the closing marker; if there's none, it's plain text.
		c := Chunk{Conflict: true, MineLabel: strings.TrimSpace(strings.TrimPrefix(lines[i], "<<<<<<<"))}
		part, end := "mine", -1
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			switch {
			case part == "mine" && isMarker(l, "|||||||"):
				part = "base"
			case (part == "mine" || part == "base") && l == "=======":
				part = "theirs"
			case part == "theirs" && isMarker(l, ">>>>>>>"):
				c.TheirsLabel = strings.TrimSpace(strings.TrimPrefix(l, ">>>>>>>"))
				end = j
			case part == "mine":
				c.Mine = append(c.Mine, l)
			case part == "base":
				c.Base = append(c.Base, l)
			default:
				c.Theirs = append(c.Theirs, l)
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			plain = append(plain, lines[i])
			continue
		}
		flush()
		chunks = append(chunks, c)
		i = end
	}
	flush()
	return chunks, eol, final
}
