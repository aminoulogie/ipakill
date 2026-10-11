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

// Live state of the install in progress. The iPhone polls this rapidly to
// render a live console plus a phase-local 1.00 -> 100.00 progress bar.
type progressState struct {
	mu      sync.Mutex
	Running bool     `json:"running"`
	Name    string   `json:"name"`
	Stage   string   `json:"stage"` // download, upload, sign, install, done, failed
	Done    int64    `json:"done"`
	Total   int64    `json:"total"`
	Percent float64  `json:"percent"`
	Log     []string `json:"-"`
	Started int64    `json:"started"` // unix milliseconds

	phaseStarted time.Time
}

var (
	prog       progressState
	logPrefix  = regexp.MustCompile(`^\[[^\]]*\]\s*`)
	installPct = regexp.MustCompile(`Installation progress:\s*(\d+(?:\.\d+)?)%`)
)

func prettyStage(stage string) string {
	switch stage {
	case "download":
		return "Downloading"
	case "upload":
		return "Uploading"
	case "sign":
		return "Signing"
	case "install":
		return "Installing"
	case "done":
		return "Complete"
	case "failed":
		return "Failed"
	default:
		if stage == "" {
			return "Preparing"
		}
		return strings.ToUpper(stage[:1]) + stage[1:]
	}
}

func progStart(name, stage string) {
	now := time.Now()
	prog.mu.Lock()
	defer prog.mu.Unlock()
	prog.Running, prog.Name, prog.Stage = true, name, stage
	prog.Done, prog.Total, prog.Percent = 0, 0, 1
	prog.Log = nil
	prog.Started = now.UnixMilli()
	prog.phaseStarted = now
	prog.Log = append(prog.Log, "[phase] "+prettyStage(stage)+" 1.00%")
}

func progStage(stage string) {
	prog.mu.Lock()
	defer prog.mu.Unlock()
	if prog.Stage == stage {
		return
	}
	if prog.Stage != "" && prog.Stage != "done" && prog.Stage != "failed" {
		if len(prog.Log) < 5000 {
			prog.Log = append(prog.Log, "[phase] "+prettyStage(prog.Stage)+" 100.00%")
		}
	}
	prog.Stage = stage
	prog.Done, prog.Total, prog.Percent = 0, 0, 1
	prog.phaseStarted = time.Now()
	if len(prog.Log) < 5000 {
		prog.Log = append(prog.Log, "[phase] "+prettyStage(stage)+" 1.00%")
	}
}

func progBytes(done, total int64) {
	prog.mu.Lock()
	defer prog.mu.Unlock()
	prog.Done, prog.Total = done, total
	if total > 0 {
		p := float64(done) * 100 / float64(total)
		if p < 1 {
			p = 1
		}
		if p > 100 {
			p = 100
		}
		prog.Percent = p
	}
}

func bumpSignLocked(target float64) {
	if prog.Stage == "sign" && target > prog.Percent {
		prog.Percent = target
	}
}

// progLine records every raw plumesign line. It also extracts real milestones
// to make signing progress useful without pretending plumesign has a byte meter.
func progLine(line string) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return
	}
	clean := strings.TrimSpace(logPrefix.ReplaceAllString(raw, ""))

	prog.mu.Lock()
	defer prog.mu.Unlock()

	lower := strings.ToLower(clean)
	if prog.Stage == "sign" {
		switch {
		case strings.Contains(lower, "restoring session"):
			bumpSignLocked(8)
		case strings.Contains(lower, "registering device"):
			bumpSignLocked(16)
		case strings.Contains(lower, "provision"):
			bumpSignLocked(32)
		case strings.Contains(lower, "certificate"):
			bumpSignLocked(42)
		case strings.Contains(lower, "entitlement"):
			bumpSignLocked(55)
		case strings.Contains(lower, "signing"):
			next := prog.Percent + 4
			if next > 92 {
				next = 92
			}
			bumpSignLocked(next)
		}
	}

	if m := installPct.FindStringSubmatch(clean); m != nil {
		if prog.Stage != "install" {
			if prog.Stage == "sign" && len(prog.Log) < 5000 {
				prog.Log = append(prog.Log, "[phase] Signing 100.00%")
			}
			prog.Stage = "install"
			prog.Percent = 1
			prog.Done, prog.Total = 0, 0
			prog.phaseStarted = time.Now()
			if len(prog.Log) < 5000 {
				prog.Log = append(prog.Log, "[phase] Installing 1.00%")
			}
		}
		if p, err := strconv.ParseFloat(m[1], 64); err == nil {
			if p < 1 {
				p = 1
			}
			if p > 100 {
				p = 100
			}
			prog.Percent = p
		}
	}

	if len(prog.Log) < 5000 {
		prog.Log = append(prog.Log, raw)
	}
}

