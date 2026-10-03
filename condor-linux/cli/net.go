package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// condor net: internet for the tablet over USB. Android 4.2 has no "adb reverse", so this
// keeps a control connection open to condor-init (127.0.0.1:2325 on the tablet). Each time a
// program on the tablet uses the proxy (127.0.0.1:3128 there), condor-init sends "OPEN n";
// we connect back with "DATA n" and act as an HTTP proxy on that connection, using the PC's
// internet. Leave it running in its own window.
const netPort = "2325"

func netCmd(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	if out, err := adb("forward", "tcp:"+netPort, "tcp:"+netPort); err != nil {
		return fmt.Errorf("adb forward: %s", strings.TrimSpace(out))
	}
	defer adb("forward", "--remove", "tcp:"+netPort)
	fmt.Println("internet over USB for the tablet. Leave this window open; Ctrl+C to stop.")
	waiting := false
	for {
		err := netSession("127.0.0.1:" + netPort)
		if !waiting {
			fmt.Printf("waiting for condor-init on the tablet (%v)...\n", err)
			waiting = true
		}
		time.Sleep(2 * time.Second)
		if err == nil {
			waiting = false
		}
	}
}

// netSession runs one control connection until it drops.
func netSession(addr string) error {
	ctrl, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer ctrl.Close()
	if _, err := io.WriteString(ctrl, "CTRL\n"); err != nil {
		return err
	}
	r := bufio.NewReader(ctrl)
	connected := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if !connected {
				return fmt.Errorf("tablet closed the connection: is the new condor-init running? (dev.cmd)")
			}
			fmt.Println("tablet disconnected")
			return err
		}
		if !connected {
			connected = true
		}
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "OPEN "); ok {
			go func() {
				d, err := net.DialTimeout("tcp", addr, 5*time.Second)
				if err != nil {
					return
				}
				defer d.Close()
				fmt.Fprintf(d, "DATA %s\n", id)
				serveProxyConn(d)
			}()
		}
	}
}

// proxyTransport fetches plain-http requests for the tablet. No proxy of its own, and no
// transparent decompression: the bytes go to the tablet exactly as the server sent them.
var proxyTransport = &http.Transport{Proxy: nil, DisableCompression: true, ResponseHeaderTimeout: 60 * time.Second}

// serveProxyConn speaks HTTP proxy on one connection: CONNECT tunnels (https) and
// absolute-URL requests (http), with keep-alive.
func serveProxyConn(c net.Conn) {
	br := bufio.NewReader(c)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if req.Method == http.MethodConnect {
			target, err := net.DialTimeout("tcp", req.Host, 15*time.Second)
			if err != nil {
				fmt.Fprintf(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
				fmt.Printf("  CONNECT %s failed: %v\n", req.Host, err)
				return
			}
			fmt.Printf("  CONNECT %s\n", req.Host)
			io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
			if n := br.Buffered(); n > 0 {
				b, _ := br.Peek(n)
				target.Write(b)
			}
			go func() { io.Copy(target, c); target.Close() }()
			io.Copy(c, target)
			target.Close()
			return
		}
		if !req.URL.IsAbs() {
			fmt.Fprintf(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
			return
		}
		fmt.Printf("  %s %s\n", req.Method, req.URL)
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		resp, err := proxyTransport.RoundTrip(req)
		if err != nil {
			fmt.Fprintf(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			fmt.Printf("  failed: %v\n", err)
			return
		}
		err = resp.Write(c)
		resp.Body.Close()
		if err != nil || req.Close || resp.Close {
			return
		}
	}
}
