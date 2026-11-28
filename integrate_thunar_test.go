package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The uca.xml Thunar 4.20 installs in /etc/xdg (shortened: one translation).
const systemUCA = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE actions [\n<!ELEMENT actions (action)+>\n<!ELEMENT action (icon | patterns | name | unique-id | command | description | directories | audio-files | image-files | other-files | text-files | video-files)*>\n<!ELEMENT icon (#PCDATA)>\n<!ELEMENT command (#PCDATA)>\n<!ELEMENT patterns (#PCDATA)>\n<!ELEMENT name (#PCDATA)>\n<!ATTLIST name xml:lang CDATA #IMPLIED>\n<!ELEMENT unique-id (#PCDATA)>\n<!ELEMENT description (#PCDATA)>\n<!ATTLIST description xml:lang CDATA #IMPLIED>\n<!ELEMENT startup-notify EMPTY>\n<!ELEMENT directories EMPTY>\n<!ELEMENT audio-files EMPTY>\n<!ELEMENT image-files EMPTY>\n<!ELEMENT other-files EMPTY>\n<!ELEMENT text-files EMPTY>\n<!ELEMENT video-files EMPTY>\n]>\n<!--\n  Copyright (c) 2005-2006 Benedikt Meurer <benny@xfce.org>\n\n  Default thunar-uca configuration file, which gets installed system-wide.\n  This should act more like an example, and is therefore kept simple.\n-->\n<actions>\n  <action>\n    <icon>utilities-terminal</icon>\n    <patterns>*</patterns>\n    <range/>\n    <name>Open Terminal Here</name>\n    <name xml:lang=\"am\">ተርሚናል እዚህ ክፈት</name>\n    <command>exo-open --working-directory %f --launch TerminalEmulator</command>\n    <description>Example for a custom action</description>\n    <description xml:lang=\"am\">የግል ተግባር ምሳሌ</description>\n    <startup-notify/>\n    <directories/>\n  </action>\n</actions>\n"

// One the user made in Thunar's settings, as Thunar writes it.
const userUCA = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<actions>\n<action>\n\t<icon>utilities-terminal</icon>\n\t<name>My terminal &amp; stuff</name>\n\t<submenu></submenu>\n\t<unique-id>1764300000000000-1</unique-id>\n\t<command>xfce4-terminal --working-directory %f</command>\n\t<description>mine</description>\n\t<range></range>\n\t<patterns>*</patterns>\n\t<startup-notify/>\n\t<directories/>\n</action>\n</actions>\n"

func thunarSetup(t *testing.T) (path string) {
	t.Helper()
	sys := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sys, "Thunar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "Thunar", "uca.xml"), []byte(systemUCA), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_DIRS", sys)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return thunarMenu(nil)[0].path
}

// ucaActions lists the actions in a uca.xml by unique-id (or name), and checks it parses.
func ucaActions(t *testing.T, b []byte) []string {
	t.Helper()
	var doc struct {
		Actions []struct {
			ID    string   `xml:"unique-id"`
			Names []string `xml:"name"` // the first one; then translations
		} `xml:"action"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not valid XML: %v\n%s", err, b)
	}
	var ids []string
	for _, a := range doc.Actions {
		if a.ID == "" {
			ids = append(ids, a.Names[0])
		} else {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

func integrateThunar(t *testing.T, args ...string) {
	t.Helper()
	saved := integrations
	defer func() { integrations = saved }()
	in := byName(t, saved, "Thunar (XFCE)")
	in.present = func() bool { return true }
	integrations = []integration{in}
	var out strings.Builder
	if code := runIntegrate(args, &out); code != 0 {
		t.Fatalf("integrate %v: exit %d: %s", args, code, out.String())
	}
}

func TestThunarNewFileKeepsSystemActions(t *testing.T) {
	p := thunarSetup(t)
	integrateThunar(t)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	ids := ucaActions(t, b)
	want := []string{"Open Terminal Here", "trunkline-commit", "trunkline-update", "trunkline-log", "trunkline-diff",
		"trunkline-blame", "trunkline-add", "trunkline-revert", "trunkline-lock", "trunkline-unlock", "trunkline-open"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("actions %q, want %q", ids, want)
	}
	if !strings.HasPrefix(string(b), systemUCA[:strings.LastIndex(systemUCA, "</actions>")]) {
		t.Error("the system file's part changed")
	}
	// --remove leaves the copy of the system file, unchanged.
	integrateThunar(t, "--remove")
	if b, _ := os.ReadFile(p); string(b) != systemUCA {
		t.Errorf("after --remove:\n%s", b)
	}
}

func TestThunarKeepsTheUsersActions(t *testing.T) {
	p := thunarSetup(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(userUCA), 0o644); err != nil {
		t.Fatal(err)
	}
	integrateThunar(t)
	first, _ := os.ReadFile(p)
	if ids := ucaActions(t, first); ids[0] != "1764300000000000-1" || len(ids) != 11 {
		t.Errorf("actions %q", ids)
	}
	if !strings.HasPrefix(string(first), userUCA[:strings.LastIndex(userUCA, "</actions>")]) {
		t.Error("the user's part changed")
	}
	integrateThunar(t) // again: the same file
	if again, _ := os.ReadFile(p); string(again) != string(first) {
		t.Errorf("not the same the second time:\n%s", again)
	}
	integrateThunar(t, "--remove")
	if b, _ := os.ReadFile(p); string(b) != userUCA {
		t.Errorf("after --remove:\n%s", b)
	}
}

func TestThunarLeavesABrokenFile(t *testing.T) {
	p := thunarSetup(t)
	broken := "<?xml version=\"1.0\"?>\n<actions>\n<action>\n\t<name>half"
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := integrations
	t.Cleanup(func() { integrations = saved })
	in := byName(t, saved, "Thunar (XFCE)")
	in.present = func() bool { return true }
	integrations = []integration{in}
	var out strings.Builder
	if code := runIntegrate(nil, &out); code == 0 || !strings.Contains(out.String(), "leaving it alone") {
		t.Errorf("exit %d: %s", code, out.String())
	}
	if b, _ := os.ReadFile(p); string(b) != broken {
		t.Errorf("changed to %q", b)
	}
}

func TestThunarCommand(t *testing.T) {
	s := thunarActions([]string{"/opt/it's here/trunkline"})
	if !strings.Contains(s, `<command>'/opt/it'\''s here/trunkline' commit -- %F</command>`) {
		t.Errorf("command not quoted for g_shell_parse_argv:\n%s", s)
	}
	for in, want := range map[string]string{"/usr/bin/trunkline": "/usr/bin/trunkline", "/a b": "'/a b'", "": "''", "/a$b": "'/a$b'"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
