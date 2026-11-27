//go:build !windows

package update

import "errors"

// startAfterExit is only for Windows (.msi updates).
func startAfterExit(string) error { return errors.New("only on Windows") }
