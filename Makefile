# TrunkLine: a desktop SVN client for Linux and Windows (Go + Wails v2).
#
# Building on Linux needs libgtk-3-dev and libwebkit2gtk-4.1-dev. The tests
# and the app need the svn client at run time (svnadmin too, for the tests).
# make dist also needs nsis (makensis) for the Windows setup.

SHELL      := /bin/bash
VERSION    := $(shell git describe --tags --dirty --always 2>/dev/null)
APP_TAGS   := desktop,production
LINUX_TAGS := $(APP_TAGS),webkit2_41
# STAMP(version, install kind): what the updater knows about this build.
STAMP       = -X trunkline/internal/update.Version=$(1) -X trunkline/internal/update.Install=$(2)

.PHONY: all app run dev windows test vet install-desktop dist release clean

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

# Windows needs no cgo, so it cross-builds from Linux.
windows:
	GOOS=windows GOARCH=amd64 go build -tags $(APP_TAGS) -ldflags "-s -w -H windowsgui $(call STAMP,$(VERSION),source)" -o build/bin/trunkline.exe .

test:
	go test ./...

vet:
	go vet ./...
	GOOS=windows go vet -tags $(APP_TAGS) ./...

# A launcher in the desktop's app menu for this clone's build.
install-desktop: app
	mkdir -p $(HOME)/.local/share/applications
	printf '[Desktop Entry]\nType=Application\nName=TrunkLine (dev)\nExec=%s\nIcon=%s\nTerminal=false\nCategories=Development;RevisionControl;\n' \
		"$(CURDIR)/build/bin/trunkline" "$(CURDIR)/packaging/trunkline.svg" > $(HOME)/.local/share/applications/trunkline-dev.desktop
	@echo "Installed ~/.local/share/applications/trunkline-dev.desktop"

# The release files for TAG (default: the tag at HEAD) in build/dist: the
# .deb, the Windows setup and SHA256SUMS. The release workflow runs this;
# TAG=v9.9.9 makes a local test set from any commit (don't install it: it
# would never accept a real release as an update).
dist: test
	@tag="$(TAG)"; [[ -n $$tag ]] || tag=$$(git describe --tags --exact-match 2>/dev/null) || \
		{ echo "HEAD is not at a release tag: use make release, or TAG=vX.Y.Z for a test build"; exit 1; }; \
	[[ $$tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$$ ]] || { echo "$$tag is not a vX.Y.Z tag"; exit 1; }; \
	command -v makensis >/dev/null || { echo "makensis not found: sudo apt install nsis"; exit 1; }; \
	set -e; v=$${tag#v}; rm -rf build/dist build/stage; mkdir -p build/dist build/stage; \
	echo "== $$tag: Linux"; \
	go build -tags $(LINUX_TAGS) -ldflags "-s -w $(call STAMP,$$tag,deb)" -o build/stage/trunkline .; \
	packaging/linux/build-deb.sh $$tag build/stage/trunkline build/dist/trunkline_$${v}_amd64.deb; \
	echo "== $$tag: Windows"; \
	GOOS=windows GOARCH=amd64 go build -tags $(APP_TAGS) -ldflags "-s -w -H windowsgui $(call STAMP,$$tag,windows)" -o build/stage/trunkline.exe .; \
	makensis -V2 -DVERSION=$$v -DAPPEXE=$(CURDIR)/build/stage/trunkline.exe -DLICENSEFILE=$(CURDIR)/LICENSE \
		-DOUTFILE=$(CURDIR)/build/dist/trunkline-$$v-setup.exe packaging/windows/installer.nsi; \
	cd build/dist && sha256sum -- * > SHA256SUMS && ls -l

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
