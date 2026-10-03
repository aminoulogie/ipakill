// ipakill-core: signs + installs .ipa files through plumesign, tracks when each
// app's free 7-day signature runs out, and serves the ipakill iPhone app over Wi-Fi.
//
//	ipakill-core install <app.ipa> [plumesign flags]   sign + install (USB preferred, Wi-Fi otherwise)
//	ipakill-core list                show installed apps and days left
//	ipakill-core devices             show iPhones reachable over USB / Wi-Fi
//	ipakill-core serve               run the Wi-Fi sync server for the iPhone app
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	port       = 7777
	signedFor  = 7 * 24 * time.Hour // free Apple ID profiles last 7 days
	usbmuxAddr = "127.0.0.1:27015"  // Apple Mobile Device Service on Windows
)

var (
	home      = filepath.Join(os.Getenv("USERPROFILE"), ".ipakill")
	plumesign = filepath.Join(home, "tools", "plumesign.exe")
	appsFile  = filepath.Join(home, "apps.json")
	syncFile  = filepath.Join(home, "sync.json")
	ipaDir    = filepath.Join(home, "apps")
	logFile   = filepath.Join(home, "last.log")

	bundleRe  = regexp.MustCompile(`binary identifier to (\S+) \(derived`)
	versionRe = regexp.MustCompile(`(?i)[-_ ]v?\d[\w.\-() ]*$`)
	semverRe  = regexp.MustCompile(`\d+(\.\d+)+(-[0-9A-Za-z.]+)?`)
	installMu sync.Mutex
)

type App struct {
	Name    string    `json:"name"`
	Bundle  string    `json:"bundle"`
	Signed  time.Time `json:"signed"`
	Expires time.Time `json:"expires"`
	IPA     string    `json:"ipa"`
	Version string    `json:"version,omitempty"`
}

// Meta names an install; empty fields are guessed from the .ipa file name.
type Meta struct {
	Name, Version string
}

type Device struct {
	ID   int
	UDID string
	Conn string // "USB" or "Network"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: ipakill-core install <app.ipa> | list | serve")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "install":
		if len(os.Args) < 3 {
			err = fmt.Errorf("missing .ipa path")
			break
		}
		_, err = install(os.Args[2], os.Stdout, Meta{}, os.Args[3:]...)
	case "list":
		printList()
	case "devices":
		var devs []Device
		if devs, err = listDevices(); err == nil {
			if len(devs) == 0 {
				fmt.Println("[ipakill] No iPhone found over USB or Wi-Fi.")
			}
			for _, d := range devs {
				fmt.Printf("  %-8s %s  (#%d)\n", map[string]string{"USB": "USB", "Network": "Wi-Fi"}[d.Conn], d.UDID, d.ID)
			}
		}
	case "wifi":
		var want *bool
		if len(os.Args) > 2 {
			v := os.Args[2] == "on"
			want = &v
		}
		var on bool
		if on, err = setWifiSync(want); err == nil {
			if on {
				fmt.Println("[ipakill] Wi-Fi sync is ON - you can unplug, installs work over Wi-Fi.")
			} else {
				fmt.Println("[ipakill] Wi-Fi sync is OFF. Turn it on with: ipakill wifi on")
			}
		}
	case "serve":
		err = serve()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ipakill] "+err.Error())
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- devices

func dialMux() (net.Conn, error) {
	c, err := net.DialTimeout("tcp", usbmuxAddr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("Apple Mobile Device Service not reachable (is iTunes installed?)")
	}
	return c, nil
}

