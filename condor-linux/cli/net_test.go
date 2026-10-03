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

func TestServeProxyConnPlainHTTP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "index for "+r.URL.Path)
	}))
	defer origin.Close()
	a, b := net.Pipe()
	go serveProxyConn(b)
	fmt.Fprintf(a, "GET %s/v3.24/main/x86/APKINDEX.tar.gz HTTP/1.1\r\nHost: x\r\n\r\n", origin.URL)
	resp, err := http.ReadResponse(bufio.NewReader(a), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "/v3.24/main/x86/APKINDEX.tar.gz") {
		t.Fatalf("body %q", body)
	}
	a.Close()
}

func TestServeProxyConnCONNECT(t *testing.T) {
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echo.Close()
	go func() {
		c, _ := echo.Accept()
		io.Copy(c, c)
	}()
	a, b := net.Pipe()
	go serveProxyConn(b)
	fmt.Fprintf(a, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo.Addr(), echo.Addr())
	r := bufio.NewReader(a)
	line, _ := r.ReadString('\n')
	if !strings.Contains(line, "200") {
		t.Fatalf("CONNECT reply %q", line)
	}
	r.ReadString('\n') // blank line
	a.Write([]byte("ping"))
	buf := make([]byte, 4)
	io.ReadFull(r, buf)
	if string(buf) != "ping" {
		t.Fatalf("tunnel echo %q", buf)
	}
	a.Close()
}
