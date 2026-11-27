package main

// TrunkLine's own updates (internal/update) and desktop notifications.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"trunkline/internal/config"
	"trunkline/internal/update"
)

// --- Updates ------------------------------------------------------------------

// Events for updates.
const (
	evUpdateAvailable = "update:available" // update.Info, from the automatic check
	evUpdateProgress  = "update:progress"  // string, one per install step
)

// How often the automatic check runs. GitHub allows 60 unauthenticated API
// calls an hour per address, which a whole office behind one address shares.
const (
	updateFirstCheck = 10 * time.Second
	updateEvery      = 6 * time.Hour
)

// watchUpdates checks for a new release shortly after start, then every
// updateEvery, and tells the window when there is one.
func (a *App) watchUpdates() {
	wait := updateFirstCheck
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateEvery
		off := false
		a.cfg.Read(func(c *config.Config) { off = c.NoUpdateCheck })
		if off {
			continue
		}
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		info, err := update.New().Check(ctx)
		cancel()
		if err == nil && info.Available {
			runtime.EventsEmit(a.ctx, evUpdateAvailable, info)
		}
	}
}

// CheckUpdate looks for a newer release now.
func (a *App) CheckUpdate() (update.Info, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	info, err := update.New().Check(ctx)
	if err != nil {
		return info, &appError{Kind: "other", Message: "Couldn't check for updates: " + err.Error()}
	}
	return info, nil
}

// RepoURL is the app's GitHub page.
func (a *App) RepoURL() string { return "https://github.com/" + update.Repo }

// InstallKind is how this copy was installed: deb, windows or source.
func (a *App) InstallKind() string { return update.InstallKind() }

// SetUpdateCheck turns the automatic check on or off.
func (a *App) SetUpdateCheck(on bool) error {
	return a.cfg.Update(func(c *config.Config) { c.NoUpdateCheck = !on })
}

// InstallUpdate installs release tag, then starts the new version and quits.
func (a *App) InstallUpdate(tag string) error {
	if !a.updating.TryLock() {
		return &appError{Kind: "other", Message: "An update is already being installed."}
	}
	defer a.updating.Unlock()
	restart, err := update.New().Install(a.ctx, tag, func(s string) { runtime.EventsEmit(a.ctx, evUpdateProgress, s) })
	if err != nil {
		return &appError{Kind: "other", Message: err.Error()}
	}
	if restart != "" {
		if err := update.Restart(restart); err != nil {
			return &appError{Kind: "other", Message: "Installed " + tag + ", but restarting failed: " + err.Error() + ". Start TrunkLine again."}
		}
	}
	runtime.Quit(a.ctx)
	return nil
}

// OpenReleasePage opens this app's repository page, or a page in it (a
// release, from the update check), in the browser. Nothing else.
func (a *App) OpenReleasePage(u string) error {
	base := "https://github.com/" + update.Repo
	if u != base && !strings.HasPrefix(u, base+"/") {
		return &appError{Kind: "other", Message: "Not a page of this app's repository: " + u}
	}
	runtime.BrowserOpenURL(a.ctx, u)
	return nil
}

// --- Notifications ----------------------------------------------------------------------

// Notify shows a desktop notification, if the system offers them and the
// user hasn't turned them off.
func (a *App) Notify(title, body string) error {
	off := false
	a.cfg.Read(func(c *config.Config) { off = c.NoNotify })
	if !a.notifyOK || off {
		return nil
	}
	return runtime.SendNotification(a.ctx, runtime.NotificationOptions{ID: "trunkline-" + strconv.FormatInt(time.Now().UnixNano(), 36), Title: title, Body: body})
}

// SetNotify turns the new-commit notifications on or off.
func (a *App) SetNotify(on bool) error {
	return a.cfg.Update(func(c *config.Config) { c.NoNotify = !on })
}
