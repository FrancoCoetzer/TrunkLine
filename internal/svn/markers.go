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
