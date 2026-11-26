package main

import "os"

// The pages are dark, but WebKitGTK draws <select> boxes and their popups
// with the GTK theme, which ignores the pages' CSS. Ask GTK for its dark
// variant, before Wails starts GTK, unless the user picked a theme.
func init() {
	if os.Getenv("GTK_THEME") == "" {
		os.Setenv("GTK_THEME", "Adwaita:dark")
	}
}
