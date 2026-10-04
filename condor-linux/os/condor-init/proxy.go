package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// When no PC is connected, condor-init is the proxy itself and reaches the internet over the
// tablet's own network (Wi-Fi). Android keeps no /etc/resolv.conf for us, so DNS goes to the
// servers Alpine has (written by udhcpc when Wi-Fi connects), falling back to 1.1.1.1.

func nameservers() []string {
	var ns []string
	b, _ := os.ReadFile(alpineRoot + "/etc/resolv.conf")
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "nameserver" {
			ns = append(ns, net.JoinHostPort(f[1], "53"))
		}
	}
	return append(ns, "1.1.1.1:53", "8.8.8.8:53")
}

var resolver = &net.Resolver{
	PreferGo: true,
	Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		var last error
		for _, ns := range nameservers() {
			c, err := d.DialContext(ctx, network, ns)
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	},
}

var dialer = &net.Dialer{Timeout: 15 * time.Second, Resolver: resolver}

func directDial(addr string) (net.Conn, error) { return dialer.Dial("tcp", addr) }

// proxyRoots are the certificates the proxy trusts when it fetches an https:// URL itself
// (busybox wget asks for those with a plain GET, not CONNECT). condor-init runs in
// Android's root, where Go finds no certificates, so it takes Alpine's. Tests replace it.
var proxyRoots = alpineRoots

var directTransport = sync.OnceValue(func() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		ResponseHeaderTimeout: 60 * time.Second,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{RootCAs: proxyRoots()},
		TLSHandshakeTimeout:   20 * time.Second,
	}
})

// serveProxyConn speaks HTTP proxy on one connection: CONNECT tunnels (https) and
// absolute-URL requests (http), with keep-alive. dial opens CONNECT targets.
func serveProxyConn(c net.Conn, dial func(string) (net.Conn, error)) {
	br := bufio.NewReader(c)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if req.Method == http.MethodConnect {
			target, err := dial(req.Host)
			if err != nil {
				time.Sleep(3 * time.Second)
				target, err = dial(req.Host)
			}
			if err != nil {
				fmt.Fprintf(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
				return
			}
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
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		resp, err := directTransport().RoundTrip(req)
		if err != nil && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
			// Right after boot the network can still be settling (DNS, DHCP): retry once.
			time.Sleep(3 * time.Second)
			resp, err = directTransport().RoundTrip(req)
		}
		if err != nil {
			log.Printf("proxy: %s: %v", req.URL, err)
			msg := "no internet: connect Wi-Fi (wifi connect SSID password) or run 'condor net' on the PC"
			var ce *tls.CertificateVerificationError
			if errors.As(err, &ce) {
				msg = "secure connection refused: " + strings.ReplaceAll(ce.Err.Error(), "\n", " ")
			}
			fmt.Fprintf(c, "HTTP/1.1 502 %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", msg)
			return
		}
		err = resp.Write(c)
		resp.Body.Close()
		if err != nil || req.Close || resp.Close {
			return
		}
	}
}
