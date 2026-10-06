package epub

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// The resolvePath cases from Soma's src/lib/epub.test.ts.
func TestResolvePathMatchesSoma(t *testing.T) {
	for _, c := range []struct{ base, href, want string }{
		{"OEBPS/content.opf", "text/ch1.xhtml", "OEBPS/text/ch1.xhtml"},
		{"OEBPS/content.opf", "../shared/a.css", "shared/a.css"},
		{"OEBPS/content.opf", "./cover.jpg", "OEBPS/cover.jpg"},
		{"content.opf", "ch1.xhtml", "ch1.xhtml"},
		{"OEBPS/content.opf", "/images/c.png", "images/c.png"},
		{"OEBPS/text/a.xhtml", "b.xhtml#top", "OEBPS/text/b.xhtml"},
		{"OEBPS/content.opf", "text/Chapter%202.xhtml", "OEBPS/text/Chapter 2.xhtml"},
		{"a/b.opf", "100%.xhtml", "a/100%.xhtml"},
	} {
		if got := ResolvePath(c.base, c.href); got != c.want {
			t.Errorf("ResolvePath(%q, %q) = %q, want %q", c.base, c.href, got, c.want)
		}
	}
}

const testOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>The Test &amp; Book</dc:title>
    <dc:creator>A. Writer</dc:creator>
    <meta name="cover" content="cov"/>
  </metadata>
  <manifest>
    <item id="c1" href="text/Chapter%201.xhtml" media-type="application/xhtml+xml"/>
    <item id="ad" href="text/ad.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="cov" href="img/cover.jpg" media-type="image/jpeg"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="ad" linear="no"/><itemref idref="c2"/></spine>
</package>`

func TestParseOPF(t *testing.T) {
	b, err := ParseOPF([]byte(testOPF), "OEBPS/content.opf")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "The Test & Book" || b.Author != "A. Writer" {
		t.Errorf("metadata %q / %q", b.Title, b.Author)
	}
	if b.CoverPath != "OEBPS/img/cover.jpg" {
		t.Errorf("cover %q", b.CoverPath)
	}
	if len(b.Chapters) != 2 || b.Chapters[0].Path != "OEBPS/text/Chapter 1.xhtml" || b.Chapters[1].ID != "c2" {
		t.Errorf("chapters %+v (linear=no must be skipped)", b.Chapters)
	}
	if _, err := ParseOPF([]byte("<package><manifest/></package>"), "x.opf"); err == nil {
		t.Error("a package without manifest items is not an EPUB")
	}
}

// writeEPUB builds a small but complete book; also used for previews.
func writeEPUB(t *testing.T, file string) {
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	add("mimetype", "application/epub+zip")
	add(ContainerPath, `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	add("OEBPS/content.opf", testOPF)
	add("OEBPS/text/Chapter 1.xhtml", `<html><head><title>One</title><style>p{}</style></head><body>
<h1>Chapter One</h1><p>It was a <i>bright</i> cold day in April &amp; the clocks
were striking thirteen.</p><script>alert(1)</script><p>Second paragraph.<br/>After a break.</p>
<img src="../img/x.png" alt="A map"/></body></html>`)
	add("OEBPS/text/ad.xhtml", `<html><body><p>Buy more books</p></body></html>`)
	add("OEBPS/text/ch2.xhtml", `<html><body><h2>Two</h2><p>The end.</p></body></html>`)
	zw.Close()
	f.Close()
}

func TestOpenAndChapterText(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.epub")
	writeEPUB(t, file)
	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	x, err := b.ReadFile(b.Chapters[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	blocks := ChapterText(x)
	want := []Block{
		{"Chapter One", true},
		{"It was a bright cold day in April & the clocks were striking thirteen.", false},
		{"Second paragraph.", false},
		{"After a break.", false},
		{"[A map]", false},
	}
	if len(blocks) != len(want) {
		t.Fatalf("blocks %+v", blocks)
	}
	for i := range want {
		if blocks[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, blocks[i], want[i])
		}
	}
	if got := ChapterTitle(x, "?"); got != "Chapter One" {
		t.Errorf("title %q", got)
	}
}

// Books that don't mark their cover: an image named cover, else the first picture (not SVG).
func TestCoverFallbacks(t *testing.T) {
	opf := func(items string) string {
		return `<package><metadata><dc:title>T</dc:title></metadata><manifest>` + items +
			`<item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`
	}
	for _, tc := range []struct{ items, want string }{
		{`<item id="logo" href="img/logo.png" media-type="image/png"/><item id="i9" href="img/Front_Cover.jpg" media-type="image/jpeg"/>`, "img/Front_Cover.jpg"},
		{`<item id="a" href="img/art.svg" media-type="image/svg+xml"/><item id="b" href="img/first.jpg" media-type="image/jpeg"/>`, "img/first.jpg"},
		{``, ""},
	} {
		b, err := ParseOPF([]byte(opf(tc.items)), "content.opf")
		if err != nil {
			t.Fatal(err)
		}
		if b.CoverPath != tc.want {
			t.Errorf("cover %q, want %q", b.CoverPath, tc.want)
		}
	}
}
