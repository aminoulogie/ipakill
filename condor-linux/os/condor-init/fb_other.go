//go:build !linux

package main

import "errors"

// condor-init only runs on the tablet; these stubs let the portable code build and test on the PC.

func openScreen(rot Rotation) (*Screen, error) {
	return nil, errors.New("framebuffer needs linux")
}

func setBacklight(percent int) {}

func blankScreen(s *Screen, off bool) {}
