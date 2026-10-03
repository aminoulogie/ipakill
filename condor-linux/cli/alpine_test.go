package main

import "testing"

// Shape of https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86/latest-releases.yaml
const sampleIndex = `---
-
  title: "Standard"
  flavor: alpine-standard
  file: alpine-standard-3.22.1-x86.iso
  sha256: 1111111111111111111111111111111111111111111111111111111111111111
-
  title: "Mini root filesystem"
  desc: "Minimal root filesystem.
    For use in containers
    and minimal chroots."
  branch: v3.22
  arch: x86
  version: 3.22.1
  flavor: alpine-minirootfs
  file: alpine-minirootfs-3.22.1-x86.tar.gz
  iso: alpine-minirootfs-3.22.1-x86.tar.gz
  size: 3277146
  sha256: 2222222222222222222222222222222222222222222222222222222222222222
  sha512: 33
`

func TestPickMinirootfs(t *testing.T) {
	f, s, err := pickMinirootfs(sampleIndex)
	if err != nil {
		t.Fatal(err)
	}
	if f != "alpine-minirootfs-3.22.1-x86.tar.gz" || s[:4] != "2222" {
		t.Fatalf("got %s %s", f, s)
	}
	if _, _, err := pickMinirootfs("---\n- file: other.iso\n"); err == nil {
		t.Fatal("expected an error without a minirootfs entry")
	}
}
