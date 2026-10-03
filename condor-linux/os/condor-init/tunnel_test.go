package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestTunnelSplicesTabletClientToPC plays the PC: it opens CTRL, and for every OPEN it
// connects back with DATA and echoes in upper case.
func TestTunnelSplicesTabletClientToPC(t *testing.T) {
	tn := newTunnel()
	tl, _ := net.Listen("tcp", "127.0.0.1:0")
	pl, _ := net.Listen("tcp", "127.0.0.1:0")
	tl.Close()
	pl.Close()
	go tn.serveTunnel(tl.Addr().String())
	go tn.serveProxy(pl.Addr().String())
	time.Sleep(50 * time.Millisecond)

	// Before the PC connects, condor-init proxies directly; an unreachable host gives a 502.
	c, _ := net.Dial("tcp", pl.Addr().String())
	fmt.Fprint(c, "GET http://127.0.0.1:1/ HTTP/1.1\r\nHost: x\r\n\r\n")
	b, _ := io.ReadAll(c)
	if !strings.Contains(string(b), "502") {
		t.Fatalf("without PC: %q", b)
	}

	ctrl, err := net.Dial("tcp", tl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(ctrl, "CTRL\n")
	go func() {
		r := bufio.NewReader(ctrl)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			id := strings.TrimSpace(strings.TrimPrefix(line, "OPEN "))
			go func() {
				d, _ := net.Dial("tcp", tl.Addr().String())
				fmt.Fprintf(d, "DATA %s\n", id)
				buf := make([]byte, 64)
				n, _ := d.Read(buf)
				d.Write([]byte(strings.ToUpper(string(buf[:n]))))
				d.Close()
			}()
		}
	}()
	time.Sleep(50 * time.Millisecond)

	c, err = net.Dial("tcp", pl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("hello pc"))
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	b, _ = io.ReadAll(c)
	if string(b) != "HELLO PC" {
		t.Fatalf("through tunnel: %q", b)
	}
}
