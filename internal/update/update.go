// Package update keeps TrunkLine at the latest release.
//
// A release is an annotated tag vMAJOR.MINOR.PATCH on main. Pushing it runs
// the GitHub Actions release workflow, which builds the installers and
// publishes them, with a SHA256SUMS file, as a GitHub release. How a running
// copy updates depends on how it was installed, which the build stamps into
// Install:
//
//   - "deb": installed from the .deb. Downloads the new .deb and installs it
//     with apt through pkexec (one password prompt).
//   - "windows": installed by the setup .exe into the user's folder.
//     Downloads the new setup and runs it silently; the setup closes the
//     app, replaces it and starts it again.
//   - "msi": installed from the .msi. For one user (a double-click: the
//     default), it downloads the new .msi, and a helper waits for the app
//     to close, runs msiexec and starts the app again. For all users (an
//     administrator's msiexec ALLUSERS=1), whoever installed it updates it:
//     it only says that a new release exists.
//   - "source" (or unstamped): built from a clone. It only says that a new
//     release exists; you pull and rebuild yourself.
package update

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Set by the Makefile with -ldflags -X trunkline/internal/update.<Var>=...
var (
	// Version is the release tag for a package ("v0.3.0"), or
	// `git describe --tags --dirty --always` for a source build.
	Version string
	// Install is how this copy was installed: "deb", "windows", "msi" or "source".
	Install string
	// Repo is the GitHub owner/name releases come from (a fork sets its own).
	Repo = "FrancoCoetzer/TrunkLine"
)

// Install kinds.
const (
	InstallDeb     = "deb"
	InstallWindows = "windows"
	InstallMSI     = "msi"
	InstallSource  = "source"
)

// InstallKind is Install, "source" when unstamped.
func InstallKind() string { return cmp.Or(Install, InstallSource) }

// Semver is a release version vMAJOR.MINOR.PATCH.
type Semver struct{ Major, Minor, Patch int }

func (v Semver) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Plain is the version without the "v", as the package file names use it.
func (v Semver) Plain() string { return strings.TrimPrefix(v.String(), "v") }

func (v Semver) Compare(o Semver) int {
	return cmp.Or(cmp.Compare(v.Major, o.Major), cmp.Compare(v.Minor, o.Minor), cmp.Compare(v.Patch, o.Patch))
}

// ParseTag parses an exact release tag "v1.2.3".
func ParseTag(s string) (Semver, bool) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return Semver{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Semver{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return Semver{}, false
		}
		n[i] = v
	}
	return Semver{n[0], n[1], n[2]}, true
}

// AssetName is the file a release carries for an install kind. The Makefile
// (make dist) names the files the same way.
func AssetName(install string, v Semver) string {
	switch install {
	case InstallDeb:
		return fmt.Sprintf("trunkline_%s_amd64.deb", v.Plain())
	case InstallWindows:
		return fmt.Sprintf("trunkline-%s-setup.exe", v.Plain())
	case InstallMSI:
		return fmt.Sprintf("trunkline-%s-x64.msi", v.Plain())
	}
	return ""
}

// SumsName is the checksum file every release carries.
const SumsName = "SHA256SUMS"

// Release is a published GitHub release.
type Release struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	URL        string  `json:"html_url"`
	Assets     []Asset `json:"assets"`
}

// Asset is a file attached to a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

func (r Release) asset(name string) (Asset, bool) {
	i := slices.IndexFunc(r.Assets, func(a Asset) bool { return a.Name == name })
	if i < 0 {
		return Asset{}, false
	}
	return r.Assets[i], true
}