func progEnd(err error) {
	prog.mu.Lock()
	defer prog.mu.Unlock()
	prog.Running = false
	if err != nil {
		prog.Stage = "failed"
		if prog.Percent < 1 {
			prog.Percent = 1
		}
		prog.Log = append(prog.Log, "! "+err.Error())
		return
	}

	if prog.Stage != "done" && prog.Stage != "" && len(prog.Log) < 5000 {
		prog.Log = append(prog.Log, "[phase] "+prettyStage(prog.Stage)+" 100.00%")
	}
	prog.Stage, prog.Percent = "done", 100
	prog.phaseStarted = time.Now()
	if len(prog.Log) < 5000 {
		prog.Log = append(prog.Log, "[phase] Complete 100.00%")
	}
}

func phasePercentLocked(now time.Time) float64 {
	p := prog.Percent
	elapsed := now.Sub(prog.phaseStarted).Seconds()

	switch prog.Stage {
	case "download", "upload":
		if prog.Total > 0 {
			p = float64(prog.Done) * 100 / float64(prog.Total)
		} else if prog.Running {
			// Content-Length can be unknown. Keep moving, but reserve completion
			// for the real transition to the next phase.
			smooth := 1 + elapsed*2
			if smooth > 95 {
				smooth = 95
			}
			if smooth > p {
				p = smooth
			}
		}
	case "sign":
		// plumesign gives milestones but no continuous percent. Smooth between
		// those real milestones, capped below 100 until installation actually starts.
		smooth := 1 + elapsed*1.6
		if smooth > 96.5 {
			smooth = 96.5
		}
		if smooth > p {
			p = smooth
		}
	case "done":
		p = 100
	}

	if p < 1 && prog.Running {
		p = 1
	}
	if p > 100 {
		p = 100
	}
	return p
}

// GET /progress?from=N returns current state and only new console lines.
func progressHandler(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	prog.mu.Lock()
	defer prog.mu.Unlock()
	if from < 0 || from > len(prog.Log) {
		from = 0
	}

	now := time.Now()
	percent := phasePercentLocked(now)
	speed := float64(0)
	if (prog.Stage == "download" || prog.Stage == "upload") && !prog.phaseStarted.IsZero() {
		seconds := now.Sub(prog.phaseStarted).Seconds()
		if seconds > 0 {
			speed = float64(prog.Done) / seconds
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "running": prog.Running, "name": prog.Name, "stage": prog.Stage,
		"done": prog.Done, "total": prog.Total, "percent": percent, "started": prog.Started,
		"bytesPerSecond": speed,
		"lines": append([]string{}, prog.Log[from:]...), "next": len(prog.Log),
	})
}

// countingReader reports transfer progress frequently enough for a responsive UI.
type countingReader struct {
	r     io.Reader
	n     int64
	total int64
	last  time.Time
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if time.Since(c.last) > 50*time.Millisecond || err != nil {
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
	if len(lines) > 0 {
		if l := strings.TrimSpace(logPrefix.ReplaceAllString(lines[len(lines)-1], "")); l != "" {
			return l
		}
	}
	return err.Error()
}
