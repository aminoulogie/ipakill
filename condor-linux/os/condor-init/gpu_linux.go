//go:build linux

package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// condorsf.bin is os/condor-gl/condorsf, built by os/condor-gl/build.sh.
//
//go:embed condorsf.bin
var condorsfBin []byte

const (
	sfHelperPath = "/data/condor/condor-sf"
	glShmPath    = "/dev/condor-gl.shm" // Android's /dev is a tmpfs: memory, not flash
	sfLog        = "/data/condor/surfaceflinger.log"
)

// sfGPU is the GPU helper (os/condor-gl/condorsf.cpp): condor's screen on a SurfaceFlinger
// layer. One command at a time (mu); overlay frames are coalesced: the sender goroutine
// always sends the latest, so a finger moving faster than the panel refreshes never queues.
type sfGPU struct {
	cmd     *exec.Cmd
	in, out *os.File
	mem     []byte
	size    int // one native screen image
	pageW   int

	mu     sync.Mutex // one command at a time
	dead   error
	frames int // overlay frames shown since the scroll began
	since  time.Time

	pendMu  sync.Mutex // the overlay waiting to be shown (never held while the GPU works)
	pending []quad
	havePen bool
	overGen int
	kick    chan struct{}
}

// startGPU starts SurfaceFlinger (if it isn't running) and the helper, and returns once the
// helper's layer is up. It needs Android's root and environment, which condor-init has.
func startGPU(s *Screen) (gpuDisplay, error) {
	if _, err := os.Stat("/system/lib/libgui.so"); err != nil {
		return nil, errors.New("no Android graphics libraries here")
	}
	if s.rot != Rot90 || s.bpp != 32 || s.stride != 4*s.fbW {
		return nil, fmt.Errorf("framebuffer layout %dx%d stride %d not handled", s.fbW, s.fbH, s.stride)
	}
	if old, err := os.ReadFile(sfHelperPath); err != nil || !bytes.Equal(old, condorsfBin) {
		if err := os.WriteFile(sfHelperPath+".new", condorsfBin, 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(sfHelperPath+".new", sfHelperPath); err != nil {
			return nil, err
		}
	}
	log.Printf("GPU: starting: SurfaceFlinger")
	if err := startSurfaceFlinger(); err != nil {
		return nil, err
	}

	size := s.stride * s.fbH
	pageW := s.fbH // the logical (portrait) width
	total := 3*size + pageW*pageMaxRows*4
	f, err := os.OpenFile(glShmPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := f.Truncate(int64(total)); err != nil {
		return nil, err
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, total, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
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
	cmd := exec.Command(sfHelperPath, glShmPath, strconv.Itoa(s.fbW), strconv.Itoa(s.fbH), strconv.Itoa(pageMaxRows), strconv.Itoa(pageSlice))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, log.Writer()
	cmd.Env = os.Environ() // Android's, from init: ANDROID_PROPERTY_WORKSPACE, LD_LIBRARY_PATH...
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	inR.Close()
	outW.Close()
	go cmd.Wait()
	log.Printf("GPU: helper started (pid %d), waiting for its layer", cmd.Process.Pid)
	g := &sfGPU{cmd: cmd, in: inW, out: outR, mem: mem, size: size, pageW: pageW, kick: make(chan struct{}, 1)}
	if err := g.answer('R', 30*time.Second); err != nil {
		g.close()
		return nil, fmt.Errorf("helper didn't start: %w", err)
	}
	log.Printf("GPU: the helper's layer is up")
	hideBootAnimation()
	go func() { time.Sleep(3 * time.Second); hideBootAnimation() }()
	go g.sender()
	return g, nil
}

// startSurfaceFlinger runs Android's compositor, detached, unless it's already running. On
// this tablet it isn't an init service of its own (init.rc has it commented out: Android runs
// it inside system_server), so condor runs the program itself.
func startSurfaceFlinger() error {
	if procRunning("surfaceflinger") {
		log.Printf("GPU: SurfaceFlinger already running")
		return nil
	}
	lf, err := os.OpenFile(sfLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	cmd := exec.Command("/system/bin/surfaceflinger")
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("surfaceflinger: %w", err)
	}
	go func() {
		err := cmd.Wait()
		log.Printf("GPU: SurfaceFlinger exited: %v (see %s)", err, sfLog)
	}()
	log.Printf("GPU: started SurfaceFlinger, pid %d", cmd.Process.Pid)
	return nil
}

// hideBootAnimation: SurfaceFlinger starts Android's boot animation as it comes up; condor's
// layer covers it, but it would keep the GPU busy.
func hideBootAnimation() {
	exec.Command("/system/bin/setprop", "service.bootanim.exit", "1").Run()
	exec.Command("/system/bin/stop", "bootanim").Run()
}

func procPids(name string) []int {
	var pids []int
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		b, err := os.ReadFile(d + "/cmdline")
		if err != nil || len(b) == 0 {
			continue
		}
		arg0, _, _ := strings.Cut(string(b), "\x00")
		if filepath.Base(arg0) == name {
			if pid, err := strconv.Atoi(filepath.Base(d)); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

func procRunning(name string) bool { return len(procPids(name)) > 0 }

func leftoverSurfaceFlinger() bool { return procRunning("surfaceflinger") }

func (g *sfGPU) answer(want byte, wait time.Duration) error {
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

// do sends one command and waits for its answer. Caller holds g.mu.
func (g *sfGPU) do(cmd []byte, wait time.Duration) error {
	if g.dead != nil {
		return g.dead
	}
	if _, err := g.in.Write(cmd); err != nil {
		g.dead = err
		return err
	}
	g.out.SetReadDeadline(time.Now().Add(wait))
	var b [1]byte
	if _, err := g.out.Read(b[:]); err != nil {
		g.dead = err
		return err
	}
	if b[0] == 'f' {
		return errors.New("the GPU couldn't")
	}
	if b[0] != 'k' {
		g.dead = fmt.Errorf("helper said %q", b[0])
		return g.dead
	}
	return nil
}

func words32(v ...uint32) []byte {
	var b []byte
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, x)
	}
	return b
}

func (g *sfGPU) images() (old, nu []byte) {
	return g.mem[:g.size:g.size], g.mem[g.size : 2*g.size : 2*g.size]
}

func (g *sfGPU) screenBuf() []byte { return g.mem[2*g.size : 3*g.size : 3*g.size] }

func (g *sfGPU) load() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.do(words32(1, 3), 2*time.Second)
}

func (g *sfGPU) frame(q []quad, last bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.do(encodeFrame(q, last), time.Second)
}

func (g *sfGPU) present(lo, hi int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.do(words32(4, uint32(lo), uint32(hi)), 2*time.Second)
}

// pageNew tells the helper the page is now h rows, none of them on the GPU (it frees the
// old ones).
func (g *sfGPU) pageNew(h int) error {
	if h > pageMaxRows {
		return fmt.Errorf("page of %d rows too tall for the GPU", h)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.do(words32(5, uint32(h)), 2*time.Second)
}

// pageRows copies rows y0..y1-1 of the page into shared memory and uploads them. A slice not
// on the GPU yet must be sent whole (gpuscroll.go sees to it).
func (g *sfGPU) pageRows(img *image.RGBA, y0, y1 int) error {
	if img.Rect.Dx() != g.pageW {
		return fmt.Errorf("page %d wide, the GPU's is %d", img.Rect.Dx(), g.pageW)
	}
	y0, y1 = max(y0, 0), min(y1, img.Rect.Dy())
	if y0 >= y1 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.copyRows(img, y0, y1)
	return g.do(words32(6, uint32(y0), uint32(y1)), 2*time.Second)
}

// pageFree takes slice k of the page off the GPU.
func (g *sfGPU) pageFree(k int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.do(words32(9, uint32(k)), time.Second)
}

func (g *sfGPU) copyRows(img *image.RGBA, y0, y1 int) {
	page := g.mem[3*g.size:]
	n := 4 * g.pageW
	for y := y0; y < y1; y++ {
		o := img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y)
		copy(page[y*n:(y+1)*n], img.Pix[o:o+n])
	}
}

// overlay shows q over the screen from now on. It never waits (the touch loop calls it): the
// sender shows the latest overlay as soon as the GPU is free.
func (g *sfGPU) overlay(q []quad) {
	g.pendMu.Lock()
	g.pending, g.havePen = q, true
	g.pendMu.Unlock()
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

// overlayOff drops the overlay (and any not yet shown); the next present shows the screen.
func (g *sfGPU) overlayOff() error {
	g.pendMu.Lock()
	g.pending, g.havePen = nil, false
	g.overGen++ // an overlay the sender has taken but not sent yet is dropped
	g.pendMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.frames > 0 {
		d := time.Since(g.since)
		log.Printf("GPU: scroll: %d frames in %v (%.0f a second)", g.frames, d.Round(time.Millisecond), float64(g.frames)/d.Seconds())
		g.frames = 0
	}
	return g.do(words32(8), time.Second)
}

func (g *sfGPU) sender() {
	for range g.kick {
		g.pendMu.Lock()
		q, have, gen := g.pending, g.havePen, g.overGen
		g.pending, g.havePen = nil, false
		g.pendMu.Unlock()
		if !have {
			continue
		}
		g.mu.Lock()
		g.pendMu.Lock()
		stale := gen != g.overGen
		g.pendMu.Unlock()
		if !stale {
			b := binary.LittleEndian.AppendUint32(nil, 7)
			b = binary.LittleEndian.AppendUint32(b, uint32(len(q)))
			b = append(b, encodeFrame(q, false)[8:]...)
			if g.frames == 0 {
				g.since = time.Now()
			}
			if err := g.do(b, time.Second); err != nil {
				log.Printf("GPU: overlay: %v", err)
			}
			g.frames++
		}
		dead := g.dead != nil
		g.mu.Unlock()
		if dead {
			return
		}
	}
}

func (g *sfGPU) failed() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dead
}

// close stops the helper (its layer goes away). The shared memory stays mapped: the screen
// may still be drawn into it.
func (g *sfGPU) close() {
	g.in.Close()
	g.out.Close()
	if g.cmd.Process != nil {
		g.cmd.Process.Kill()
	}
}
