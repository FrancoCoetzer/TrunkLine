# TrunkLine: a desktop SVN client for Linux and Windows (Go + Wails v2).
#
# Building on Linux needs libgtk-3-dev and libwebkit2gtk-4.1-dev. The tests
# and the app need the svn client at run time (svnadmin too, for the tests).

VERSION    := $(shell git describe --tags --dirty --always 2>/dev/null)
APP_TAGS   := desktop,production
LINUX_TAGS := $(APP_TAGS),webkit2_41
STAMP      := -X main.Version=$(VERSION)

.PHONY: all app run dev windows test vet install-desktop clean

all: test app

app:
	go build -tags $(LINUX_TAGS) -ldflags "-s -w $(STAMP)" -o build/bin/trunkline .

run: app
	build/bin/trunkline

# A debug build: right-click > Inspect in the window, and the page is also
# served on http://localhost:34115 (with the Go methods) for a browser.
dev:
	go build -tags dev,webkit2_41 -ldflags "$(STAMP)" -o build/bin/trunkline-dev .
	build/bin/trunkline-dev -assetdir frontend/dist

# Windows needs no cgo, so it cross-builds from Linux.
windows:
	GOOS=windows GOARCH=amd64 go build -tags $(APP_TAGS) -ldflags "-s -w -H windowsgui $(STAMP)" -o build/bin/trunkline.exe .

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

clean:
	rm -rf build
