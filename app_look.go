package main

// The look: theme, font, zoom, and the window colour behind the page.

import (
	"fmt"
	"slices"

	"github.com/wailsapp/wails/v2/pkg/options"

	"trunkline/internal/config"
)

// lookOf is the saved theme ("dark", "light", "system") and font ("mono",
// "regular"), with anything unknown read as the default.
func lookOf(c *config.Config) (theme, font string) {
	theme, font = "system", "mono"
	if c.Theme == "dark" || c.Theme == "light" {
		theme = c.Theme
	}
	if c.Font == "regular" {
		font = c.Font
	}
	return theme, font
}

// windowColour is the window's background before the page has painted: the
// light theme's when it was picked, otherwise the dark theme's (the page
// sets it again once it knows the system's setting).
func (a *App) windowColour() *options.RGBA {
	theme := "system"
	a.cfg.Read(func(c *config.Config) { theme, _ = lookOf(c) })
	if theme == "light" {
		return &options.RGBA{R: 0xf4, G: 0xf2, B: 0xf9, A: 255}
	}
	return &options.RGBA{R: 0x16, G: 0x15, B: 0x1f, A: 255}
}

// The window's size (zoom) in percent can be from minZoom to maxZoom.
const minZoom, maxZoom = 50, 200

// SetLook saves the theme ("system", "dark" or "light"), the font ("mono" or
// "regular") and the zoom (percent).
func (a *App) SetLook(theme, font string, zoom int) error {
	if !slices.Contains([]string{"system", "dark", "light"}, theme) || !slices.Contains([]string{"mono", "regular"}, font) ||
		zoom < minZoom || zoom > maxZoom {
		return fmt.Errorf("unknown look %q, %q, %d%%", theme, font, zoom)
	}
	return a.cfg.Update(func(c *config.Config) {
		c.Theme, c.Font, c.Zoom = theme, font, zoom
		if zoom == 100 {
			c.Zoom = 0
		}
		if theme == "system" {
			c.Theme = ""
		}
		if font == "mono" {
			c.Font = ""
		}
	})
}
