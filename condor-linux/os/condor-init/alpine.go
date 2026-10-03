package main

import (
	"os"
	"strings"
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
// proxy that carries internet over USB (condor net on the PC).
var alpineConfig = map[string]string{
	"/etc/profile.d/condor.sh": `PS1='[\u@\h \W]\$ '
alias ll='ls -l'
alias la='ls -la'
export http_proxy=http://` + proxyAddr + ` https_proxy=http://` + proxyAddr + `
export HTTP_PROXY=$http_proxy HTTPS_PROXY=$https_proxy no_proxy=localhost,127.0.0.1
`,
	"/etc/resolv.conf": "nameserver 1.1.1.1\nnameserver 8.8.8.8\n",
}

// configureAlpine writes alpineConfig and points apk at plain-http mirrors (packages are
// signed, so http is safe, and a plain proxy request is simpler than a CONNECT tunnel).
func configureAlpine() {
	for p, content := range alpineConfig {
		os.WriteFile(alpineRoot+p, []byte(content), 0o644)
	}
	repos := alpineRoot + "/etc/apk/repositories"
	if b, err := os.ReadFile(repos); err == nil {
		os.WriteFile(repos, []byte(strings.ReplaceAll(string(b), "https://", "http://")), 0o644)
	}
}
