//go:build linux

package main

import "syscall"

func syncDisks() { syscall.Sync() }
