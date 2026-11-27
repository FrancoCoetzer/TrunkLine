//go:build !dev

package main

// instanceID names the one running TrunkLine that later launches hand their
// arguments to. Debug builds use their own (instance_dev.go), so make dev
// runs beside an installed TrunkLine.
const instanceID = "io.github.francocoetzer.trunkline"
