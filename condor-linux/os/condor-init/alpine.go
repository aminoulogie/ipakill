package main

import (
	_ "embed"
	"log"
	"os"
	"strings"
	"time"
)

// alpineRoot is where `condor alpine install` unpacks Alpine Linux. When it's there, the
// console's shell runs inside it (chroot); otherwise it's Android's /system/bin/sh.
var alpineRoot = "/data/alpine"

func alpineInstalled() bool {
	_, err := os.Stat(alpineRoot + "/bin/busybox")
	return err == nil
}

// alpineVersion is the installed release, e.g. "3.22.1", or "" if Alpine isn't installed.
func alpineVersion() string {
	b, err := os.ReadFile(alpineRoot + "/etc/alpine-release")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// alpineConfig is (re)written into the Alpine root at every start: the prompt, and the
// proxy (condor-init's, on 127.0.0.1:3128): through the PC when condor net runs, otherwise
// straight out over Wi-Fi.
var alpineConfig = map[string]string{
	"/etc/profile.d/condor.sh": `PS1='[\u@\h \W]\$ '
alias ll='ls -l'
alias la='ls -la'
export TZ=WAT-1
export http_proxy=http://` + proxyAddr + ` https_proxy=http://` + proxyAddr + `
export HTTP_PROXY=$http_proxy HTTPS_PROXY=$https_proxy no_proxy=localhost,127.0.0.1
`,
	"/etc/resolv.conf": "nameserver 1.1.1.1\nnameserver 8.8.8.8\n",
}

// updateScript is update.sh: "update" in the Terminal tab gets, builds and installs the
// latest condor-init on the tablet itself.
//
//go:embed update.sh
var updateScript []byte

// configureAlpine writes alpineConfig and points apk at plain-http mirrors (packages are
// signed, so http is safe, and a plain proxy request is simpler than a CONNECT tunnel).
func configureAlpine() {
	for p, content := range alpineConfig {
		os.WriteFile(alpineRoot+p, []byte(content), 0o644)
	}
	os.MkdirAll(alpineRoot+"/usr/local/bin", 0o755)
	os.WriteFile(alpineRoot+"/usr/local/bin/wifi", []byte(wifiScript), 0o755)
	os.Chmod(alpineRoot+"/usr/local/bin/wifi", 0o755)
	os.WriteFile(alpineRoot+"/usr/local/bin/update", updateScript, 0o755) // condor updates itself
	os.Chmod(alpineRoot+"/usr/local/bin/update", 0o755)
	repos := alpineRoot + "/etc/apk/repositories"
	if b, err := os.ReadFile(repos); err == nil {
		os.WriteFile(repos, []byte(strings.ReplaceAll(string(b), "https://", "http://")), 0o644)
	}
}

// alpineBoot prepares Alpine at startup (config, mounts) and reconnects to the saved Wi-Fi
// network, so the tablet is online without the PC.
func alpineBoot() {
	if !alpineInstalled() {
		return
	}
	configureAlpine()
	if err := alpineMounts(); err != nil {
		log.Printf("alpine mounts: %v", err)
		return
	}
	if err := runInAlpine("/usr/local/bin/wifi", "boot"); err != nil {
		log.Printf("wifi boot: %v", err)
	}
	log.Printf("wifi boot done")
	syncClock()
	startSSHD()
}

// startSSHD starts Alpine's SSH server if it's installed (condor ssh setup installs it).
// Root logs in with a key only (Alpine's default PermitRootLogin prohibit-password).
func startSSHD() {
	if _, err := os.Stat(alpineRoot + "/usr/sbin/sshd"); err != nil {
		return
	}
	if err := runInAlpine("/bin/sh", "-c", sshdStart); err != nil {
		log.Printf("sshd: %v", err)
		return
	}
	log.Printf("sshd started")
}

// sshdStart makes host keys on first use and starts sshd unless it's already running.
const sshdStart = "ssh-keygen -A >/dev/null 2>&1; pgrep -x sshd >/dev/null || /usr/sbin/sshd"

// syncClock sets the real time from the internet (busybox ntpd, one shot). The tablet has no
// clock battery, so without this the time is only roughly right (see fixClock).
func syncClock() {
	for try := 1; try <= 3; try++ {
		if err := runInAlpine("/bin/sh", "-c", "ntpd -n -q -p pool.ntp.org"); err == nil {
			log.Printf("clock set from pool.ntp.org: %s", time.Now().UTC().Format(time.RFC3339))
			return
		}
		time.Sleep(time.Duration(try) * 10 * time.Second)
	}
	log.Printf("ntp: no answer; clock stays approximate")
}
