//go:build !linux

package main

import "errors"

func startGPU(s *Screen) (gpuDisplay, error) { return nil, errors.New("the GPU needs the tablet") }

func leftoverSurfaceFlinger() bool { return false }
