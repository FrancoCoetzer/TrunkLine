//go:build bench

package main

// The big-repository benchmark: make bench. It builds (once, cached in
// build/bench) a repository with BENCH_REVS revisions (default 100000) and a
// trunk of BENCH_FILES files (default 50000), checks trunk out, makes some
// local changes, and times the calls the window makes on it, as the window
// makes them. The numbers go to the test log as a table.
//
// The repository comes from a generated dump fed to svnadmin load: far
// faster than committing 100000 times. Every 50th revision changes a line
// of trunk/src/hot.c (1000 lines), so blame has 2000 revisions to walk;
// every 10000th makes a branch, every 20000th a tag.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"trunkline/internal/config"
	"trunkline/internal/svn"
)

func benchEnv(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}

const hotLines = 1000

// dumpWriter writes an svn dump (format 2, full texts).
type dumpWriter struct{ w *bufio.Writer }

func props(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, "K %d\n%s\nV %d\n%s\n", len(kv[i]), kv[i], len(kv[i+1]), kv[i+1])
	}
	b.WriteString("PROPS-END\n")
	return b.String()
}

func (d dumpWriter) revision(rev int, author, msg string, when time.Time) {
	p := props("svn:log", msg, "svn:author", author, "svn:date", when.UTC().Format("2006-01-02T15:04:05.000000Z"))
	fmt.Fprintf(d.w, "Revision-number: %d\nProp-content-length: %d\nContent-length: %d\n\n%s\n", rev, len(p), len(p), p)
}

func (d dumpWriter) dir(path string) {
	p := props()
	fmt.Fprintf(d.w, "Node-path: %s\nNode-kind: dir\nNode-action: add\nProp-content-length: %d\nContent-length: %d\n\n%s\n\n", path, len(p), len(p), p)
}

func (d dumpWriter) file(path, action, text string) {
	if action == "add" {
		p := props()
		fmt.Fprintf(d.w, "Node-path: %s\nNode-kind: file\nNode-action: add\nProp-content-length: %d\nText-content-length: %d\nContent-length: %d\n\n%s%s\n\n",
			path, len(p), len(text), len(p)+len(text), p, text)
		return
	}
	fmt.Fprintf(d.w, "Node-path: %s\nNode-kind: file\nNode-action: change\nText-content-length: %d\nContent-length: %d\n\n%s\n\n", path, len(text), len(text), text)
}

func (d dumpWriter) copy(path, from string, fromRev int) {
	fmt.Fprintf(d.w, "Node-path: %s\nNode-kind: dir\nNode-action: add\nNode-copyfrom-rev: %d\nNode-copyfrom-path: %s\n\n\n", path, fromRev, from)
}

func srcFile(i int) string { return fmt.Sprintf("trunk/src/d%03d/f%05d.c", i/100, i) }

func hotText(lines []string) string { return strings.Join(lines, "\n") + "\n" }

// writeDump writes the whole repository's history to w.
func writeDump(w io.Writer, revs, files int) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	d := dumpWriter{bw}
	authors := []string{"alice", "ben", "chen", "dana"}
	words := []string{"motor", "encoder", "speed", "PWM", "calibration", "housing", "driver", "docs"}
	start := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	when := func(r int) time.Time { return start.Add(time.Duration(r) * 15 * time.Minute) }

	fmt.Fprint(bw, "SVN-fs-dump-format-version: 2\n\n")
	d.revision(1, "alice", "Project layout and sources", when(1))
	for _, p := range []string{"trunk", "branches", "tags", "trunk/src"} {
		d.dir(p)
	}
	for i := 0; i < files; i++ {
		if i%100 == 0 {
			d.dir(filepath.ToSlash(filepath.Dir(srcFile(i))))
		}
		d.file(srcFile(i), "add", fmt.Sprintf("// file %d\n", i))
	}
	hot := make([]string, hotLines)
	for i := range hot {
		hot[i] = fmt.Sprintf("int line%d = %d;", i, i)
	}
	d.file("trunk/src/hot.c", "add", hotText(hot))

	for r := 2; r <= revs; r++ {
		author := authors[r%len(authors)]
		switch {
		case r%20000 == 0:
			d.revision(r, author, fmt.Sprintf("Tag v%d.0", r/20000), when(r))
			d.copy(fmt.Sprintf("tags/v%d.0", r/20000), "trunk", r-1)
		case r%10000 == 0:
			d.revision(r, author, fmt.Sprintf("Branch b%d for the %s work", r, words[r%len(words)]), when(r))
			d.copy(fmt.Sprintf("branches/b%d", r), "trunk", r-1)
		case r%50 == 0:
			k := (r / 50) % hotLines
			hot[k] = fmt.Sprintf("int line%d = %d; // r%d", k, r, r)
			d.revision(r, author, fmt.Sprintf("Tune %s in hot.c (line %d)", words[r%len(words)], k+1), when(r))
			d.file("trunk/src/hot.c", "change", hotText(hot))
		default:
			i := (r * 7919) % files
			d.revision(r, author, fmt.Sprintf("Fix %s in f%05d.c", words[r%len(words)], i), when(r))
			d.file(srcFile(i), "change", fmt.Sprintf("// file %d\n// changed in r%d\n", i, r))
		}
	}
	return bw.Flush()
}

