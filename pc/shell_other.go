//go:build !windows

package main

import (
	"context"
	"os/exec"
)

func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}

func startDetached(exe string, args ...string) error {
	return exec.Command(exe, args...).Start()
}
