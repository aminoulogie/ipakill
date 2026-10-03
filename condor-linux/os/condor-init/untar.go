package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// untar extracts a .tar.gz into dir, keeping owners, permissions (setuid included),
// symlinks and hard links. Android has no tar, so `condor alpine install` runs this as
// `condor-init untar <file.tar.gz> <dir>`. Entries that would land outside dir are refused.
func untar(archive, dir string) (int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	inside := func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if p != dir && !strings.HasPrefix(p, dir+string(os.PathSeparator)) {
			return "", fmt.Errorf("%q escapes %s", name, dir)
		}
		return p, nil
	}
	type dirMeta struct {
		path string
		h    *tar.Header
	}
	var dirs []dirMeta // directory modes are applied last, so read-only dirs can be filled
	n := 0
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, err
		}
		p, err := inside(h.Name)
		if err != nil {
			return n, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return n, err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return n, err
			}
			dirs = append(dirs, dirMeta{p, h})
			continue
		case tar.TypeReg, tar.TypeRegA:
			os.Remove(p)
			out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return n, err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return n, err
			}
		case tar.TypeSymlink:
			os.Remove(p)
			if err := os.Symlink(h.Linkname, p); err != nil {
				return n, err
			}
			os.Lchown(p, h.Uid, h.Gid)
			n++
			continue
		case tar.TypeLink:
			target, err := inside(h.Linkname)
			if err != nil {
				return n, err
			}
			os.Remove(p)
			if err := os.Link(target, p); err != nil {
				return n, err
			}
			n++
			continue
		default:
			continue // device nodes, fifos: the tablet's /dev is bound in instead
		}
		applyMeta(p, h)
		n++
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		applyMeta(dirs[i].path, dirs[i].h)
	}
	return n, nil
}

// applyMeta sets owner, then mode (chown clears setuid bits, so mode goes second), then mtime.
func applyMeta(p string, h *tar.Header) {
	os.Lchown(p, h.Uid, h.Gid)
	os.Chmod(p, tarMode(h.Mode))
	os.Chtimes(p, h.ModTime, h.ModTime)
}

func tarMode(m int64) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}