// benchRepo makes (or reuses) the repository and returns its trunk URL.
func benchRepo(t *testing.T, dir string, revs, files int) string {
	repo := filepath.Join(dir, "repo")
	url := svn.FileURL(repo) + "/trunk"
	if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
		t.Logf("reusing %s", repo)
		return url
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("svnadmin", "create", repo).CombinedOutput(); err != nil {
		t.Fatalf("svnadmin create: %v\n%s", err, out)
	}
	began := time.Now()
	load := exec.Command("svnadmin", "load", "-q", repo)
	in, err := load.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	load.Stderr = &stderr
	if err := load.Start(); err != nil {
		t.Fatal(err)
	}
	werr := writeDump(in, revs, files)
	in.Close()
	if err := load.Wait(); err != nil || werr != nil {
		t.Fatalf("svnadmin load: %v %v\n%s", err, werr, stderr.String())
	}
	t.Logf("made the repository (%d revisions, %d files) in %s", revs, files, time.Since(began).Round(time.Second))
	return url
}

type timing struct {
	what        string
	first, best time.Duration
	note        string
}

func TestBigRepo(t *testing.T) {
	revs, files := benchEnv("BENCH_REVS", 100000), benchEnv("BENCH_FILES", 50000)
	c, err := svn.Find()
	if err != nil {
		t.Skip(err)
	}
	dir, _ := filepath.Abs(filepath.Join("build", "bench", fmt.Sprintf("%d-%d", revs, files)))
	url := benchRepo(t, dir, revs, files)
	ctx := context.Background()
	var times []timing

	wc := filepath.Join(dir, "wc")
	if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil {
		began := time.Now()
		if err := c.Checkout(ctx, svn.Opts{}, url, wc, nil); err != nil {
			t.Fatal(err)
		}
		times = append(times, timing{what: "Check out trunk (one time)", first: time.Since(began)})
		if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Local changes, the same every run: 100 modified files, 100 new ones.
	if out, err := exec.Command("svn", "revert", "-q", "-R", wc).CombinedOutput(); err != nil {
		t.Fatalf("revert: %v\n%s", err, out)
	}
	for i := 0; i < 100; i++ {
		f := filepath.Join(wc, filepath.FromSlash(strings.TrimPrefix(srcFile(i*(files/100)), "trunk/")))
		if err := os.WriteFile(f, []byte("// edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wc, "src", fmt.Sprintf("new%03d.c", i)), []byte("// new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := config.Load(filepath.Join(dir, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: ctx, cfg: cfg, svn: c, creds: map[string]svn.Creds{}, noEvents: true}
	if _, err := a.AddWorkingCopy(wc); err != nil {
		t.Fatal(err)
	}

	run := func(what string, f func() (string, error)) {
		tm := timing{what: what, best: time.Hour}
		for i := 0; i < 3; i++ {
			began := time.Now()
			note, err := f()
			d := time.Since(began)
			if err != nil {
				t.Errorf("%s: %v", what, err)
				return
			}
			if i == 0 {
				tm.first, tm.note = d, note
			}
			tm.best = min(tm.best, d)
		}
		times = append(times, tm)
	}
	n := func(k int, what string) string { return strconv.Itoa(k) + " " + what }

	run("Status (Changes tab)", func() (string, error) {
		s, err := a.Status(wc, false)
		if err != nil {
			return "", err
		}
		return n(len(s.Changes), "changes"), nil
	})
	run("Status with the server (Check server)", func() (string, error) {
		s, err := a.Status(wc, true)
		if err != nil {
			return "", err
		}
		return n(len(s.Changes), "changes"), nil
	})
	run("Incoming count (Update button)", func() (string, error) {
		k, err := a.Incoming(wc)
		return n(k, "incoming"), err
	})
	run("History: first page (100)", func() (string, error) {
		e, err := a.Log(url, 0, 100, "")
		return n(len(e), "entries"), err
	})
	run(fmt.Sprintf("History: a page %d back", revs/2), func() (string, error) {
		e, err := a.Log(url, revs/2, 100, "")
		return n(len(e), "entries"), err
	})
	run("History: search \"calibration\"", func() (string, error) {
		e, err := a.Log(url, 0, 200, "calibration")
		return n(len(e), "entries"), err
	})
	run("History of hot.c (first page)", func() (string, error) {
		e, err := a.Log(url+"/src/hot.c", 0, 100, "")
		return n(len(e), "entries"), err
	})
	run(fmt.Sprintf("Blame hot.c (%d lines, %d changes)", hotLines, revs/50), func() (string, error) {
		b, err := a.BlameFile(wc, "src/hot.c")
		if err != nil {
			return "", err
		}
		return n(len(b.Lines), "lines"), nil
	})
	run("Diff of a modified file", func() (string, error) {
		d, err := a.Diff(wc, strings.TrimPrefix(srcFile(0), "trunk/"))
		return n(len(d), "bytes"), err
	})
	run("Branches tab", func() (string, error) {
		b, err := a.Branches(wc)
		if err != nil {
			return "", err
		}
		return n(len(b.Branches), "branches and tags"), nil
	})
	run("Revision graph", func() (string, error) {
		g, err := a.RevisionGraph(wc)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d commits, %d lanes", len(g.Commits), len(g.Lanes)), nil
	})

	var b strings.Builder
	fmt.Fprintf(&b, "\n%d revisions, %d files, svn %s\n\n| What | First | Best of 3 | Result |\n|---|---:|---:|---|\n", revs, files, svnVersion(c))
	for _, tm := range times {
		best := "-"
		if tm.best != 0 && tm.best != time.Hour {
			best = tm.best.Round(time.Millisecond).String()
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", tm.what, tm.first.Round(time.Millisecond), best, tm.note)
	}
	t.Log(b.String())
}

func svnVersion(c *svn.Client) string {
	v, err := c.Version(context.Background())
	if err != nil {
		return "?"
	}
	return v
}
