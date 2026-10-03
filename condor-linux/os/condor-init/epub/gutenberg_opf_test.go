package epub

import (
	"strings"
	"testing"
)

// Project Gutenberg's EPUB 3 package documents have <meta property> elements with text.
func TestGutenbergStyleOPF(t *testing.T) {
	opf := `<?xml version='1.0' encoding='UTF-8'?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Frankenstein</dc:title>
    <dc:language>en-GB</dc:language>
    <meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>
    <meta name="cover" content="item1"/>
  </metadata>
  <manifest>
    <item id="item1" href="cover.jpg" media-type="image/jpeg"/>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`
	b, err := ParseOPF([]byte(opf), "OEBPS/content.opf")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 1 || b.Title != "Frankenstein" || b.CoverPath != "OEBPS/cover.jpg" || b.Language != "en" {
		t.Fatalf("got %+v", b)
	}
	// The same with an opf: prefix on every element, which some generators write.
	prefixed := strings.NewReplacer("<package xmlns=", "<opf:package xmlns:opf=", "</package>", "</opf:package>",
		"<metadata", "<opf:metadata", "</metadata>", "</opf:metadata>", "<meta ", "<opf:meta ", "</meta>", "</opf:meta>",
		"<manifest>", "<opf:manifest>", "</manifest>", "</opf:manifest>", "<item ", "<opf:item ",
		"<spine>", "<opf:spine>", "</spine>", "</opf:spine>", "<itemref ", "<opf:itemref ").Replace(opf)
	if b, err := ParseOPF([]byte(prefixed), "OEBPS/content.opf"); err != nil || len(b.Chapters) != 1 {
		t.Fatalf("prefixed: %v %+v", err, b)
	}
}
