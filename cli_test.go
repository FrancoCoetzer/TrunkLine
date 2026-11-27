package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args   []string
		action string
		paths  []string
		err    error
	}{
		{nil, "", nil, nil},
		{[]string{"commit", "a.c", "b"}, "commit", []string{"a.c", "b"}, nil},
		{[]string{"log"}, "log", []string{"."}, nil},
		{[]string{"/home/me/wc"}, "open", []string{"/home/me/wc"}, nil},
		{[]string{"-assetdir", "frontend/dist", "-devserver", "localhost:34115"}, "", nil, nil},
		{[]string{"-assetdir=frontend/dist", "blame", "x.go"}, "blame", []string{"x.go"}, nil},
		{[]string{"commit", "--", "-odd name"}, "commit", []string{"-odd name"}, nil},
		{[]string{"open", "commit"}, "open", []string{"commit"}, nil},
		{[]string{"--help"}, "", nil, errHelp},
		{[]string{"help"}, "", nil, errHelp},
		{[]string{"-v"}, "", nil, errVersion},
	}
	for _, c := range cases {
		r, err := parseArgs(c.args)
		if !errors.Is(err, c.err) {
			t.Errorf("%q: err %v, want %v", c.args, err, c.err)
			continue
		}
		if c.action == "" {
			if r != nil {
				t.Errorf("%q: %+v, want no request", c.args, r)
			}
			continue
		}
		if r == nil || r.Action != c.action || !slices.Equal(r.Paths, c.paths) {
			t.Errorf("%q: %+v, want %s %q", c.args, r, c.action, c.paths)
		}
	}
	if _, err := parseArgs([]string{"--frobnicate"}); err == nil {
		t.Error("an unknown option was accepted")
	}
}

func TestResolve(t *testing.T) {
	a, wc := testApp(t)
	put(t, filepath.Join(wc, "src", "new.c")) // unversioned: still found
	other := t.TempDir()

	act := a.resolve(&cliRequest{Action: "commit", Paths: []string{"src/new.c", ".", other}}, wc)
	if act.Error != "" {
		t.Fatal(act.Error)
	}
	if act.WC != wc || !slices.Equal(act.Paths, []string{"src/new.c", ""}) || !slices.Equal(act.Skipped, []string{other}) {
		t.Errorf("got %+v", act)
	}
	// From inside a subfolder, and the working copy is now in the list.
	act = a.resolve(&cliRequest{Action: "log", Paths: []string{"new.c"}}, filepath.Join(wc, "src"))
	if act.WC != wc || !slices.Equal(act.Paths, []string{"src/new.c"}) {
		t.Errorf("from src: %+v", act)
	}
	if s := a.Startup(); len(s.WorkingCopies) != 1 || s.WorkingCopies[0].Path != wc {
		t.Errorf("working copies: %+v", s.WorkingCopies)
	}

	if act := a.resolve(&cliRequest{Action: "open", Paths: []string{"."}}, other); act.Error == "" {
		t.Errorf("outside a working copy: %+v", act)
	}
}

func TestPendingActionOnce(t *testing.T) {
	a, wc := testApp(t)
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(wc); err != nil {
		t.Fatal(err)
	}
	a.pending = &cliRequest{Action: "update", Paths: []string{"."}}
	if act := a.PendingAction(); act == nil || act.Action != "update" || act.WC != wc {
		t.Fatalf("first: %+v", act)
	}
	if act := a.PendingAction(); act != nil {
		t.Errorf("second: %+v", act)
	}
}
