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
