// Package epub reads EPUB books: the archive, the package document, the reading order, and
// the text of each chapter as paragraphs.
//
// Ported from Soma's reader (aminoulogie/kite-bay-otter-topaz, src/lib/epub.ts), keeping its
// rules: the package document is found through META-INF/container.xml (never guessed),
// manifest hrefs are percent-decoded and resolved against the OPF's folder, spine items marked
// linear="no" are skipped, metadata is matched on the part after the "dc:" prefix, and the
// cover comes from whichever of the three conventions the book uses. Scripts and remote
// resources are never followed: a book is a document.
package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// ContainerPath is the fixed entry point every EPUB has.
const ContainerPath = "META-INF/container.xml"

// Chapter is one item of the reading order.
type Chapter struct {
	ID        string
	Path      string // inside the archive, resolved against the OPF folder
	MediaType string
}

// Book is an opened EPUB.
type Book struct {
	Title, Author string
	Language      string // e.g. "en", "fr" (lower case, region dropped); "" if unknown
	CoverPath     string
	Chapters      []Chapter
	files         map[string]*zip.File
	closer        io.Closer
}

// ResolvePath joins a relative href against the folder of base, the way Soma's resolvePath
// does: query and fragment dropped, percent-decoded (left raw if the escape is invalid), a
// leading "/" meaning the archive root, "." and ".." honoured.
func ResolvePath(base, href string) string {
	raw := strings.SplitN(strings.SplitN(href, "#", 2)[0], "?", 2)[0]
	rel, err := url.PathUnescape(raw)
	if err != nil {
		rel = raw
	}
	if rel == "" {
		return ""
	}
	if strings.HasPrefix(rel, "/") {
		return rel[1:]
	}
	var parts []string
	if i := strings.LastIndex(base, "/"); i >= 0 {
		parts = strings.Split(base[:i], "/")
	}
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, "/")
}

// ContainerOPFPath reads where the package document lives from container.xml.
func ContainerOPFPath(containerXML []byte) (string, error) {
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xmlUnmarshal(containerXML, &c); err != nil {
		return "", err
	}
	for _, r := range c.Rootfiles {
		if p := strings.TrimPrefix(strings.TrimSpace(r.FullPath), "/"); p != "" {
			return p, nil
		}
	}
	return "", errors.New("container.xml names no package document")
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfDoc struct {
	Metadata struct {
		Inner []byte `xml:",innerxml"`
		Metas []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Items    []opfItem `xml:"manifest>item"`
	Itemrefs []struct {
		IDRef  string `xml:"idref,attr"`
		Linear string `xml:"linear,attr"`
	} `xml:"spine>itemref"`
}

// ParseOPF reads the package document: title, author, cover and reading order.
func ParseOPF(opfXML []byte, opfPath string) (*Book, error) {
	var d opfDoc
	if err := xmlUnmarshal(opfXML, &d); err != nil {
		return nil, err
	}
	type item struct{ path, mediaType, properties string }
	byID := map[string]item{}
	var order []string
	for _, it := range d.Items {
		if it.ID == "" || it.Href == "" {
			continue
		}
		byID[it.ID] = item{ResolvePath(opfPath, it.Href), it.MediaType, it.Properties}
		order = append(order, it.ID)
	}
	if len(byID) == 0 {
		return nil, errors.New("not an EPUB package document: no manifest items")
	}
	b := &Book{}
	for _, ref := range d.Itemrefs {
		if ref.IDRef == "" || ref.Linear == "no" { // in the book, not in the reading order
			continue
		}
		if it, ok := byID[ref.IDRef]; ok {
			b.Chapters = append(b.Chapters, Chapter{ref.IDRef, it.path, it.mediaType})
		}
	}
	b.Title, b.Author = dcText(d.Metadata.Inner, "title"), dcText(d.Metadata.Inner, "creator")
	b.Language = strings.ToLower(strings.SplitN(strings.SplitN(dcText(d.Metadata.Inner, "language"), "-", 2)[0], "_", 2)[0])
	// Cover: EPUB 3 properties, then EPUB 2 <meta name="cover">, then an item called "cover".
	for _, id := range order {
		if strings.Contains(" "+byID[id].properties+" ", " cover-image ") {
			b.CoverPath = byID[id].path
			break
		}
	}
	if b.CoverPath == "" {
		for _, m := range d.Metadata.Metas {
			if it, ok := byID[m.Content]; m.Name == "cover" && ok {
				b.CoverPath = it.path
				break
			}
		}
	}
	if b.CoverPath == "" {
		for _, id := range []string{"cover", "cover-image", "coverimage"} {
			if it, ok := byID[id]; ok && strings.HasPrefix(it.mediaType, "image/") {
				b.CoverPath = it.path
				break
			}
		}
	}
	return b, nil
}

// dcText finds the first non-empty element whose name, after any "dc:" prefix, is tag.
func dcText(inner []byte, tag string) string {
	dec := xml.NewDecoder(bytes.NewReader(inner))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok || !strings.EqualFold(se.Name.Local, tag) {
			continue
		}
		var s struct {
			Text string `xml:",chardata"`
		}
		if dec.DecodeElement(&s, &se) == nil {
			if t := strings.Join(strings.Fields(s.Text), " "); t != "" {
				return t
			}
		}
	}
}

// xmlUnmarshal is tolerant: books in the wild have HTML entities and sloppy markup. No
// HTML auto-closing though: OPF files use <meta property="...">value</meta>, and treating
// <meta> as an empty HTML tag made the closing tag break the parse (Gutenberg's EPUBs).
func xmlUnmarshal(b []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return dec.Decode(v)
}

// Open opens an .epub file and reads its package document.
func Open(file string) (*Book, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%s: not in the archive", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 64<<20))
	}
	cx, err := read(ContainerPath)
	if err != nil {
		zr.Close()
		return nil, err
	}
	opfPath, err := ContainerOPFPath(cx)
	if err != nil {
		zr.Close()
		return nil, err
	}
	opf, err := read(opfPath)
	if err != nil {
		zr.Close()
		return nil, err
	}
	b, err := ParseOPF(opf, opfPath)
	if err != nil {
		zr.Close()
		return nil, err
	}
	b.files, b.closer = files, zr
	if b.Title == "" {
		b.Title = strings.TrimSuffix(path.Base(file), path.Ext(file))
	}
	return b, nil
}

// Close releases the archive.
func (b *Book) Close() error {
	if b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// ReadFile returns a file from the archive by its path.
func (b *Book) ReadFile(name string) ([]byte, error) {
	f, ok := b.files[name]
	if !ok {
		return nil, fmt.Errorf("%s: not in the archive", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 64<<20))
}
