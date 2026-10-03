//go:build !linux

package main

func alpineMounts() error { return nil }

func setHostname() {}

func runInAlpine(args ...string) error { return nil }
