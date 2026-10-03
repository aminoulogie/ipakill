package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeTarGz(t *testing.T, path string, entries []*tar.Header, bodies map[string]string) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := bodies[h.Name]
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	os.WriteFile(path, buf.Bytes(), 0o644)
}

func TestUntarLikeAlpine(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "root.tar.gz")
	writeTarGz(t, arc, []*tar.Header{
		{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "bin/busybox", Typeflag: tar.TypeReg, Mode: 0o4755},
		{Name: "bin/sh", Typeflag: tar.TypeSymlink, Linkname: "/bin/busybox", Mode: 0o777},
		{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "etc/alpine-release", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "etc/release-link", Typeflag: tar.TypeLink, Linkname: "etc/alpine-release"},
		{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777},
	}, map[string]string{"bin/busybox": "#!fake", "etc/alpine-release": "3.22.1\n"})

	root := filepath.Join(dir, "alpine")
	n, err := untar(arc, root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("extracted %d entries, want 4 (dirs not counted)", n)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/release-link")); string(b) != "3.22.1\n" {
		t.Errorf("hard link content %q", b)
	}
	if l, _ := os.Readlink(filepath.Join(root, "bin/sh")); l != "/bin/busybox" {
		t.Errorf("symlink -> %q", l)
	}
	if runtime.GOOS == "linux" {
		fi, _ := os.Stat(filepath.Join(root, "bin/busybox"))
		if fi.Mode()&os.ModeSetuid == 0 || fi.Mode().Perm() != 0o755 {
			t.Errorf("busybox mode %v", fi.Mode())
		}
		fi, _ = os.Stat(filepath.Join(root, "tmp"))
		if fi.Mode()&os.ModeSticky == 0 {
			t.Errorf("tmp mode %v", fi.Mode())
		}
	}
}

func TestUntarRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "bad.tar.gz")
	writeTarGz(t, arc, []*tar.Header{{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		map[string]string{"../evil": "x"})
	if _, err := untar(arc, filepath.Join(dir, "root")); err == nil {
		t.Fatal("expected an error for ../evil")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("file written outside the target")
	}
}
