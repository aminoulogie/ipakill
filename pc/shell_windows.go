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

// startDetached runs the program in a console window of its own, so it keeps
// running after this process exits. (Not via "cmd /C start": start reads an
// unquoted first argument as the program, not the window title.)
func startDetached(exe string, args ...string) error {
	const createNewConsole = 0x00000010
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return cmd.Start()
}
