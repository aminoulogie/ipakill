package main

import (
	"context"
	"os/exec"
	"syscall"
)

// cmd.exe parses its own command line, so hand it over verbatim.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + line + `"`}
	return cmd
}

// startDetached opens the program in a new console window.
func startDetached(exe string, args ...string) error {
	return exec.Command("cmd.exe", append([]string{"/C", "start", "ipakill", exe}, args...)...).Start()
}
