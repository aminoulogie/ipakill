//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
	tiocswinsz = 0x5414
)

// shrcPath is the shell's startup file (mksh reads $ENV): the prompt and a few aliases.
const shrcPath = condorHome + "/shrc"

const shrc = `PS1='[root@condor ${PWD:-/}]# '
alias ll='ls -l'
alias la='ls -la'
`

// startShell runs the shell on a new pseudo-terminal of cols x rows. It returns the pty's
// master side and a function that waits for the shell to exit.
func startShell(cols, rows int) (*os.File, func(), error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(m.Fd(), tiocsptlck, unsafe.Pointer(&unlock)); err != nil {
		m.Close()
		return nil, nil, fmt.Errorf("unlock pty: %w", err)
	}
	var n uint32
	if err := ioctl(m.Fd(), tiocgptn, unsafe.Pointer(&n)); err != nil {
		m.Close()
		return nil, nil, fmt.Errorf("pty number: %w", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	defer slave.Close()
	ws := [4]uint16{uint16(rows), uint16(cols), 0, 0}
	ioctl(slave.Fd(), tiocswinsz, unsafe.Pointer(&ws))

	os.MkdirAll(condorHome, 0o755)
	os.WriteFile(shrcPath, []byte(shrc), 0o644)
	cmd := exec.Command(shellPath)
	cmd.Dir = condorHome
	cmd.Env = []string{"PATH=" + shellPATH, "HOME=" + condorHome, "TERM=xterm", "ENV=" + shrcPath,
		"USER=root", "LOGNAME=root"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, func() { cmd.Wait() }, nil
}
