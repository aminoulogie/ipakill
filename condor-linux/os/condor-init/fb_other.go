//go:build !linux

package main

import "errors"

// condor-init only runs on the tablet; this stub lets the portable code build and test on the PC.
func openScreen(rot Rotation) (*Screen, error) {
	return nil, errors.New("framebuffer needs linux")
}
