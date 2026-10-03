package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectProxyFetches(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok "+r.URL.Path)
	}))
	defer origin.Close()
	a, b := net.Pipe()
	go serveProxyConn(b, directDial)
	fmt.Fprintf(a, "GET %s/APKINDEX HTTP/1.1\r\nHost: x\r\n\r\n", origin.URL)
	resp, err := http.ReadResponse(bufio.NewReader(a), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ok /APKINDEX") {
		t.Fatalf("body %q", body)
	}
	a.Close()
}
