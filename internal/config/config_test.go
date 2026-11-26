package config

import "testing"

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
