//go:build !linux

package main

import "errors"

func readTouch(name string, fbW, fbH int, rot Rotation, logf func(string, ...any), handle func([]TouchPoint)) error {
	return errors.New("touch needs linux")
}

func readKeys(name string, handle func(code uint16, value int32)) error {
	return errors.New("keys need linux")
}
