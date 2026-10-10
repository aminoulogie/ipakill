package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live state of the install in progress, polled by the iPhone app so it can
// show a progress bar and the full plumesign log while it happens.

type progressState struct {
	mu      sync.Mutex
	Running bool     `json:"running"`
	Name    string   `json:"name"`
	Stage   string   `json:"stage"` // download, upload, sign, install, done, failed
	Done    int64    `json:"done"`  // bytes, for download / upload
	Total   int64    `json:"total"` // bytes, 0 if unknown
	Percent int      `json:"percent"`
	Log     []string `json:"-"`
	Started int64    `json:"started"` // unix time, tells installs apart
}

var (
	prog       progressState
	logPrefix  = regexp.MustCompile(`^\[[^\]]*\]\s*`)
	installPct = regexp.MustCompile(`Installation progress: (\d+)%`)
)

func progStart(name, stage string) {
	prog.mu.Lock()
	defer prog.mu.Unlock()
	prog.Running, prog.Name, prog.Stage = true, name, stage
	prog.Done, prog.Total, prog.Percent = 0, 0, 0
	prog.Log = nil
	prog.Started = time.Now().Unix()
}

func progStage(stage string) {
	prog.mu.Lock()
	prog.Stage = stage
	prog.mu.Unlock()
}

func progBytes(done, total int64) {
	prog.mu.Lock()
	prog.Done, prog.Total = done, total
	prog.mu.Unlock()
}

// progLine records one line of plumesign output.
func progLine(line string) {
	line = strings.TrimSpace(logPrefix.ReplaceAllString(line, ""))
	if line == "" {
		return
	}
	prog.mu.Lock()
	defer prog.mu.Unlock()
	if m := installPct.FindStringSubmatch(line); m != nil {
		prog.Stage = "install"
		prog.Percent, _ = strconv.Atoi(m[1])
		return // one line per percent would flood the log
	}
	if len(prog.Log) < 5000 {
		prog.Log = append(prog.Log, line)
	}
}

func progEnd(err error) {
	prog.mu.Lock()
	defer prog.mu.Unlock()
	prog.Running = false
	if err != nil {
		prog.Stage = "failed"
		prog.Log = append(prog.Log, "! "+err.Error())
	} else {
		prog.Stage, prog.Percent = "done", 100
	}
}

// GET /progress?from=N: the state plus log lines from index N on.
func progressHandler(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	prog.mu.Lock()
	defer prog.mu.Unlock()
	if from < 0 || from > len(prog.Log) {
		from = len(prog.Log)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "running": prog.Running, "name": prog.Name, "stage": prog.Stage,
		"done": prog.Done, "total": prog.Total, "percent": prog.Percent, "started": prog.Started,
		"lines": append([]string{}, prog.Log[from:]...), "next": len(prog.Log),
	})
}

// countingReader reports bytes read to the progress state.
type countingReader struct {
	r     io.Reader
	n     int64
	total int64
	last  time.Time
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if time.Since(c.last) > 200*time.Millisecond || err != nil {
		c.last = time.Now()
		progBytes(c.n, c.total)
	}
	return n, err
}

// ---------------------------------------------------------------- app IDs

// A free Apple ID may create 10 app IDs per 7 days; each lives 7 days.
const freeAppIDs = 10

var (
	appIDsMu    sync.Mutex
	appIDsCache map[string]any
	appIDsAt    time.Time
	idRe        = regexp.MustCompile(`identifier: "([^"]+)"`)
	nameRe      = regexp.MustCompile(`\bname: "([^"]*)"`)
)

// GET /appids: the app IDs the Apple ID currently holds (cached 5 minutes,
// plumesign has to log in to Apple for it).
func appIDsHandler(w http.ResponseWriter, r *http.Request) {
	appIDsMu.Lock()
	defer appIDsMu.Unlock()
	if appIDsCache != nil && time.Since(appIDsAt) < 5*time.Minute && r.URL.Query().Get("fresh") == "" {
		writeJSON(w, http.StatusOK, appIDsCache)
		return
	}
	cmd := exec.Command(plumesign, "account", "app-ids")
	cmd.Env = append(os.Environ(), "RUST_LOG=info")
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "plumesign: " + lastLine(string(out), err)})
		return
	}
	type appID struct {
		Name       string `json:"name"`
		Identifier string `json:"identifier"`
	}
	ids := []appID{}
	// The output is Rust debug format, one "AppID { ... }" block per ID.
	for _, block := range strings.Split(string(out), "AppID {")[1:] {
		id := idRe.FindStringSubmatch(block)
		if id == nil {
			continue
		}
		a := appID{Identifier: id[1]}
		if n := nameRe.FindStringSubmatch(block); n != nil {
			a.Name = n[1]
		}
		ids = append(ids, a)
	}
	appIDsCache = map[string]any{"ok": true, "ids": ids, "limit": freeAppIDs}
	appIDsAt = time.Now()
	writeJSON(w, http.StatusOK, appIDsCache)
}

func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(logPrefix.ReplaceAllString(lines[len(lines)-1], "")); l != "" {
		return l
	}
	return err.Error()
}
