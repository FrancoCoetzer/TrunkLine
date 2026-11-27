package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Update(func(c *Config) {
		c.AddWorkingCopy(WorkingCopy{Name: "a", Path: "/x/a"})
		c.AddWorkingCopy(WorkingCopy{Name: "a2", Path: "/x/a/"}) // same path: replaces
		c.AddServer(Server{Name: "s", URL: "https://svn/repo"})
		c.Trust("svn:443")
		c.Trust("svn:443")
		c.Usernames["svn:443"] = "me"
	}); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.WorkingCopies) != 1 || c2.WorkingCopies[0].Name != "a2" || len(c2.Servers) != 1 ||
		len(c2.TrustedHosts) != 1 || !c2.Trusted("svn:443") || c2.Usernames["svn:443"] != "me" {
		t.Fatalf("reloaded = %+v", c2)
	}
	c2.RemoveWorkingCopy("/x/a")
	c2.RemoveServer("https://svn/repo")
	if len(c2.WorkingCopies) != 0 || len(c2.Servers) != 0 {
		t.Errorf("after remove = %+v", c2)
	}
}

func TestLoadBOMAndBroken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// With a byte order mark: read as usual.
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf{\"theme\": \"dark\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || c.Theme != "dark" || c.Note != "" {
		t.Fatalf("with a BOM: %+v, %v", c, err)
	}
	// Not JSON: kept aside, fresh settings, a note.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(dir)
	if err != nil || c.Note == "" || c.Theme != "" || c.WorkingCopies == nil {
		t.Fatalf("broken: %+v, %v", c, err)
	}
	kept, _ := filepath.Glob(path + ".broken-*")
	if len(kept) != 1 {
		t.Fatalf("kept files: %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); string(b) != "{not json" {
		t.Errorf("kept %q", b)
	}
	// It saves fresh settings where the old ones were.
	if err := c.Update(func(c *Config) { c.Theme = "light" }); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(dir); err != nil || c.Theme != "light" {
		t.Errorf("after saving: %+v, %v", c, err)
	}
}
