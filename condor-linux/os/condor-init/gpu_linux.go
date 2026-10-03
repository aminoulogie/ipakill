//go:build linux

package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// glanim.bin is os/condor-gl/glanim, built by os/condor-gl/build.sh.
//
//go:embed glanim.bin
var glanimBin []byte

const (
	glanimPath = "/data/condor/condor-gl"
	glShmPath  = "/dev/condor-gl.shm" // Android's /dev is a tmpfs: memory, not flash
)

type glHelper struct {
	cmd     *exec.Cmd
	in, out *os.File
	mem     []byte
	size    int
}

// startGPU writes out and starts the GPU helper, which needs Android's root (where
// condor-init runs: /system, /vendor and the properties area it inherited from init) and
// SurfaceFlinger stopped (condor doesn't use it).
func startGPU(s *Screen) (gpuDev, error) {
	if _, err := os.Stat("/system/lib/libEGL.so"); err != nil {
		return nil, errors.New("no Android EGL here")
	}
	if s.rot != Rot90 || s.bpp != 32 || s.stride != 4*s.fbW {
		return nil, fmt.Errorf("framebuffer layout %dx%d stride %d not handled", s.fbW, s.fbH, s.stride)
	}
	if old, err := os.ReadFile(glanimPath); err != nil || !bytes.Equal(old, glanimBin) {
		if err := os.WriteFile(glanimPath+".new", glanimBin, 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(glanimPath+".new", glanimPath); err != nil {
			return nil, err
		}
	}
	if err := exec.Command("/system/bin/stop", "surfaceflinger").Run(); err != nil {
		log.Printf("GPU: stop surfaceflinger: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	size := s.stride * s.fbH
	f, err := os.OpenFile(glShmPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := f.Truncate(int64(3 * size)); err != nil {
		return nil, err
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, 3*size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(glanimPath, glShmPath, strconv.Itoa(s.fbW), strconv.Itoa(s.fbH))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, log.Writer()
	cmd.Env = os.Environ() // Android's, from init: ANDROID_PROPERTY_WORKSPACE, LD_LIBRARY_PATH...
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	inR.Close()
	outW.Close()
	go cmd.Wait()
	g := &glHelper{cmd: cmd, in: inW, out: outR, mem: mem, size: size}
	if err := g.answer('R', 20*time.Second); err != nil {
		g.close()
		return nil, fmt.Errorf("helper didn't start: %w", err)
	}
	return g, nil
}

func (g *glHelper) answer(want byte, wait time.Duration) error {
	g.out.SetReadDeadline(time.Now().Add(wait))
	var b [1]byte
	if _, err := g.out.Read(b[:]); err != nil {
		return err
	}
	if b[0] != want {
		return fmt.Errorf("helper said %q", b[0])
	}
	return nil
}

func (g *glHelper) images() (old, nu []byte) {
	return g.mem[:g.size:g.size], g.mem[g.size : 2*g.size : 2*g.size]
}

func (g *glHelper) load() error {
	cmd := binary.LittleEndian.AppendUint32(nil, 1)
	cmd = binary.LittleEndian.AppendUint32(cmd, 3)
	if _, err := g.in.Write(cmd); err != nil {
		return err
	}
	return g.answer('k', 2*time.Second)
}

func (g *glHelper) frame(q []quad, last bool) error {
	if _, err := g.in.Write(encodeFrame(q, last)); err != nil {
		return err
	}
	return g.answer('k', time.Second)
}

// close stops the helper. The shared memory stays mapped: pictures taken from it may
// still be in use by the animation that failed.
func (g *glHelper) close() {
	g.in.Close()
	g.out.Close()
	if g.cmd.Process != nil {
		g.cmd.Process.Kill()
	}
}