// muxRequest sends one plist message to usbmuxd and returns its plist reply.
// fields is the inner XML of the request dict.
func muxRequest(c net.Conn, messageType, fields string) (string, error) {
	c.SetDeadline(time.Now().Add(5 * time.Second))
	defer c.SetDeadline(time.Time{})
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` +
		`<key>MessageType</key><string>` + messageType + `</string>` +
		`<key>ClientVersionString</key><string>ipakill</string>` +
		`<key>ProgName</key><string>ipakill</string>` + fields + `</dict></plist>`)
	hdr := make([]byte, 16)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(16+len(body)))
	binary.LittleEndian.PutUint32(hdr[4:], 1) // version: plist
	binary.LittleEndian.PutUint32(hdr[8:], 8) // message: plist
	binary.LittleEndian.PutUint32(hdr[12:], 1)
	if _, err := c.Write(append(hdr, body...)); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(c, hdr); err != nil {
		return "", err
	}
	resp := make([]byte, binary.LittleEndian.Uint32(hdr[0:])-16)
	if _, err := io.ReadFull(c, resp); err != nil {
		return "", err
	}
	return string(resp), nil
}

// listDevices asks Apple's usbmuxd which iPhones it can reach, over USB or Wi-Fi.
func listDevices() ([]Device, error) {
	c, err := dialMux()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	resp, err := muxRequest(c, "ListDevices", "")
	if err != nil {
		return nil, err
	}

	var devs []Device
	// Each attached device has exactly one Properties dict holding everything we need.
	for _, chunk := range strings.Split(resp, "<key>Properties</key>")[1:] {
		d := Device{
			UDID: plistString(chunk, "SerialNumber"),
			Conn: plistString(chunk, "ConnectionType"),
		}
		if m := regexp.MustCompile(`<key>DeviceID</key>\s*<integer>(\d+)</integer>`).FindStringSubmatch(chunk); m != nil {
			d.ID, _ = strconv.Atoi(m[1])
		}
		devs = append(devs, d)
	}
	return devs, nil
}

func plistString(s, key string) string {
	m := regexp.MustCompile(`<key>` + key + `</key>\s*<string>([^<]*)</string>`).FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// pickDevice prefers a USB connection, falling back to Wi-Fi.
func pickDevice() (Device, error) {
	devs, err := listDevices()
	if err != nil {
		return Device{}, err
	}
	if len(devs) == 0 {
		return Device{}, fmt.Errorf("no iPhone found over USB or Wi-Fi")
	}
	sort.SliceStable(devs, func(i, j int) bool { return devs[i].Conn == "USB" && devs[j].Conn != "USB" })
	return devs[0], nil
}

// ---------------------------------------------------------------- install

// install signs and installs ipa, streaming the useful log lines to out.
// extra is passed straight to plumesign (e.g. --custom-identifier).
func install(ipa string, out io.Writer, meta Meta, extra ...string) (App, error) {
	installMu.Lock()
	defer installMu.Unlock()

	ipa, _ = filepath.Abs(ipa)
	if _, err := os.Stat(ipa); err != nil {
		return App{}, fmt.Errorf("IPA not found: %s", ipa)
	}
	if _, err := os.Stat(plumesign); err != nil {
		return App{}, fmt.Errorf("plumesign missing - run 'ipakill login' first")
	}
	dev, err := pickDevice()
	if err != nil {
		return App{}, err
	}
	conn := map[string]string{"USB": "USB", "Network": "Wi-Fi"}[dev.Conn]
	fmt.Fprintf(out, "[ipakill] Installing to %s over %s\n", dev.UDID, conn)

	args := append([]string{"sign", "-p", ipa, "--apple-id", "--register-and-install",
		"--udid", strconv.Itoa(dev.ID)}, extra...)
	cmd := exec.Command(plumesign, args...)
	cmd.Env = append(os.Environ(), "RUST_LOG=info")
	pipe, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return App{}, err
	}

	logf, _ := os.Create(logFile)
	defer logf.Close()
	bundle := ""
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(logf, line)
		if m := bundleRe.FindStringSubmatch(line); m != nil {
			bundle = m[1] // the main app is signed last, so the last match wins
		}
		if strings.Contains(line, "plumesign::") || strings.Contains(line, "ERROR") || strings.HasPrefix(line, "Error") {
			if i := strings.Index(line, "] "); i >= 0 && strings.HasPrefix(line, "[") {
				line = line[i+2:]
			}
			fmt.Fprintln(out, "  "+line)
		}
	}
	if err := cmd.Wait(); err != nil {
		return App{}, fmt.Errorf("install failed - full log: %s", logFile)
	}

	base := strings.TrimSuffix(filepath.Base(ipa), filepath.Ext(ipa))
	name, version := meta.Name, meta.Version
	if name == "" {
		name = base
		if n := versionRe.ReplaceAllString(base, ""); n != "" {
			name = n
		}
	}
	if version == "" {
		version = semverRe.FindString(base)
	}
	now := time.Now().Truncate(time.Second) // whole seconds keep the JSON dates easy for iOS to parse
	app := App{Name: name, Bundle: bundle, Signed: now, Expires: now.Add(signedFor), IPA: ipa, Version: version}
	apps := loadApps()
	apps[keyFor(app)] = app
	saveApps(apps)
	fmt.Fprintf(out, "[ipakill] Done! %s is signed until %s.\n", name, app.Expires.Format("Mon Jan 2 15:04"))
	return app, nil
}

func keyFor(a App) string {
	if a.Bundle != "" {
		return a.Bundle
	}
	return a.Name
}

func loadApps() map[string]App {
	apps := map[string]App{}
	if b, err := os.ReadFile(appsFile); err == nil {
		json.Unmarshal(b, &apps)
	}
	return apps
}

func saveApps(apps map[string]App) {
	os.MkdirAll(home, 0o755)
	b, _ := json.MarshalIndent(apps, "", "  ")
	os.WriteFile(appsFile, b, 0o644)
}

func sortedApps() []App {
	var list []App
	for _, a := range loadApps() {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Expires.Before(list[j].Expires) })
	return list
}

func printList() {
	list := sortedApps()
	if len(list) == 0 {
		fmt.Println("[ipakill] No apps installed with ipakill yet.")
		return
	}
	for _, a := range list {
		left := time.Until(a.Expires)
		status := fmt.Sprintf("%.1f days left", left.Hours()/24)
		if left <= 0 {
			status = "EXPIRED - run ipakill again"
		}
		fmt.Printf("  %-16s %-10s %-16s %s\n", a.Name, a.Version, status, a.Bundle)
	}
}

// ---------------------------------------------------------------- Wi-Fi server

type syncConfig struct {
	Code string `json:"code"`
}

func loadSyncConfig() syncConfig {
	var c syncConfig
	if b, err := os.ReadFile(syncFile); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Code == "" {
		n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
		c.Code = fmt.Sprintf("%06d", n.Int64())
		os.MkdirAll(home, 0o755)
		b, _ := json.MarshalIndent(c, "", "  ")
		os.WriteFile(syncFile, b, 0o600)
	}
	return c
}

func localIPs() []string {
	var ips []string
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		// Skip WSL / Hyper-V virtual adapters - the iPhone can't reach those.
		if iface.Flags&net.FlagUp == 0 || strings.HasPrefix(iface.Name, "vEthernet") {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
				ips = append(ips, n.IP.String())
			}
		}
	}
	return ips
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func installAndReply(w http.ResponseWriter, ipa string, meta Meta) {
	var log bytes.Buffer
	app, err := install(ipa, io.MultiWriter(os.Stdout, &log), meta)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "log": log.String()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": app, "log": log.String()})
}

func download(src, dst string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(src)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server said %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 4<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func serve() error {
	cfg := loadSyncConfig()
	os.MkdirAll(ipaDir, 0o755)
	host, _ := os.Hostname()

	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Images can't carry headers, so the code may also come as ?code=.
			if r.Header.Get("X-Ipakill-Code") != cfg.Code && r.URL.Query().Get("code") != cfg.Code {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "wrong pairing code"})
				return
			}
			h(w, r)
		}
	}

	http.HandleFunc("/status", auth(func(w http.ResponseWriter, r *http.Request) {
		phone := ""
		if devs, err := listDevices(); err == nil && len(devs) > 0 {
			d, _ := pickDevice()
			phone = map[string]string{"USB": "usb", "Network": "wifi"}[d.Conn]
		}
		apps := sortedApps()
		if apps == nil {
			apps = []App{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pc": host, "phone": phone, "apps": apps})
	}))

	http.HandleFunc("/install", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		name := filepath.Base(r.URL.Query().Get("name"))
		if !strings.HasSuffix(strings.ToLower(name), ".ipa") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "not an .ipa file"})
			return
		}
		dst := filepath.Join(ipaDir, name)
		f, err := os.Create(dst)
		if err == nil {
			_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, 4<<30))
			f.Close()
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "upload failed: " + err.Error()})
			return
		}
		fmt.Printf("[ipakill] Received %s from iPhone\n", name)
		installAndReply(w, dst, Meta{})
	}))

	// The icon of an installed app, read straight out of its .ipa.
	http.HandleFunc("/icon", auth(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("app")
		for _, a := range loadApps() {
			if keyFor(a) != key {
				continue
			}
			png, err := ipaIcon(a.IPA)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "max-age=86400")
			w.Write(png)
			return
		}
		http.NotFound(w, r)
	}))

	// Store installs: the PC downloads the .ipa itself, so nothing big goes over the phone.
	http.HandleFunc("/install-url", auth(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		src := q.Get("url")
		if !strings.HasPrefix(src, "https://") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "source link must be https"})
			return
		}
		name := filepath.Base(strings.SplitN(src, "?", 2)[0])
		if !strings.HasSuffix(strings.ToLower(name), ".ipa") {
			name = "download.ipa"
		}
		fmt.Printf("[ipakill] Downloading %s\n", src)
		dst := filepath.Join(ipaDir, name)
		if err := download(src, dst); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "download failed: " + err.Error()})
			return
		}
		installAndReply(w, dst, Meta{Name: q.Get("name"), Version: q.Get("version")})
	}))

	// The signing certificate, for running apps inside ipakill (see cert.go).
	http.HandleFunc("/cert", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		profile, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad upload"})
			return
		}
		p12, pass, err := signingP12(profile)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		fmt.Println("[ipakill] Sent the signing certificate to the iPhone")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "p12": p12, "password": pass})
	}))

	fmt.Println("[ipakill] Wi-Fi sync is running. In the ipakill iPhone app, enter:")
	for _, ip := range localIPs() {
		fmt.Printf("            PC address:   %s\n", ip)
	}
	fmt.Printf("            Pairing code: %s\n", cfg.Code)
	fmt.Println("[ipakill] Keep this window open. Ctrl+C to stop.")
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
