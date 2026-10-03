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
	"sync"
	"syscall"
	"time"
)

// shellAddr is a plain, separate root shell (no pty, not on screen) kept as a back door for
// tools; people use the console on consoleAddr through ` + "`condor term`" + `.
const shellAddr = "127.0.0.1:2324"

const (
	condorHome = "/data/condor"
	shellPATH  = "/sbin:/system/bin:/system/xbin"
)

// shellPath is the tablet's shell; tests point it at the PC's.
var shellPath = "/system/bin/sh"

func main() {
	if len(os.Args) > 1 { // tools run by hand or by the condor CLI, not the console
		switch os.Args[1] {
		case "drm": // diagnostic, read-only: what the display scans out
			fmt.Print(drmInfo())
			return
		case "untar": // condor-init untar <file.tar.gz> <dir>  (Android has no tar)
			if len(os.Args) != 4 {
				fmt.Fprintln(os.Stderr, "usage: condor-init untar <file.tar.gz> <dir>")
				os.Exit(2)
			}
			n, err := untar(os.Args[2], os.Args[3])
			if err != nil {
				fmt.Fprintln(os.Stderr, "untar:", err)
				os.Exit(1)
			}
			fmt.Printf("untar: %d entries into %s\n", n, os.Args[3])
			return
		}
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("condor-init starting, pid %d", os.Getpid())
	// Survive the end of the adb/shell session that may have started us by hand.
	signal.Ignore(syscall.SIGHUP)
	fixClock()
	setHostname()
	setBacklight(80)
	go markBootGood()
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
			drawMu.Lock()
			s.Clear()
			log.Printf("got %v: screen cleared, exiting", sig)
			os.Exit(0)
		}()
		if c, err := newConsole(s); err != nil {
			log.Printf("console: %v; showing the launcher instead", err)
			go runHome(s)
		} else {
			go c.run()
			go c.serve()
		}
	}
	go runTunnel()
	go alpineBoot()
	serveShell()
}

// drawMu serialises drawing between the touch loop and the signal handler.
var drawMu sync.Mutex

// drawTouchScreen shows a black screen with a white 40x40 target in each corner, so taps
// on the targets show whether touch lines up with the display.
func drawTouchScreen(s *Screen) {
	drawMu.Lock()
	defer drawMu.Unlock()
	clear(s.buf)
	s.markRows(0, s.fbH-1)
	const t = 40
	for _, c := range [][2]int{{0, 0}, {s.W - t, 0}, {0, s.H - t}, {s.W - t, s.H - t}} {
		s.Fill(c[0], c[1], c[0]+t, c[1]+t, 255, 255, 255)
	}
	if err := s.Flush(); err != nil {
		log.Printf("flush: %v", err)
	}
}

var fingerColors = [][3]uint8{{255, 64, 64}, {64, 255, 64}, {64, 128, 255}, {255, 255, 64}, {255, 64, 255}}

// touchLoop draws a stroke under each finger. Errors (device gone) are logged and retried.
func touchLoop(s *Screen) {
	for {
		err := readTouch("Goodix", s.fbW, s.fbH, s.rot, log.Printf, func(pts []TouchPoint) {
			drawMu.Lock()
			defer drawMu.Unlock()
			for _, p := range pts {
				c := fingerColors[p.Slot%len(fingerColors)]
				switch {
				case p.Down:
					log.Printf("touch down slot %d: raw %d,%d -> logical %d,%d", p.Slot, p.RawX, p.RawY, p.X, p.Y)
					s.Dot(p.X, p.Y, 8, c[0], c[1], c[2])
				case p.Moved:
					s.Line(p.PrevX, p.PrevY, p.X, p.Y, 8, c[0], c[1], c[2])
				case p.Up:
					log.Printf("touch up   slot %d at logical %d,%d", p.Slot, p.X, p.Y)
				}
			}
			if err := s.Flush(); err != nil {
				log.Printf("flush: %v", err)
			}
		})
		log.Printf("touch: %v; retrying in 2s", err)
		time.Sleep(2 * time.Second)
	}
}

// serveShell gives each connection on shellAddr its own root shell (no pty).
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
	cmd := exec.Command(shellPath, "-i")
	cmd.Env = []string{"PATH=" + shellPATH, "HOME=" + condorHome, "PS1=condor# "}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c, c, c
	if err := cmd.Run(); err != nil && err != io.EOF {
		log.Printf("shell ended: %v", err)
	}
}
