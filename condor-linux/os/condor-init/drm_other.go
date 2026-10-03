//go:build !linux

package main

func drmInfo() string { return "drm needs linux\n" }
