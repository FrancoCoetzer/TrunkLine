package svn

import (
	"context"
	"encoding/xml"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A revision graph: the trunk and each branch as a lane, their commits, where
// each branch was copied from, merges between lanes, and tags as labels on
// the commit they were copied from.

// GraphLane is the trunk or a branch.
type GraphLane struct {
	Kind      string `json:"kind"` // trunk or branch
	Name      string `json:"name"`
	Path      string `json:"path"`      // its repository path, /branches/x
	From      int    `json:"from"`      // the revision it was made in (0: before the graph starts)
	To        int    `json:"to"`        // the revision it was deleted in, or 0
	Parent    int    `json:"parent"`    // the lane it was copied from, or -1
	ParentRev int    `json:"parentRev"` // the revision it was copied from
}

// GraphCommit is one commit, on one lane.
type GraphCommit struct {
	Rev     int       `json:"rev"`
	Lane    int       `json:"lane"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Message string    `json:"message"`
	Tags    []string  `json:"tags,omitempty"` // tags copied from this commit
}

// GraphMerge is a merge: lane ToLane's commit ToRev brought in FromLane's
// changes up to FromRev.
type GraphMerge struct {
	FromLane int `json:"fromLane"`
	FromRev  int `json:"fromRev"`
	ToLane   int `json:"toLane"`
	ToRev    int `json:"toRev"`
}

// Graph is a revision graph, commits newest first.
type Graph struct {
	Lanes   []GraphLane   `json:"lanes"`
	Commits []GraphCommit `json:"commits"`
	Merges  []GraphMerge  `json:"merges"`
}

// laneOf is the lane path a repository path is in (/trunk, /branches/x,
// /tags/x) under prefix (the project's path in the repository, "" for its
// root), with its kind, or "" when it's in none.
func laneOf(prefix, p string) (lane, kind string) {
	rest, ok := strings.CutPrefix(p, prefix+"/")
	if !ok {
		return "", ""
	}
	seg := strings.SplitN(rest, "/", 3)
	switch {
	case seg[0] == "trunk":
		return prefix + "/trunk", KindTrunk
	case (seg[0] == "branches" || seg[0] == "tags") && len(seg) >= 2 && seg[1] != "":
		kind = KindBranch
		if seg[0] == "tags" {
			kind = KindTag
		}
		return prefix + "/" + seg[0] + "/" + seg[1], kind
	}
	return "", ""
}

// BuildGraph makes the graph of a project (at repository path prefix) from
// its log (with changed paths, any order).
func BuildGraph(prefix string, entries []LogEntry) *Graph {
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b LogEntry) int { return a.Revision - b.Revision })
	g := &Graph{Lanes: []GraphLane{}, Commits: []GraphCommit{}, Merges: []GraphMerge{}}
	index := map[string]int{}
	lane := func(path, kind string) int {
		if i, ok := index[path]; ok {
			return i
		}
		name := path[strings.LastIndex(path, "/")+1:]
		g.Lanes = append(g.Lanes, GraphLane{Kind: kind, Name: name, Path: path, Parent: -1})
		index[path] = len(g.Lanes) - 1
		return len(g.Lanes) - 1
	}
	type tag struct {
		name, from string
		rev        int
	}
	var tags []tag
	for _, e := range entries {
		at := -1
		for _, p := range e.Paths {
			lp, kind := laneOf(prefix, p.Path)
			if lp == "" {
				continue
			}
			root := p.Path == lp
			if kind == KindTag {
				if root && (p.Action == "A" || p.Action == "R") && p.CopyFrom != "" {
					if from, _ := laneOf(prefix, p.CopyFrom); from != "" {
						tags = append(tags, tag{lp[strings.LastIndex(lp, "/")+1:], from, p.CopyRev})
					}
				}
				continue
			}
			i := lane(lp, kind)
			switch {
			case root && (p.Action == "A" || p.Action == "R"):
				g.Lanes[i].From, g.Lanes[i].To = e.Revision, 0
				if from, fk := laneOf(prefix, p.CopyFrom); from != "" && fk != KindTag {
					g.Lanes[i].Parent, g.Lanes[i].ParentRev = lane(from, fk), p.CopyRev
				}
			case root && p.Action == "D":
				g.Lanes[i].To = e.Revision
			}
			if at < 0 {
				at = i
			}
		}
		if at >= 0 {
			msg, _, _ := strings.Cut(strings.TrimSpace(e.Message), "\n")
			g.Commits = append(g.Commits, GraphCommit{Rev: e.Revision, Lane: at, Author: e.Author, Date: e.Date, Message: msg})
		}
	}
	// A tag goes on the newest commit of its lane at or before the revision it
	// was copied from.
	for _, t := range tags {
		li, ok := index[t.from]
		if !ok {
			continue
		}
		best := -1
		for j, c := range g.Commits {
			if c.Lane == li && c.Rev <= t.rev && (best < 0 || c.Rev > g.Commits[best].Rev) {
				best = j
			}
		}
		if best >= 0 {
			g.Commits[best].Tags = append(g.Commits[best].Tags, t.name)
		}
	}
	slices.Reverse(g.Commits)
	return g
}

// AddMerges adds the merges into lane (a lane path of g) that log -g found:
// merged maps each merge commit to the repository paths and revisions it
// brought in. One line per merge commit and source lane, from the newest
// revision merged from it.
func (g *Graph) AddMerges(prefix, lane string, merged map[int][]ChangedPath) {
	to := slices.IndexFunc(g.Lanes, func(l GraphLane) bool { return l.Path == lane })
	if to < 0 {
		return
	}
	for rev, paths := range merged {
		newest := map[int]int{}
		for _, p := range paths {
			lp, kind := laneOf(prefix, p.Path)
			from := slices.IndexFunc(g.Lanes, func(l GraphLane) bool { return l.Path == lp })
			if lp == "" || kind == KindTag || from < 0 || from == to {
				continue
			}
			// The copy that made the source lane isn't a change merged.
			if p.Path == lp && p.CopyFrom != "" {
				continue
			}
			newest[from] = max(newest[from], p.CopyRev)
		}
		for from, r := range newest {
			g.Merges = append(g.Merges, GraphMerge{FromLane: from, FromRev: r, ToLane: to, ToRev: rev})
		}
	}
	slices.SortFunc(g.Merges, func(a, b GraphMerge) int { return b.ToRev - a.ToRev })
}

// MergedInto runs svn log -g on url (a lane, with peg revision if it was
// deleted) and returns, for each of its last limit commits that merged
// something, the paths each merged revision changed; ChangedPath.CopyRev
// holds that revision.
func (c *Client) MergedInto(ctx context.Context, o Opts, url string, limit int) (map[int][]ChangedPath, error) {
	out, err := c.run(ctx, o, nil, "log", "-g", "-v", "--xml", "-l", strconv.Itoa(limit), "--", url)
	if err != nil {
		return nil, err
	}
	type entry struct {
		Revision string `xml:"revision,attr"`
		Reverse  string `xml:"reverse-merge,attr"`
		Paths    []struct {
			Path     string `xml:",chardata"`
			CopyFrom string `xml:"copyfrom-path,attr"`
		} `xml:"paths>path"`
		Merged []entry `xml:"logentry"`
	}
	var x struct {
		Entries []entry `xml:"logentry"`
	}
	if err := xml.Unmarshal(out, &x); err != nil {
		return nil, errorf("log", err)
	}
	res := map[int][]ChangedPath{}
	var walk func(es []entry) []ChangedPath
	walk = func(es []entry) []ChangedPath {
		var paths []ChangedPath
		for _, m := range es {
			if m.Reverse == "true" {
				continue
			}
			r, _ := strconv.Atoi(m.Revision)
			for _, p := range m.Paths {
				paths = append(paths, ChangedPath{Path: p.Path, CopyFrom: p.CopyFrom, CopyRev: r})
			}
			paths = append(paths, walk(m.Merged)...)
		}
		return paths
	}
	for _, e := range x.Entries {
		if len(e.Merged) == 0 {
			continue
		}
		r, _ := strconv.Atoi(e.Revision)
		res[r] = walk(e.Merged)
	}
	return res, nil
}
