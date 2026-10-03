//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVTOutput makes the Windows console interpret the tablet's escape sequences
// (colours, cursor moves) instead of printing them.
func enableVTOutput() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
