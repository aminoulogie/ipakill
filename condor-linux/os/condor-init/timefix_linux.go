//go:build linux

package main

import (
	"log"
	"os"
	"syscall"
	"time"
)

// fixClock moves a clock that's clearly in the past (the tablet has no RTC battery and boots
// in 2013) forward to this binary's install time, so TLS (apk) accepts certificates. It's
// only a floor: `condor takeover push` refreshes it on every install.
func fixClock() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	fi, err := os.Stat(exe)
	if err != nil || !time.Now().Before(fi.ModTime()) {
		return
	}
	tv := syscall.NsecToTimeval(fi.ModTime().UnixNano())
	if err := syscall.Settimeofday(&tv); err != nil {
		log.Printf("clock: %v", err)
		return
	}
	log.Printf("clock was behind; set to %s", fi.ModTime().UTC().Format(time.RFC3339))
}
