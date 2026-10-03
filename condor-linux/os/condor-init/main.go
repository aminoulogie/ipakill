// condor-init: our first userspace program on the Condor TRA-901G.
//
// Started as root by the /system takeover hook (see cli/takeover.go) after Android's zygote
// has been stopped. It clears the framebuffer, draws a test pattern in the logical
// (portrait) orientation, and serves a shell on 127.0.0.1:2323, which the PC reaches over USB:
//
//	adb forward tcp:2323 tcp:2323   then connect to localhost:2323 (e.g. ncat, PuTTY raw)
//
// Build: GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -o condor-init .
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const shellAddr = "127.0.0.1:2323"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "drm" { // diagnostic, read-only: what the display scans out
		fmt.Print(drmInfo())
		return
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("condor-init starting, pid %d", os.Getpid())
	// Survive the end of the adb/shell session that may have started us by hand.
	signal.Ignore(syscall.SIGHUP)
	setBacklight(80)
	s, err := openScreen(rotation)
	if err != nil {
		log.Printf("framebuffer: %v", err)
	} else {
		if err := s.Clear(); err != nil {
			log.Printf("clear: %v", err)
		}
		// Leave the screen black when we're stopped (kill, Ctrl-C), not half a picture.
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			sig := <-stop
			s.Clear()
			log.Printf("got %v: screen cleared, exiting", sig)
			os.Exit(0)
		}()
		s.TestPattern()
		if err := s.Flush(); err != nil {
			log.Printf("flush: %v", err)
		}
		log.Printf("test pattern drawn")
	}
	serveShell()
}

// serveShell gives each connection on 127.0.0.1:2323 a root /system/bin/sh (no pty).
func serveShell() {
	for {
		ln, err := net.Listen("tcp", shellAddr)
		if err != nil {
			log.Printf("listen %s: %v; retrying", shellAddr, err)
			time.Sleep(2 * time.Second)
			continue
		}
		log.Printf("shell listening on %s", shellAddr)
		for {
			c, err := ln.Accept()
			if err != nil {
				log.Printf("accept: %v", err)
				break
			}
			go handle(c)
		}
		ln.Close()
	}
}

func handle(c net.Conn) {
	defer c.Close()
	log.Printf("shell connection from %s", c.RemoteAddr())
	fmt.Fprintf(c, "condor-init shell (pid %d). No Android running. Type 'exit' to close.\n", os.Getpid())
	cmd := exec.Command("/system/bin/sh", "-i")
	cmd.Env = []string{"PATH=/sbin:/system/bin:/system/xbin", "HOME=/data/condor", "PS1=condor# "}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c, c, c
	if err := cmd.Run(); err != nil && err != io.EOF {
		log.Printf("shell ended: %v", err)
	}
}
