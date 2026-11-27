# TrunkLine: a desktop SVN client for Linux and Windows (Go + Wails v2).
#
# Building on Linux needs libgtk-3-dev and libwebkit2gtk-4.1-dev. The tests
# and the app need the svn client at run time (svnadmin too, for the tests).
# make dist also needs rpm (rpmbuild) for the .rpm, and nsis (makensis) and
# wixl for the Windows setups and .msi.

SHELL      := /bin/bash
VERSION    := $(shell git describe --tags --match 'v[0-9]*' --dirty --always 2>/dev/null)
APP_TAGS   := desktop,production
LINUX_TAGS := $(APP_TAGS),webkit2_41
# STAMP(version, install kind): what the updater knows about this build.
STAMP       = -X trunkline/internal/update.Version=$(1) -X trunkline/internal/update.Install=$(2)

.PHONY: all app run dev windows winres test ui-test bench vet install-desktop dist dist-linux dist-windows release clean

all: test app

app:
	go build -tags $(LINUX_TAGS) -ldflags "-s -w $(call STAMP,$(VERSION),source)" -o build/bin/trunkline .

run: app
	build/bin/trunkline

# A debug build: right-click > Inspect in the window, and the page is also
# served on http://localhost:34115 (with the Go methods) for a browser.
dev:
	go build -tags dev,webkit2_41 -ldflags "$(call STAMP,$(VERSION),source)" -o build/bin/trunkline-dev .
	build/bin/trunkline-dev -assetdir frontend/dist -devserver localhost:34115

# The UI tests (tests/ui): Playwright drives the dev build's page against a
# throwaway repository. Needs Node.js; the app's window goes to a virtual
# display when xvfb-run is installed.
XVFB = $(if $(shell command -v xvfb-run),xvfb-run -a env UI_XVFB=1)

ui-test:
	go build -tags dev,webkit2_41 -ldflags "$(call STAMP,$(VERSION),source)" -o build/bin/trunkline-dev .
	cd tests/ui && npm ci --no-audit --no-fund --silent
	cd tests/ui && $(XVFB) npx playwright test

# The big-repository benchmark (bench_test.go): times what the window does on
# a repository of BENCH_REVS revisions (100000) and BENCH_FILES files (50000).
# The repository is made once and kept in build/bench (a few GB).
bench:
	go test -tags bench -run TestBigRepo -v -count=1 -timeout 120m .

# The Windows resources linked into the .exe (icon, version, manifest) are
# written for each build by make_syso.py, with X.Y.Z of the last tag.
SYSO_VERSION = $(shell [[ '$(VERSION)' =~ ^v([0-9]+\.[0-9]+\.[0-9]+) ]] && echo "$${BASH_REMATCH[1]}" || echo 0.0.0)

# Windows needs no cgo, so it cross-builds from Linux.
windows:
	python3 packaging/windows/make_syso.py $(SYSO_VERSION) amd64
	GOOS=windows GOARCH=amd64 go build -tags $(APP_TAGS) -ldflags "-s -w -H windowsgui $(call STAMP,$(VERSION),source)" -o build/bin/trunkline.exe .

# The Windows pictures: packaging/windows/trunkline.ico (the .exe's and the
# installer's icon) and the installer's welcome and header pictures, all
# committed, drawn by a script that mirrors packaging/trunkline.svg. Needs
# Python 3 and Pillow.
winres:
	python3 packaging/windows/make_icon.py

test:
	go test ./...

vet:
	go vet ./...
	GOOS=windows go vet -tags $(APP_TAGS) ./...

# A launcher in the desktop's app menu for this clone's build, also under
# "Open With" on folders.
install-desktop: app
	mkdir -p $(HOME)/.local/share/applications
	printf '[Desktop Entry]\nType=Application\nName=TrunkLine (dev)\nExec=%s %%F\nIcon=%s\nTerminal=false\nCategories=Development;RevisionControl;\nMimeType=inode/directory;\n' \
		"$(CURDIR)/build/bin/trunkline" "$(CURDIR)/packaging/trunkline.svg" > $(HOME)/.local/share/applications/trunkline-dev.desktop
	-command -v update-desktop-database >/dev/null && update-desktop-database $(HOME)/.local/share/applications
	@echo "Installed ~/.local/share/applications/trunkline-dev.desktop"

# The release files for TAG (default: the tag at HEAD) in build/dist, by
# packaging/dist.sh: .deb and .rpm for this machine's processor, the Windows
# setups (x64, ARM64) and .msi (x64), and SHA256SUMS. The release workflow
# builds the Linux files on an x64 and an ARM64 machine (dist-linux), the
# Windows ones (dist-windows), then the sums. TAG=v9.9.9 makes a local test
# set from any commit (don't install it: it would never accept a real
# release as an update).
DIST_TAG = $(or $(TAG),$(shell git describe --tags --match 'v[0-9]*' --exact-match 2>/dev/null))

dist: test
	rm -rf build/dist build/stage
	packaging/dist.sh linux '$(DIST_TAG)'
	packaging/dist.sh windows '$(DIST_TAG)'
	packaging/dist.sh sums

dist-linux:
	packaging/dist.sh linux '$(DIST_TAG)'

dist-windows:
	packaging/dist.sh windows '$(DIST_TAG)'

# Tag a release and push it: GitHub Actions (.github/workflows/release.yml)
# builds the files and publishes the release, and installed copies offer it.
#   make release V=v0.2.0 NOTES="Blame view"
# The first line of NOTES is what users see in the update banner.
release: export NOTES := $(NOTES)
release:
	@[[ $(V) =~ ^v[0-9]+\.[0-9]+\.[0-9]+$$ ]] || { echo 'usage: make release V=vX.Y.Z NOTES="..."'; exit 1; }
	@[[ -n $$NOTES ]] || { echo "NOTES is required: the release notes users see"; exit 1; }
	@[[ -z $$(git status --porcelain) ]] || { echo "commit or stash your changes first"; exit 1; }
	@[[ $$(git rev-parse --abbrev-ref HEAD) == "main" ]] || { echo "releases are tagged on main"; exit 1; }
	git fetch origin main --quiet
	@[[ -z $$(git log HEAD..origin/main --oneline) ]] || { echo "main is behind origin/main: pull first"; exit 1; }
	$(MAKE) test
	git tag -a $(V) -m "$$NOTES"
	git push origin main $(V)
	@echo "Pushed $(V). The release workflow builds and publishes it: https://github.com/FrancoCoetzer/TrunkLine/actions"

clean:
	rm -rf build
