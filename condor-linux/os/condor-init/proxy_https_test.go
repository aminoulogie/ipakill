package main

import (
	"bufio"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// busybox wget sends an https:// URL to the proxy as a plain GET (no CONNECT): the proxy
// fetches it itself, so it must trust the site's certificate.
func TestProxyFetchesHTTPSForWget(t *testing.T) {
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#!/bin/sh\necho update\n")
	}))
	defer site.Close()
	pool := x509.NewCertPool()
	pool.AddCert(site.Certificate())
	old := proxyRoots
	proxyRoots = func() *x509.CertPool { return pool }
	defer func() { proxyRoots = old }()

	ask := func() *http.Response {
		a, b := net.Pipe()
		go serveProxyConn(b, directDial)
		defer a.Close()
		fmt.Fprintf(a, "GET %s/update.sh HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", site.URL, strings.TrimPrefix(site.URL, "https://"))
		resp, err := http.ReadResponse(bufio.NewReader(a), nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		return resp
	}
	resp := ask()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "echo update") {
		t.Fatalf("got %s %q", resp.Status, body)
	}
}
