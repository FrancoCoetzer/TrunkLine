package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub serves releases and their files. files maps a file name to its
// content; each release lists the files named in its assets.
func fakeGitHub(t *testing.T, releases []Release, files map[string]string) *GitHub {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/me/app/releases" {
			out := make([]Release, len(releases))
			for i, rel := range releases {
				out[i] = rel
				out[i].Assets = nil
				for _, a := range rel.Assets {
					a.URL = srv.URL + "/dl/" + rel.Tag + "/" + a.Name
					a.Size = int64(len(files[a.Name]))
					out[i].Assets = append(out[i].Assets, a)
				}
			}
			json.NewEncoder(w).Encode(out)
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/dl/"); ok {
			_, name, _ = strings.Cut(name, "/")
			if s, ok := files[name]; ok {
				w.Write([]byte(s))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return &GitHub{API: srv.URL, Repo: "me/app", HTTP: srv.Client()}
}

func stamp(t *testing.T, version, install string) {
	t.Helper()
	v, i := Version, Install
	Version, Install = version, install
	t.Cleanup(func() { Version, Install = v, i })
}

func assets(names ...string) []Asset {
	var a []Asset
	for _, n := range names {
		a = append(a, Asset{Name: n})
	}
	return a
}

var releases = []Release{
	{Tag: "v0.1.0", Body: "First", Assets: assets("trunkline_0.1.0_amd64.deb", "trunkline-0.1.0-setup.exe", SumsName)},
	{Tag: "v0.3.0", Body: "Blame view\n\nMore text", Assets: assets("trunkline_0.3.0_amd64.deb", SumsName)}, // no Windows file
	{Tag: "v0.2.0", Body: "Branches", Assets: assets("trunkline_0.2.0_amd64.deb", "trunkline-0.2.0-setup.exe", SumsName)},
	{Tag: "v0.4.0-rc1", Prerelease: true, Assets: assets("trunkline_0.4.0_amd64.deb")},
	{Tag: "v9.0.0", Draft: true},
}

func TestCheck(t *testing.T) {
	g := fakeGitHub(t, releases, nil)
	ctx := context.Background()

	stamp(t, "v0.1.0", InstallDeb)
	info, err := g.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || !info.CanInstall || info.Latest != "v0.3.0" || len(info.Notes) != 2 ||
		info.Notes[0] != "v0.3.0: Blame view" || info.Notes[1] != "v0.2.0: Branches" {
		t.Errorf("deb: %+v", info)
	}

	// Windows skips v0.3.0, which has no setup.
	stamp(t, "v0.1.0", InstallWindows)
	if info, _ := g.Check(ctx); info.Latest != "v0.2.0" || len(info.Notes) != 1 {
		t.Errorf("windows: %+v", info)
	}

	stamp(t, "v0.3.0", InstallDeb)
	if info, _ := g.Check(ctx); info.Available || info.Latest != "v0.3.0" {
		t.Errorf("up to date: %+v", info)
	}

	// A source build is told, but can't install.
	stamp(t, "v0.2.0-5-gabc1234-dirty", "")
	if info, _ := g.Check(ctx); !info.Available || info.CanInstall || info.Latest != "v0.3.0" {
		t.Errorf("source: %+v", info)
	}

	stamp(t, "", "")
	if info, _ := g.Check(ctx); info.Available || info.Note == "" || info.Latest != "v0.3.0" {
		t.Errorf("unstamped: %+v", info)
	}
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestInstallRefusesBadFiles(t *testing.T) {
	ctx := context.Background()
	stamp(t, "v0.1.0", InstallDeb)
	deb := "trunkline_0.2.0_amd64.deb"
	none := func(string) {}

	// The file doesn't match its checksum.
	g := fakeGitHub(t, releases, map[string]string{deb: "tampered", SumsName: sum("original") + "  " + deb + "\n"})
	if _, err := g.Install(ctx, "v0.2.0", none); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("tampered file: %v", err)
	}

	// The checksum file doesn't list it.
	g = fakeGitHub(t, releases, map[string]string{deb: "x", SumsName: sum("x") + "  other.deb\n"})
	if _, err := g.Install(ctx, "v0.2.0", none); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Errorf("unlisted file: %v", err)
	}

	// No checksum file at all.
	noSums := []Release{{Tag: "v0.2.0", Assets: assets(deb)}}
	g = fakeGitHub(t, noSums, map[string]string{deb: "x"})
	if _, err := g.Install(ctx, "v0.2.0", none); err == nil || !strings.Contains(err.Error(), "unchecked") {
		t.Errorf("no SHA256SUMS: %v", err)
	}

	// A source build never installs.
	stamp(t, "v0.1.0", InstallSource)
	if _, err := g.Install(ctx, "v0.2.0", none); err == nil {
		t.Error("source build installed")
	}
}

func TestDownloadChecksum(t *testing.T) {
	g := fakeGitHub(t, releases, map[string]string{"f": "hello"})
	dst := t.TempDir() + "/f"
	got, err := g.download(context.Background(), g.API+"/dl/v0.1.0/f", dst)
	if err != nil || got != sum("hello") {
		t.Errorf("download: %s, %v", got, err)
	}
	if m := parseSums([]byte(sum("a") + "  a.deb\n" + sum("b") + " *b.exe\n")); m["a.deb"] != sum("a") || m["b.exe"] != sum("b") {
		t.Errorf("parseSums = %v", m)
	}
}

func TestParseTag(t *testing.T) {
	for s, ok := range map[string]bool{"v1.2.3": true, "v0.0.1": true, "1.2.3": false, "v1.2": false, "v01.2.3": false, "v1.2.3-rc1": false} {
		if _, got := ParseTag(s); got != ok {
			t.Errorf("ParseTag(%q) = %v", s, got)
		}
	}
	if v, ok := baseVersion("v0.2.0-5-gabc-dirty"); !ok || v != (Semver{0, 2, 0}) {
		t.Errorf("baseVersion = %v %v", v, ok)
	}
}
