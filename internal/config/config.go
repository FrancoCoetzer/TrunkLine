// Package config keeps the app's settings: the working copies in the
// sidebar, the servers the user saved, and the servers whose certificate the
// user chose to trust. Passwords are never stored here.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// WorkingCopy is one entry in the sidebar.
type WorkingCopy struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	RepoRoot string `json:"repoRoot"`
}

// Server is a repository URL the user browses from.
type Server struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Config is the saved settings.
type Config struct {
	WorkingCopies []WorkingCopy     `json:"workingCopies"`
	Servers       []Server          `json:"servers"`
	TrustedHosts  []string          `json:"trustedHosts"`    // host[:port] whose certificate failures are accepted
	Usernames     map[string]string `json:"usernames"`       // host -> last username, to fill the login dialog
	NoUpdateCheck bool              `json:"noUpdateCheck"`   // don't look for new releases automatically
	Theme         string            `json:"theme,omitempty"` // "dark", "light", or "" to follow the system
	Font          string            `json:"font,omitempty"`  // "regular", or "" for monospace
	Zoom          int               `json:"zoom,omitempty"`  // the window's size in percent, or 0 for 100

	// Note says, when Load couldn't read the settings file, what it did
	// instead (for the app to show once).
	Note string `json:"-"`

	mu   sync.Mutex
	path string
}

// Dir is where the settings live: ~/.config/trunkline, %AppData%\trunkline.
func Dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "trunkline"), nil
}

// Load reads the settings from dir (empty settings if there are none yet).
func Load(dir string) (*Config, error) {
	c := &Config{path: filepath.Join(dir, "config.json")}
	b, err := os.ReadFile(c.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		// A byte order mark (Windows editors and PowerShell 5 write one) isn't JSON.
		b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
		if err := json.Unmarshal(b, c); err != nil {
			// Damaged settings mustn't stop the app from starting: keep the
			// file for the user, and start afresh.
			kept := c.path + ".broken-" + time.Now().Format("20060102-150405")
			if rerr := os.Rename(c.path, kept); rerr != nil {
				return nil, fmt.Errorf("%w (and keeping it aside failed: %v)", err, rerr)
			}
			*c = Config{path: c.path, Note: fmt.Sprintf("Your settings couldn't be read (%v), so TrunkLine started with fresh ones. The old file is kept as %s.", err, kept)}
		}
	}
	if c.WorkingCopies == nil {
		c.WorkingCopies = []WorkingCopy{}
	}
	if c.Servers == nil {
		c.Servers = []Server{}
	}
	if c.Usernames == nil {
		c.Usernames = map[string]string{}
	}
	return c, nil
}

// Update changes the settings under the lock and saves them.
func (c *Config) Update(f func(c *Config)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c)
	return c.save()
}

// Read runs f under the lock.
func (c *Config) Read(f func(c *Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c)
}

// save writes the file atomically (a temp file renamed over it).
func (c *Config) save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// AddWorkingCopy adds wc, or replaces the entry with the same path.
func (c *Config) AddWorkingCopy(wc WorkingCopy) {
	i := slices.IndexFunc(c.WorkingCopies, func(w WorkingCopy) bool { return samePath(w.Path, wc.Path) })
	if i >= 0 {
		c.WorkingCopies[i] = wc
	} else {
		c.WorkingCopies = append(c.WorkingCopies, wc)
	}
}

// RemoveWorkingCopy removes the entry for path (the files stay on disk).
func (c *Config) RemoveWorkingCopy(path string) {
	c.WorkingCopies = slices.DeleteFunc(c.WorkingCopies, func(w WorkingCopy) bool { return samePath(w.Path, path) })
}

// AddServer adds s, or renames the entry with the same URL.
func (c *Config) AddServer(s Server) {
	i := slices.IndexFunc(c.Servers, func(x Server) bool { return x.URL == s.URL })
	if i >= 0 {
		c.Servers[i] = s
	} else {
		c.Servers = append(c.Servers, s)
	}
}

// RemoveServer removes the entry for url.
func (c *Config) RemoveServer(url string) {
	c.Servers = slices.DeleteFunc(c.Servers, func(s Server) bool { return s.URL == url })
}

// Trusted says whether host's certificate failures are accepted.
func (c *Config) Trusted(host string) bool { return slices.Contains(c.TrustedHosts, host) }

// Trust accepts host's certificate failures from now on.
func (c *Config) Trust(host string) {
	if !c.Trusted(host) {
		c.TrustedHosts = append(c.TrustedHosts, host)
	}
}

func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
