package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ipaLabDir = filepath.Join(home, "lab")

type labItem struct {
	Path         string   `json:"path"`
	Kind         string   `json:"kind"`
	Size         uint64   `json:"size"`
	Architecture string   `json:"architecture,omitempty"`
	LoadCommands []string `json:"loadCommands,omitempty"`
	RPaths       []string `json:"rpaths,omitempty"`
}

type labReport struct {
	OK           bool      `json:"ok"`
	Session      string    `json:"session,omitempty"`
	AppRoot      string    `json:"appRoot,omitempty"`
	MainBinary   string    `json:"mainBinary,omitempty"`
	Items        []labItem `json:"items,omitempty"`
	Warnings     []string  `json:"warnings,omitempty"`
	Error        string    `json:"error,omitempty"`
	Modified     bool      `json:"modified"`
}

type machoInfo struct {
	arch   string
	loads  []string
	rpaths []string
	thin64 bool
}

func registerIPALabRoutes(auth func(http.HandlerFunc) http.HandlerFunc) {
	os.MkdirAll(ipaLabDir, 0o755)

	guard := func(fn http.HandlerFunc) http.HandlerFunc {
		return auth(func(w http.ResponseWriter, r *http.Request) {
			if !isLANRemote(r.RemoteAddr) {
				writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "IPA Lab is limited to the local network"})
				return
			}
			fn(w, r)
		})
	}

	http.HandleFunc("/lab/open", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		dir := filepath.Join(ipaLabDir, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		dst := filepath.Join(dir, "working.ipa")
		f, err := os.Create(dst)
		if err == nil {
			_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, 4<<30))
			_ = f.Close()
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			writeJSON(w, 500, map[string]any{"ok": false, "error": "upload failed: " + err.Error()})
			return
		}
		if err := copyFile(dst, filepath.Join(dir, "original.ipa")); err != nil {
			_ = os.RemoveAll(dir)
			writeJSON(w, 500, map[string]any{"ok": false, "error": "snapshot failed: " + err.Error()})
			return
		}
		rep, err := inspectIPA(dst)
		if err != nil {
			_ = os.RemoveAll(dir)
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rep.Session, rep.OK = id, true
		writeJSON(w, 200, rep)
	}))

	http.HandleFunc("/lab/report", guard(func(w http.ResponseWriter, r *http.Request) {
		ipa, id, err := labSessionIPA(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rep, err := inspectIPA(ipa)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rep.Session, rep.OK = id, true
		writeJSON(w, 200, rep)
	}))

	http.HandleFunc("/lab/extract", guard(func(w http.ResponseWriter, r *http.Request) {
		ipa, _, err := labSessionIPA(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		want := cleanArchivePath(r.URL.Query().Get("path"))
		data, err := zipReadFile(ipa, want)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(want)+`"`)
		w.Write(data)
	}))

	http.HandleFunc("/lab/inject", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		ipa, id, err := labSessionIPA(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		name := filepath.Base(r.URL.Query().Get("name"))
		if !strings.HasSuffix(strings.ToLower(name), ".dylib") {
			writeJSON(w, 400, map[string]any{"ok": false, "error": "injected file must be a .dylib"})
			return
		}
		dylib, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<20))
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		mi, err := parseMachO(dylib)
		if err != nil || mi.arch != "arm64" {
			writeJSON(w, 400, map[string]any{"ok": false, "error": "dylib must be a thin arm64 Mach-O"})
			return
		}
		weak := r.URL.Query().Get("weak") == "1"
		if err := injectDylibIntoIPA(ipa, name, dylib, weak); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rep, _ := inspectIPA(ipa)
		rep.OK, rep.Session, rep.Modified = true, id, true
		writeJSON(w, 200, rep)
	}))

	http.HandleFunc("/lab/replace", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		ipa, id, err := labSessionIPA(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		want := cleanArchivePath(r.URL.Query().Get("path"))
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<20))
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := rewriteZIP(ipa, map[string][]byte{want: data}, nil); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rep, _ := inspectIPA(ipa)
		rep.OK, rep.Session, rep.Modified = true, id, true
		writeJSON(w, 200, rep)
	}))

	http.HandleFunc("/lab/remove", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		ipa, id, err := labSessionIPA(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		want := cleanArchivePath(r.URL.Query().Get("path"))
		rep, err := inspectIPA(ipa)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		base := filepath.Base(want)
		mainData, _ := zipReadFile(ipa, rep.MainBinary)
		mainInfo, _ := parseMachO(mainData)
		for _, load := range mainInfo.loads {
			if filepath.Base(load) == base {
				writeJSON(w, 409, map[string]any{
					"ok": false,
					"error": "refusing to remove a library still referenced by the main executable; remove its load command first",
				})
				return
			}
		}
		if err := rewriteZIP(ipa, nil, map[string]bool{want: true}); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		rep, _ = inspectIPA(ipa)
		rep.OK, rep.Session, rep.Modified = true, id, true
		writeJSON(w, 200, rep)
	}))

	http.HandleFunc("/lab/reset", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"ok": false, "error": "POST only"})
			return
		}
		ipa, id, err := labSessionIPA(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		original := filepath.Join(ipaLabDir, id, "original.ipa")
		if err := copyFile(original, ipa); err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "error": "reset failed: " + err.Error()})
			return
		}
		rep, _ := inspectIPA(ipa)
		rep.OK, rep.Session, rep.Modified = true, id, false
		writeJSON(w, 200, rep)
	}))

	http.HandleFunc("/lab/diff", guard(func(w http.ResponseWriter, r *http.Request) {
		ipa, id, err := labSessionIPA(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		original := filepath.Join(ipaLabDir, id, "original.ipa")
		added, removed, changed, err := diffIPA(original, ipa)
		if err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "added": added, "removed": removed, "changed": changed})
	}))

	http.HandleFunc("/lab/export", guard(func(w http.ResponseWriter, r *http.Request) {
		ipa, _, err := labSessionIPA(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f, err := os.Open(ipa)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="ipakill-modified.ipa"`)
		io.Copy(w, f)
	}))
}

func labSessionIPA(r *http.Request) (string, string, error) {
	id := filepath.Base(strings.TrimSpace(r.URL.Query().Get("session")))
	if id == "" || id == "." {
		return "", "", fmt.Errorf("missing session")
	}
	ipa := filepath.Join(ipaLabDir, id, "working.ipa")
	if _, err := os.Stat(ipa); err != nil {
		return "", "", fmt.Errorf("session not found")
	}
	return ipa, id, nil
}

func cleanArchivePath(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+s)), "/")
	return s
}

func inspectIPA(path string) (labReport, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return labReport{}, fmt.Errorf("invalid IPA: %w", err)
	}
	defer zr.Close()

	rep := labReport{Items: []labItem{}, Warnings: []string{}}
	var appRoot string
	for _, f := range zr.File {
		n := cleanArchivePath(f.Name)
		if strings.HasPrefix(n, "Payload/") {
			parts := strings.Split(n, "/")
			if len(parts) >= 2 && strings.HasSuffix(parts[1], ".app") {
				appRoot = strings.Join(parts[:2], "/") + "/"
				break
			}
		}
	}
	if appRoot == "" {
		return rep, fmt.Errorf("Payload/*.app not found")
	}
	rep.AppRoot = appRoot

	var candidates []string
	for _, f := range zr.File {
		n := cleanArchivePath(f.Name)
		if f.FileInfo().IsDir() || !strings.HasPrefix(n, appRoot) {
			continue
		}
		kind := ""
		lower := strings.ToLower(n)
		if strings.HasSuffix(lower, ".dylib") {
			kind = "dylib"
		} else if strings.Contains(lower, ".framework/") && filepath.Base(n) == strings.TrimSuffix(filepath.Base(filepath.Dir(n)), ".framework") {
			kind = "framework"
		}
		if strings.Count(strings.TrimPrefix(n, appRoot), "/") == 0 {
			candidates = append(candidates, n)
		}
		if kind == "" {
			continue
		}
		data, e := readZipEntry(f)
		item := labItem{Path: n, Kind: kind, Size: f.UncompressedSize64}
		if e == nil {
			if mi, e2 := parseMachO(data); e2 == nil {
				item.Architecture, item.LoadCommands, item.RPaths = mi.arch, mi.loads, mi.rpaths
			}
		}
		rep.Items = append(rep.Items, item)
	}

	for _, c := range candidates {
		data, e := zipReadFileFromReader(&zr.Reader, c)
		if e != nil {
			continue
		}
		if mi, e := parseMachO(data); e == nil && mi.thin64 {
			rep.MainBinary = c
			item := labItem{Path: c, Kind: "executable", Size: uint64(len(data)), Architecture: mi.arch, LoadCommands: mi.loads, RPaths: mi.rpaths}
			rep.Items = append([]labItem{item}, rep.Items...)
			break
		}
	}
	if rep.MainBinary == "" {
		rep.Warnings = append(rep.Warnings, "Main Mach-O executable could not be identified automatically.")
	}
	for i := range rep.Items {
		if rep.Items[i].Architecture != "" && rep.Items[i].Architecture != "arm64" {
			rep.Warnings = append(rep.Warnings, rep.Items[i].Path+" is "+rep.Items[i].Architecture+"; direct injection currently supports thin arm64 only.")
		}
	}
	sort.SliceStable(rep.Items, func(i, j int) bool {
		if rep.Items[i].Kind == "executable" {
			return true
		}
		if rep.Items[j].Kind == "executable" {
			return false
		}
		return rep.Items[i].Path < rep.Items[j].Path
	})
	return rep, nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func zipReadFile(path, name string) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return zipReadFileFromReader(&zr.Reader, name)
}

func zipReadFileFromReader(zr *zip.Reader, name string) ([]byte, error) {
	name = cleanArchivePath(name)
	for _, f := range zr.File {
		if cleanArchivePath(f.Name) == name {
			return readZipEntry(f)
		}
	}
	return nil, os.ErrNotExist
}

func parseMachO(data []byte) (machoInfo, error) {
	if len(data) < 32 {
		return machoInfo{}, fmt.Errorf("not Mach-O")
	}
	magic := binary.LittleEndian.Uint32(data[:4])
	if magic != 0xfeedfacf {
		if binary.BigEndian.Uint32(data[:4]) == 0xcafebabe || binary.LittleEndian.Uint32(data[:4]) == 0xbebafeca {
			return machoInfo{arch: "universal/fat"}, nil
		}
		return machoInfo{}, fmt.Errorf("unsupported Mach-O")
	}
	cpu := binary.LittleEndian.Uint32(data[4:8])
	arch := fmt.Sprintf("cpu-0x%x", cpu)
	if cpu == 0x0100000c {
		arch = "arm64"
	}
	ncmds := binary.LittleEndian.Uint32(data[16:20])
	sizeofcmds := binary.LittleEndian.Uint32(data[20:24])
	if 32+int(sizeofcmds) > len(data) {
		return machoInfo{}, fmt.Errorf("truncated load commands")
	}
	info := machoInfo{arch: arch, thin64: true}
	off := 32
	for i := uint32(0); i < ncmds; i++ {
		if off+8 > len(data) {
			return machoInfo{}, fmt.Errorf("truncated load command")
		}
		cmd := binary.LittleEndian.Uint32(data[off : off+4])
		sz := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if sz < 8 || off+sz > len(data) {
			return machoInfo{}, fmt.Errorf("invalid load command")
		}
		switch cmd {
		case 0x0c, 0x80000018, 0x8000001f, 0x80000023, 0x0d:
			if sz >= 24 {
				no := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
				if no >= 0 && no < sz {
					s := cString(data[off+no : off+sz])
					if s != "" && cmd != 0x0d {
						info.loads = append(info.loads, s)
					}
				}
			}
		case 0x8000001c:
			if sz >= 12 {
				po := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
				if po >= 0 && po < sz {
					if s := cString(data[off+po : off+sz]); s != "" {
						info.rpaths = append(info.rpaths, s)
					}
				}
			}
		}
		off += sz
	}
	return info, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func injectDylibIntoIPA(ipa, name string, dylib []byte, weak bool) error {
	rep, err := inspectIPA(ipa)
	if err != nil {
		return err
	}
	if rep.MainBinary == "" {
		return fmt.Errorf("main executable not found")
	}
	mainData, err := zipReadFile(ipa, rep.MainBinary)
	if err != nil {
		return err
	}
	loadPath := "@rpath/" + name
	patched, err := addDylibLoad(mainData, loadPath, weak)
	if err != nil {
		return err
	}
	dst := rep.AppRoot + "Frameworks/" + name
	return rewriteZIP(ipa, map[string][]byte{rep.MainBinary: patched, dst: dylib}, nil)
}

func addDylibLoad(data []byte, installName string, weak bool) ([]byte, error) {
	info, err := parseMachO(data)
	if err != nil {
		return nil, err
	}
	if !info.thin64 || info.arch != "arm64" {
		return nil, fmt.Errorf("main executable must be thin arm64")
	}
	for _, s := range info.loads {
		if s == installName {
			return data, nil
		}
	}
	ncmds := binary.LittleEndian.Uint32(data[16:20])
	sizeofcmds := binary.LittleEndian.Uint32(data[20:24])
	cmdEnd := 32 + int(sizeofcmds)

	firstFileOff := len(data)
	off := 32
	for i := uint32(0); i < ncmds; i++ {
		cmd := binary.LittleEndian.Uint32(data[off : off+4])
		sz := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if (cmd == 0x19 || cmd == 0x1) && sz >= 56 {
			fileoff := int(binary.LittleEndian.Uint64(data[off+40 : off+48]))
			if fileoff > 0 && fileoff < firstFileOff {
				firstFileOff = fileoff
			}
		}
		off += sz
	}
	nameBytes := append([]byte(installName), 0)
	cmdSize := align8(24 + len(nameBytes))
	if firstFileOff-cmdEnd < cmdSize {
		return nil, fmt.Errorf("not enough Mach-O header padding for a new LC_LOAD_DYLIB (%d bytes needed, %d available)", cmdSize, firstFileOff-cmdEnd)
	}
	out := append([]byte(nil), data...)
	cmd := uint32(0x0c)
	if weak {
		cmd = 0x80000018
	}
	binary.LittleEndian.PutUint32(out[cmdEnd:cmdEnd+4], cmd)
	binary.LittleEndian.PutUint32(out[cmdEnd+4:cmdEnd+8], uint32(cmdSize))
	binary.LittleEndian.PutUint32(out[cmdEnd+8:cmdEnd+12], 24)
	binary.LittleEndian.PutUint32(out[cmdEnd+12:cmdEnd+16], 2)
	binary.LittleEndian.PutUint32(out[cmdEnd+16:cmdEnd+20], 0)
	binary.LittleEndian.PutUint32(out[cmdEnd+20:cmdEnd+24], 0)
	copy(out[cmdEnd+24:cmdEnd+24+len(nameBytes)], nameBytes)
	for i := cmdEnd + 24 + len(nameBytes); i < cmdEnd+cmdSize; i++ {
		out[i] = 0
	}
	binary.LittleEndian.PutUint32(out[16:20], ncmds+1)
	binary.LittleEndian.PutUint32(out[20:24], sizeofcmds+uint32(cmdSize))
	return out, nil
}

func align8(n int) int { return (n + 7) &^ 7 }

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

type zipFingerprint struct {
	size uint64
	crc  uint32
}

func ipaFingerprints(path string) (map[string]zipFingerprint, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out := map[string]zipFingerprint{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		out[cleanArchivePath(f.Name)] = zipFingerprint{size: f.UncompressedSize64, crc: f.CRC32}
	}
	return out, nil
}

func diffIPA(original, working string) ([]string, []string, []string, error) {
	a, err := ipaFingerprints(original)
	if err != nil {
		return nil, nil, nil, err
	}
	b, err := ipaFingerprints(working)
	if err != nil {
		return nil, nil, nil, err
	}
	var added, removed, changed []string
	for path, fp := range b {
		old, ok := a[path]
		if !ok {
			added = append(added, path)
		} else if old != fp {
			changed = append(changed, path)
		}
	}
	for path := range a {
		if _, ok := b[path]; !ok {
			removed = append(removed, path)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed, nil
}

func rewriteZIP(path string, replace map[string][]byte, remove map[string]bool) error {
	src, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	seen := map[string]bool{}

	for _, zf := range src.File {
		name := cleanArchivePath(zf.Name)
		if remove != nil && remove[name] {
			continue
		}
		if data, ok := replace[name]; ok {
			h := zf.FileHeader
			h.Method = zip.Deflate
			w, e := zw.CreateHeader(&h)
			if e != nil {
				zw.Close(); f.Close(); os.Remove(tmp); return e
			}
			if _, e = w.Write(data); e != nil {
				zw.Close(); f.Close(); os.Remove(tmp); return e
			}
			seen[name] = true
			continue
		}
		rc, e := zf.Open()
		if e != nil {
			zw.Close(); f.Close(); os.Remove(tmp); return e
		}
		h := zf.FileHeader
		w, e := zw.CreateHeader(&h)
		if e == nil {
			_, e = io.Copy(w, rc)
		}
		rc.Close()
		if e != nil {
			zw.Close(); f.Close(); os.Remove(tmp); return e
		}
	}

	for name, data := range replace {
		if seen[name] {
			continue
		}
		h := &zip.FileHeader{Name: cleanArchivePath(name), Method: zip.Deflate}
		h.SetMode(0o755)
		w, e := zw.CreateHeader(h)
		if e != nil {
			zw.Close(); f.Close(); os.Remove(tmp); return e
		}
		if _, e = w.Write(data); e != nil {
			zw.Close(); f.Close(); os.Remove(tmp); return e
		}
	}
	if err := zw.Close(); err != nil {
		f.Close(); os.Remove(tmp); return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp); return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
