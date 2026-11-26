// TrunkLine: a desktop SVN client for Linux and Windows. The svn work happens
// in Go (internal/svn runs the svn command-line client); the frontend in
// frontend/dist is plain HTML/JS with no build step.
//
// Copyright (C) 2026 Franco Coetzer. Licensed under the GNU GPL v3 (LICENSE).
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app, err := NewApp()
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title:            "TrunkLine",
		Width:            1200,
		Height:           800,
		MinWidth:         760,
		MinHeight:        480,
		BackgroundColour: &options.RGBA{R: 9, G: 13, B: 19, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		Bind:             []any{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