// Summary is the first line of the release notes (or its name).
func (r Release) Summary() string {
	s := strings.TrimSpace(r.Body)
	if s == "" {
		s = r.Name
	}
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// GitHub reads a repository's public releases. Reads need no token.
type GitHub struct {
	API  string // https://api.github.com (a test server in tests)
	Repo string // owner/name
	HTTP *http.Client
}

// New is the client for Repo.
func New() *GitHub {
	return &GitHub{API: "https://api.github.com", Repo: Repo, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

// Releases lists the latest published releases.
func (g *GitHub) Releases(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.API+"/repos/"+g.Repo+"/releases?per_page=30", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rs []Release
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		return nil, fmt.Errorf("GitHub: reading the releases: %w", err)
	}
	return rs, nil
}

// get opens url, failing on anything but 200.
func (g *GitHub) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// download saves url to dst and returns its SHA-256 (hex).
func (g *GitHub) download(ctx context.Context, url, dst string) (string, error) {
	body, err := g.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer body.Close()
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseSums reads a sha256sum file: "<hex>  <name>" per line.
func parseSums(b []byte) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 {
			m[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return m
}

// Info is the outcome of a check.
type Info struct {
	Current    string   `json:"current"`    // the running version
	Install    string   `json:"install"`    // deb, windows, msi or source
	Latest     string   `json:"latest"`     // the newest release, "" if none
	Available  bool     `json:"available"`  // Latest is newer than Current
	CanInstall bool     `json:"canInstall"` // the app can install it itself (not a source build)
	Manual     string   `json:"manual"`     // when it can't: how this copy gets updated
	Notes      []string `json:"notes"`      // "v0.3.0: summary" for each newer release, newest first
	URL        string   `json:"url"`        // the newest release's page
	Note       string   `json:"note"`       // why updates are off, for a development build
}

// baseVersion is the release a version is, or is built from (a source
// build's describe output "v0.3.0-4-gabc1234-dirty" is based on v0.3.0).
func baseVersion(v string) (Semver, bool) {
	tag, _, _ := strings.Cut(v, "-")
	return ParseTag(tag)
}

// Check looks for a release newer than the running version.
func (g *GitHub) Check(ctx context.Context) (Info, error) {
	install := InstallKind()
	info := Info{Current: cmp.Or(Version, "dev"), Install: install}
	rs, err := g.Releases(ctx)
	if err != nil {
		return info, err
	}
	var newest []Release
	for _, r := range rs {
		if _, ok := ParseTag(r.Tag); ok && !r.Draft && !r.Prerelease {
			newest = append(newest, r)
		}
	}
	slices.SortFunc(newest, func(a, b Release) int {
		va, _ := ParseTag(a.Tag)
		vb, _ := ParseTag(b.Tag)
		return vb.Compare(va)
	})
	if len(newest) > 0 {
		info.Latest, info.URL = newest[0].Tag, newest[0].URL
	}
	base, ok := baseVersion(Version)
	if !ok {
		info.Note = "a development build without a release tag: it can't tell whether a release is newer"
		return info, nil
	}
	for _, r := range newest {
		v, _ := ParseTag(r.Tag)
		if v.Compare(base) <= 0 {
			break
		}
		if install != InstallSource {
			if _, ok := r.asset(AssetName(install, v)); !ok {
				continue // released without a file for this install (yet)
			}
		}
		if !info.Available {
			info.Available, info.Latest, info.URL = true, r.Tag, r.URL
		}
		info.Notes = append(info.Notes, r.Tag+": "+r.Summary())
	}
	switch {
	case install == InstallSource:
		info.Manual = "built from source: pull and rebuild to update"
	case install == InstallMSI && !perUserMSI():
		info.Manual = "installed for all users: get the new .msi from the release page, or ask whoever installed it"
	}
	info.CanInstall = info.Available && info.Manual == ""
	return info, nil
}

// Install downloads release tag's file for this install, checks it against
// the release's SHA256SUMS and installs it. It returns the program to start
// afterwards, or "" when the installer starts the app itself.
func (g *GitHub) Install(ctx context.Context, tag string, progress func(string)) (string, error) {
	install := InstallKind()
	v, ok := ParseTag(tag)
	if !ok {
		return "", fmt.Errorf("%q is not a release tag", tag)
	}
	if install == InstallSource {
		return "", errors.New("this copy was built from source: pull and rebuild it")
	}
	if install == InstallMSI && !perUserMSI() {
		return "", errors.New("this copy was installed for all users: whoever installed it updates it")
	}
	rs, err := g.Releases(ctx)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(rs, func(r Release) bool { return r.Tag == tag })
	if i < 0 {
		return "", fmt.Errorf("release %s not found", tag)
	}
	r := rs[i]
	name := AssetName(install, v)
	asset, ok := r.asset(name)
	if !ok {
		return "", fmt.Errorf("release %s has no %s", tag, name)
	}
	sumsAsset, ok := r.asset(SumsName)
	if !ok {
		return "", fmt.Errorf("release %s has no %s: not installing an unchecked file", tag, SumsName)
	}
	body, err := g.get(ctx, sumsAsset.URL)
	if err != nil {
		return "", err
	}
	sums, err := io.ReadAll(io.LimitReader(body, 64<<10))
	body.Close()
	if err != nil {
		return "", err
	}
	want := parseSums(sums)[name]
	if want == "" {
		return "", fmt.Errorf("%s of %s does not list %s", SumsName, tag, name)
	}

	// apt reads the .deb as its own unprivileged user, so the folder must be
	// readable by everyone; the setup goes in the user's cache.
	var dir string
	if install == InstallDeb {
		dir, err = os.MkdirTemp("", "trunkline-update-")
		if err == nil {
			err = os.Chmod(dir, 0o755)
		}
	} else {
		dir, err = cacheDir()
	}
	if err != nil {
		return "", err
	}
	file := filepath.Join(dir, name)
	progress(fmt.Sprintf("Downloading %s (%.1f MB)", name, float64(asset.Size)/1e6))
	got, err := g.download(ctx, asset.URL, file)
	if err == nil && got != want {
		err = fmt.Errorf("%s is corrupt (SHA-256 %s, the release says %s): not installed", name, got[:12], want[:12])
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	switch install {
	case InstallDeb:
		defer os.RemoveAll(dir)
		return installDeb(ctx, file, progress)
	case InstallWindows:
		// The setup closes this app, so the folder is removed on the next start (CleanupOld).
		progress("Installing (TrunkLine closes and starts again)")
		if err := exec.Command(file, "/S").Start(); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		return "", nil
	case InstallMSI:
		// The app must have closed before msiexec replaces it, so a helper
		// waits a little, runs msiexec (a small progress window; the same
		// one-user or all-users install as now) and starts the app again.
		exe, err := os.Executable()
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		progress("Installing (TrunkLine closes and starts again)")
		if err := startAfterExit(fmt.Sprintf(`ping -n 3 127.0.0.1 >nul & msiexec /i "%s" /passive /norestart & start "" "%s"`, file, exe)); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		return "", nil
	}
	return "", fmt.Errorf("no package update for install kind %q", install)
}

// installDeb installs the .deb with apt through pkexec (the desktop's
// password prompt), so new dependencies come along.
func installDeb(ctx context.Context, deb string, progress func(string)) (string, error) {
	if _, err := exec.LookPath("pkexec"); err != nil {
		return "", errors.New("pkexec is not installed: download the .deb from the release page and install it with apt")
	}
	progress("Installing (asks for your password)")
	out, err := exec.CommandContext(ctx, "pkexec", "/usr/bin/apt-get", "install", "-y", "--allow-downgrades", deb).CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127) {
			return "", errors.New("canceled: the password prompt was dismissed or refused")
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return "", fmt.Errorf("apt-get install failed: %v\n%s", err, strings.Join(lines[max(0, len(lines)-10):], "\n"))
	}
	return "/usr/bin/trunkline", nil
}

func cacheDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(cache, "trunkline"), 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(filepath.Join(cache, "trunkline"), "setup-")
}

// CleanupOld removes the setup an earlier Windows update ran.
func CleanupOld() {
	if cache, err := os.UserCacheDir(); err == nil {
		old, _ := filepath.Glob(filepath.Join(cache, "trunkline", "setup-*"))
		for _, d := range old {
			os.RemoveAll(d)
		}
	}
}

// Restart starts exe; the caller then quits.
func Restart(exe string) error {
	cmd := exec.Command(exe)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// perUserMSI says whether this copy was installed for one user: the .msi
// puts it in the user's own programs folder then, and in Program Files for
// all users.
func perUserMSI() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	local := os.Getenv("LOCALAPPDATA")
	return local != "" && strings.HasPrefix(strings.ToLower(exe), strings.ToLower(filepath.Join(local, "Programs"))+string(filepath.Separator))
}
