//go:build !linux

package main

import (
	"errors"
	"os"
)

func startShell(cols, rows int) (*os.File, func(), error) {
	return nil, nil, errors.New("the console needs linux")
}

func setWinsize(f *os.File, cols, rows int) {}
