package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	alpineRoot  = "/data/alpine"
	alpineIndex = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86/latest-releases.yaml"
	alpineBase  = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86/"
)

// files written into the Alpine root after unpacking.
var alpineFiles = map[string]string{
	"/etc/resolv.conf":           "nameserver 1.1.1.1\nnameserver 8.8.8.8\n",
	"/etc/hostname":              "condor\n",
	"/root/.condor-installed-by": "condor alpine install\n",
}

func alpine(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: condor alpine install [alpine-minirootfs-*-x86.tar.gz] | status | remove | run <command>")
	}
	if err := needDevice(); err != nil {
		return err
	}
	if !strings.Contains(sh("su -c id"), "uid=0") {
		return fmt.Errorf("su doesn't give root; alpine needs it")
	}
	switch args[0] {
	case "status":
		v := sh("su -c 'cat " + alpineRoot + "/etc/alpine-release'")
		if v == "" || strings.Contains(v, "No such") {
			fmt.Println("Alpine: not installed (condor alpine install)")
		} else {
			fmt.Println("Alpine:", v, "in", alpineRoot)
			fmt.Println(sh("su -c 'df " + alpineRoot + "'"))
		}
		return nil
	case "remove":
		if strings.Contains(sh("ps"), "condor-init") && !strings.Contains(sh("ps"), "system_server") {
			return fmt.Errorf("the console is using Alpine; reboot into Android first (condor reboot), then remove")
		}
		sh("su -c 'for m in proc sys dev/pts dev tmp; do umount " + alpineRoot + "/$m; done; rm -r " + alpineRoot + "'")
		fmt.Println("removed", alpineRoot)
		return nil
	case "run":
		if len(args) < 2 {
			return fmt.Errorf("usage: condor alpine run <command> (e.g. condor alpine run apk add htop)")
		}
		return alpineRun(strings.Join(args[1:], " "))
	case "install":
		local := ""
		if len(args) > 1 {
			local = args[1]
		}
		return alpineInstall(local)
	}
	return fmt.Errorf("unknown: condor alpine %s", args[0])
}

func alpineInstall(local string) error {
	if v := sh("su -c 'cat " + alpineRoot + "/etc/alpine-release'"); v != "" && !strings.Contains(v, "No such") {
		return fmt.Errorf("Alpine %s is already installed in %s (condor alpine remove first)", v, alpineRoot)
	}
	if strings.Contains(sh("su -c 'ls "+initPath+"'"), "No such") {
		return fmt.Errorf("%s is missing; install condor-init first (dev.cmd)", initPath)
	}
	if local == "" {
		var err error
		if local, err = downloadAlpine(); err != nil {
			return err
		}
	}
	tmp := shellTmp + "/" + filepath.Base(local)
	fmt.Println("==> sending", filepath.Base(local), "to the tablet")
	if out, err := adb("push", local, tmp); err != nil {
		return fmt.Errorf("push: %s", strings.TrimSpace(out))
	}
	defer sh("rm " + tmp)
	fmt.Println("==> unpacking into", alpineRoot)
	out := sh("su -c '" + initPath + " untar " + tmp + " " + alpineRoot + "'")
	fmt.Println("   ", out)
	if !strings.Contains(out, " entries into ") {
		return fmt.Errorf("unpacking failed: %s", out)
	}
	for path, content := range alpineFiles {
		if err := writeRoot(alpineRoot+path, content); err != nil {
			return err
		}
	}
	v := sh("su -c 'cat " + alpineRoot + "/etc/alpine-release'")
	fmt.Println("==> Alpine", v, "installed in", alpineRoot)
	fmt.Println("Restart the console to enter it:  condor takeover restart   (then: condor term)")
	return nil
}

// writeRoot writes content to a root-owned path on the tablet through a temp file.
func writeRoot(path, content string) error {
	f, err := os.CreateTemp("", "condor-*")
	if err != nil {
		return err
	}
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())
	tmp := shellTmp + "/condor-file"
	if out, err := adb("push", f.Name(), tmp); err != nil {
		return fmt.Errorf("push %s: %s", path, strings.TrimSpace(out))
	}
	sh("su -c 'mkdir " + filepath.ToSlash(filepath.Dir(path)) + "; cat " + tmp + " > " + path + "; chmod 644 " + path + "'")
	sh("rm " + tmp)
	return nil
}

var (
	reMiniFile = regexp.MustCompile(`file:\s*(alpine-minirootfs-[0-9.]+-x86\.tar\.gz)`)
	reSHA256   = regexp.MustCompile(`sha256:\s*([0-9a-f]{64})`)
)

// pickMinirootfs finds the minirootfs file name and its sha256 in latest-releases.yaml.
func pickMinirootfs(index string) (file, sum string, err error) {
	for _, block := range strings.Split(index, "\n-") {
		if !strings.Contains(block, "alpine-minirootfs") {
			continue
		}
		f, s := reMiniFile.FindStringSubmatch(block), reSHA256.FindStringSubmatch(block)
		if f != nil && s != nil {
			return f[1], s[1], nil
		}
	}
	return "", "", fmt.Errorf("no x86 minirootfs in Alpine's release index")
}

// downloadAlpine fetches the latest x86 minirootfs into ~/.condor/cache (reused next time)
// and checks its sha256.
func downloadAlpine() (string, error) {
	fmt.Println("==> looking up the latest Alpine (x86)")
	index, err := httpGet(alpineIndex)
	if err != nil {
		return "", err
	}
	file, sum, err := pickMinirootfs(string(index))
	if err != nil {
		return "", err
	}
	cache := filepath.Join(home, "cache")
	os.MkdirAll(cache, 0o755)
	dst := filepath.Join(cache, file)
	if fileSHA256(dst) == sum {
		fmt.Println("    using cached", file)
		return dst, nil
	}
	fmt.Println("==> downloading", file)
	data, err := httpGet(alpineBase + file)
	if err != nil {
		return "", err
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
		return "", fmt.Errorf("%s: checksum mismatch (download corrupted?); try again", file)
	}
	return dst, os.WriteFile(dst, data, 0o644)
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
