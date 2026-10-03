//go:build !linux

package main

import "errors"

func startGPU(s *Screen) (gpuDev, error) { return nil, errors.New("the GPU needs the tablet") }
