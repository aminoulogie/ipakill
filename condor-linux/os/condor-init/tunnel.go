package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Internet over USB. Android 4.2's adb can't forward from the tablet to the PC (no
// "adb reverse"), so the PC keeps a control connection open instead:
//
//	PC (condor net) ── adb forward ──> tunnelAddr: "CTRL\n"            (one, long-lived)
//	program on tablet ──> proxyAddr      condor-init: "OPEN 7\n" on CTRL
//	PC ── adb forward ──> tunnelAddr: "DATA 7\n", then the two are spliced together
//
// The PC end speaks HTTP proxy (CONNECT and plain GET), so anything that honours
// http_proxy/https_proxy (apk, wget, curl, git) reaches the internet through the PC. When no
// PC is connected, condor-init answers the proxy itself and goes out over Wi-Fi (proxy.go).
const (
	tunnelAddr = "127.0.0.1:2325"
	proxyAddr  = "127.0.0.1:3128"
)

type tunnel struct {
	mu      sync.Mutex
	ctrl    net.Conn
	next    int
	pending map[int]net.Conn
}

func newTunnel() *tunnel { return &tunnel{pending: map[int]net.Conn{}} }

// readLine reads one "\n"-terminated line byte by byte, so nothing after it is consumed.
func readLine(c net.Conn) (string, error) {
	var b [1]byte
	var line []byte
	for len(line) < 64 {
		if _, err := c.Read(b[:]); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return string(line), nil
		}
		line = append(line, b[0])
	}
	return "", fmt.Errorf("line too long")
}

// serveTunnel accepts the PC's control and data connections on addr.
func (t *tunnel) serveTunnel(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go t.handlePC(c)
	}
}

func (t *tunnel) handlePC(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := readLine(c)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	switch {
	case line == "CTRL":
		t.mu.Lock()
		if t.ctrl != nil {
			t.ctrl.Close()
		}
		t.ctrl = c
		t.mu.Unlock()
		log.Printf("net: PC connected (internet over USB on)")
		io.Copy(io.Discard, c) // until the PC goes away
		t.mu.Lock()
		if t.ctrl == c {
			t.ctrl = nil
		}
		t.mu.Unlock()
		c.Close()
		log.Printf("net: PC disconnected")
	case strings.HasPrefix(line, "DATA "):
		id, _ := strconv.Atoi(strings.TrimPrefix(line, "DATA "))
		t.mu.Lock()
		client := t.pending[id]
		delete(t.pending, id)
		t.mu.Unlock()
		if client == nil {
			c.Close()
			return
		}
		splice(client, c)
	default:
		c.Close()
	}
}

// serveProxy accepts connections from programs on the tablet and asks the PC for a data
// connection for each.
func (t *tunnel) serveProxy(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		client, err := ln.Accept()
		if err != nil {
			return err
		}
		t.mu.Lock()
		ctrl := t.ctrl
		if ctrl == nil { // no PC: go straight out over the tablet's own network (Wi-Fi)
			t.mu.Unlock()
			go func() {
				defer client.Close()
				serveProxyConn(client, directDial)
			}()
			continue
		}
		t.next++
		id := t.next
		t.pending[id] = client
		_, err = fmt.Fprintf(ctrl, "OPEN %d\n", id)
		t.mu.Unlock()
		if err != nil {
			ctrl.Close()
		}
		go func() { // give up if the PC never claims it
			time.Sleep(15 * time.Second)
			t.mu.Lock()
			if c := t.pending[id]; c != nil {
				delete(t.pending, id)
				c.Close()
			}
			t.mu.Unlock()
		}()
	}
}

// splice copies both ways until either side closes, then closes both.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// runTunnel starts both listeners and restarts them if they fail.
func runTunnel() {
	t := newTunnel()
	go func() {
		for {
			log.Printf("net: tunnel: %v", t.serveTunnel(tunnelAddr))
			time.Sleep(2 * time.Second)
		}
	}()
	for {
		log.Printf("net: proxy: %v", t.serveProxy(proxyAddr))
		time.Sleep(2 * time.Second)
	}
}
