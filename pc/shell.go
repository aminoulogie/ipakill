package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The terminal in the ipakill iPhone app: commands typed on the phone run
// here, one at a time, in a working directory that survives restarts.
// Off unless the server was started with --shell: it is a remote shell,
// guarded only by the pairing code (and the lockout in serve's auth).
//
// Built-in commands:
//
//	cd <dir>        change directory
//	restart         swap in ipakill-core.new.exe (if built) and restart serve
//	update          download the latest prebuilt ipakill-core.exe and restart

const coreURL = "https://github.com/aminoulogie/ipakill/releases/download/pc-core/ipakill-core.exe"

var (
	shellMu  sync.Mutex
	shellDir = filepath.Join(home, "shell_dir")
)

const shellHelp = `ipakill terminal - commands run on the PC (cmd.exe).
  cd <dir>     change directory (remembered)
  update       download the latest ipakill-core and restart (no git or Go needed)
  restart      restart 'ipakill serve' (uses ipakill-core.new.exe if you built one)`

func shellEnabled() bool {
	for _, a := range os.Args[2:] {
		if a == "--shell" {
			return true
		}
	}
	return false
}

func currentDir() string {
	if b, err := os.ReadFile(shellDir); err == nil {
		if d := strings.TrimSpace(string(b)); d != "" {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				return d
			}
		}
	}
	h, _ := os.UserHomeDir()
	return h
}

func shellHandler(w http.ResponseWriter, r *http.Request) {
	if !shellEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false,
			"error": "the terminal is off - on the PC start the server with: ipakill-core serve --shell"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST only"})
		return
	}
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad request"})
		return
	}
	if !shellMu.TryLock() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "another command is still running"})
		return
	}
	defer shellMu.Unlock()

	line := strings.TrimSpace(req.Cmd)
	dir := currentDir()
	fmt.Printf("[ipakill] terminal> %s\n", line)
	reply := func(out string, code int) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out, "code": code, "cwd": currentDir()})
	}

	fields := strings.Fields(line)
	switch {
	case line == "" || line == "help":
		reply(shellHelp, 0)
	case fields[0] == "cd":
		target := strings.TrimSpace(strings.TrimPrefix(line, "cd"))
		if target == "" {
			reply(dir, 0)
			return
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		st, err := os.Stat(target)
		if err != nil || !st.IsDir() {
			reply("no such folder: "+target, 1)
			return
		}
		os.WriteFile(shellDir, []byte(filepath.Clean(target)), 0o600)
		reply("", 0)
	case line == "restart":
		out, err := prepareRestart()
		if err != nil {
			reply(out+err.Error(), 1)
			return
		}
		reply(out+"restarting - reconnects in a few seconds", 0)
		go restartSoon()
	case line == "update":
		// Same as pc/update.ps1: fetch the prebuilt ipakill-core.exe, swap, restart.
		exe, _ := os.Executable()
		next := filepath.Join(filepath.Dir(exe), "ipakill-core.new.exe")
		if err := download(coreURL, next); err != nil {
			reply("download failed: "+err.Error(), 1)
			return
		}
		out, err := prepareRestart()
		if err != nil {
			reply(out+err.Error(), 1)
			return
		}
		reply(out+"updated - restarting, reconnects in a few seconds", 0)
		go restartSoon()
	default:
		out, code := runShell(dir, line, 10*time.Minute)
		reply(out, code)
	}
}

func runShell(dir, line string, timeout time.Duration) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := shellCommand(ctx, line)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	s := out.String()
	if len(s) > 256<<10 {
		s = "...\n" + s[len(s)-256<<10:]
	}
	if ctx.Err() == context.DeadlineExceeded {
		return s + fmt.Sprintf("\n(stopped after %s)", timeout), 124
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return s, ee.ExitCode()
		}
		return s + err.Error(), 1
	}
	return s, 0
}

// prepareRestart swaps a freshly built ipakill-core.new.exe in place of the
// running one. Windows can rename a running .exe but not overwrite it.
func prepareRestart() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	next := filepath.Join(filepath.Dir(exe), "ipakill-core.new.exe")
	if _, err := os.Stat(next); err != nil {
		return "", nil // nothing new, just restart
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return "", fmt.Errorf("cannot move the running ipakill-core aside: %v", err)
	}
	if err := os.Rename(next, exe); err != nil {
		os.Rename(old, exe)
		return "", fmt.Errorf("cannot put the new ipakill-core in place: %v", err)
	}
	return "new ipakill-core installed\n", nil
}

// restartSoon starts a fresh 'serve' in its own window, then exits so it
// can take over the port (serve retries the port for a while).
func restartSoon() {
	time.Sleep(500 * time.Millisecond)
	exe, _ := os.Executable()
	if err := startDetached(exe, os.Args[1:]...); err != nil {
		fmt.Println("[ipakill] restart failed: " + err.Error())
		return
	}
	os.Exit(0)
}
