package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

func benchConsole(b *testing.B) *console {
	s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	c, err := newConsole(s)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkReaderPage(b *testing.B) {
	dir := b.TempDir()
	t := &testing.T{}
	writeLongEPUB(t, filepath.Join(dir, "long.epub"))
	old := bookDirs
	bookDirs = []string{dir}
	defer func() { bookDirs = old; os.Remove(libraryPath) }()
	c := benchConsole(b)
	c.openBookAt(filepath.Join(dir, "long.epub"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.showPage()
	}
}

func BenchmarkLauncherPage(b *testing.B) {
	c := benchConsole(b)
	for i := 0; i < b.N; i++ {
		c.showPage()
	}
}

func BenchmarkSettingsPage(b *testing.B) {
	c := benchConsole(b)
	c.mode = modeSettings
	for i := 0; i < b.N; i++ {
		c.showPage()
	}
}

func BenchmarkBlit(b *testing.B) {
	s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1856))
	b.SetBytes(int64(len(img.Pix)))
	for i := 0; i < b.N; i++ {
		s.blitRGBA(img, 0, 64)
	}
}

func BenchmarkReaderTurn(b *testing.B) {
	dir := b.TempDir()
	writeLongEPUB(&testing.T{}, filepath.Join(dir, "long.epub"))
	old := bookDirs
	bookDirs = []string{dir}
	defer func() { bookDirs = old; os.Remove(libraryPath) }()
	c := benchConsole(b)
	c.openBookAt(filepath.Join(dir, "long.epub"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.book.page = i % 3
		c.invalidatePage()
		c.showPage()
	}
}

func BenchmarkLineStep(b *testing.B) {
	dir := b.TempDir()
	writeLongEPUB(&testing.T{}, filepath.Join(dir, "long.epub"))
	old := bookDirs
	bookDirs = []string{dir}
	defer func() { bookDirs = old; os.Remove(libraryPath) }()
	c := benchConsole(b)
	c.lib.Prefs.LineFocus = true
	c.openBookAt(filepath.Join(dir, "long.epub"))
	c.showPage()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		old := c.book.line
		c.book.line = (old + 1) % 10
		c.refreshLines(old, c.book.line)
	}
}
