// TrunkLine: a desktop SVN client for Linux and Windows. The svn work happens
// in Go (internal/svn runs the svn command-line client); the frontend in
// frontend/dist is plain HTML/JS with no build step.
//
// Copyright (C) 2026 Franco Coetzer. Licensed under the GNU GPL v3 (LICENSE).
package main

import (
	"embed"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"trunkline/internal/update"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	req, err := parseArgs(os.Args[1:])
	switch {
	case errors.Is(err, errHelp):
		fmt.Print(cliHelp())
		return
	case errors.Is(err, errVersion):
		fmt.Println("TrunkLine", update.Version)
		return
	case err != nil:
		fmt.Fprintf(os.Stderr, "trunkline: %v\n\n%s", err, cliHelp())
		os.Exit(2)
	}
	app, err := NewApp()
	if err != nil {
		log.Fatal(err)
	}
	app.pending = req
	err = wails.Run(&options.App{
		Title:            "TrunkLine",
		Width:            1200,
		Height:           800,
		MinWidth:         760,
		MinHeight:        480,
		BackgroundColour: app.windowColour(),
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		Bind:             []any{app},
		// A folder dropped on the window opens its working copy (OpenDropped).
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               instanceID,
			OnSecondInstanceLaunch: app.secondInstance,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
