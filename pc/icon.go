package main

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// Top-level icon files Xcode copies into the .app, e.g. AppIcon60x60@2x.png.
// They are Apple's "CgBI" PNG variant, which iOS decodes natively.
var iconRe = regexp.MustCompile(`^Payload/[^/]+\.app/(AppIcon|Icon)[^/]*\.png$`)

// ipaIcon returns the largest home-screen icon inside an .ipa.
func ipaIcon(ipa string) ([]byte, error) {
	z, err := zip.OpenReader(ipa)
	if err != nil {
		return nil, err
	}
	defer z.Close()

	var best *zip.File
	for _, f := range z.File {
		if !iconRe.MatchString(f.Name) || strings.Contains(strings.ToLower(path.Base(f.Name)), "ipad") {
			continue
		}
		if best == nil || f.UncompressedSize64 > best.UncompressedSize64 {
			best = f
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no icon in %s", path.Base(ipa))
	}
	rc, err := best.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 4<<20))
}
