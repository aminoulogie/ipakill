package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

const termPort = "2323"

// term opens the root shell that condor-init serves on the tablet (127.0.0.1:2323),
// reached over USB with 'adb forward'. Only works in takeover mode: condor-init must
// be running (condor takeover arm <condor-init>, then condor reboot).
func term(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	if !strings.Contains(sh("ps"), "condor-init") {
		return fmt.Errorf("condor-init isn't running; arm takeover and reboot:\n" +
			"  condor takeover arm <condor-init>\n  condor reboot")
	}
	if out, err := adb("forward", "tcp:"+termPort, "tcp:"+termPort); err != nil {
		return fmt.Errorf("adb forward: %s", strings.TrimSpace(out))
	}
	defer adb("forward", "--remove", "tcp:"+termPort)

	c, err := net.Dial("tcp", "127.0.0.1:"+termPort)
	if err != nil {
		return fmt.Errorf("connect to condor-init shell: %w", err)
	}
	defer c.Close()
	fmt.Println("connected to condor-init shell (type 'exit' to quit)")

	// Relay both ways; return when either side closes (shell exits, or stdin ends).
	// Windows consoles send CRLF; the tablet's sh wants bare LF, so drop \r from stdin
	// (otherwise every command arrives as "id\r": ": not found", garbled output).
	done := make(chan struct{}, 2)
	go func() { io.Copy(os.Stdout, c); done <- struct{}{} }()
	go func() { io.Copy(c, crStripper{os.Stdin}); done <- struct{}{} }()
	<-done
	return nil
}

// crStripper is a reader that removes carriage returns from the wrapped stream.
type crStripper struct{ r io.Reader }

func (s crStripper) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	w := 0
	for i := 0; i < n; i++ {
		if p[i] != '\r' {
			p[w] = p[i]
			w++
		}
	}
	return w, err
}
