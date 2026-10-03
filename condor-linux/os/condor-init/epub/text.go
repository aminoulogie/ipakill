package epub

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// Block is one paragraph of a chapter's text.
type Block struct {
	Text    string
	Heading bool // h1..h6: drawn bold, with space around it
}

// blockTags start a new paragraph.
var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "blockquote": true,
	"li": true, "dt": true, "dd": true, "pre": true, "tr": true, "figcaption": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

var headingTags = map[string]bool{"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true}

// skipTags are never shown: scripts and styles (a book is a document), and the head.
var skipTags = map[string]bool{"script": true, "style": true, "head": true, "noscript": true, "iframe": true, "object": true, "embed": true}

// ChapterText turns a chapter's (X)HTML into paragraphs of plain text. HTML parsing is
// tolerant on purpose: an unescaped ampersand in one chapter should not cost the book.
func ChapterText(xhtml []byte) []Block {
	doc, err := html.Parse(bytes.NewReader(xhtml))
	if err != nil {
		return nil
	}
	var blocks []Block
	var cur strings.Builder
	heading := false
	flush := func() {
		if t := strings.Join(strings.Fields(cur.String()), " "); t != "" {
			blocks = append(blocks, Block{Text: t, Heading: heading})
		}
		cur.Reset()
		heading = false
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			cur.WriteString(n.Data)
			return
		case html.ElementNode:
			tag := strings.ToLower(n.Data)
			if i := strings.IndexByte(tag, ':'); i >= 0 {
				tag = tag[i+1:]
			}
			if skipTags[tag] {
				return
			}
			switch {
			case tag == "br":
				flush()
				return
			case tag == "img" || tag == "image":
				if alt := attr(n, "alt"); strings.TrimSpace(alt) != "" {
					flush()
					cur.WriteString("[" + strings.TrimSpace(alt) + "]")
					flush()
				}
				return
			case blockTags[tag]:
				flush()
				heading = headingTags[tag]
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				flush()
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	flush()
	return blocks
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// ChapterTitle is the chapter's own heading (h1, h2, h3, then <title>), for a contents list.
func ChapterTitle(xhtml []byte, fallback string) string {
	doc, err := html.Parse(bytes.NewReader(xhtml))
	if err != nil {
		return fallback
	}
	for _, want := range []string{"h1", "h2", "h3", "title"} {
		var found string
		var find func(n *html.Node)
		find = func(n *html.Node) {
			if found != "" {
				return
			}
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, want) {
				if t := strings.Join(strings.Fields(textOf(n)), " "); t != "" {
					found = t
					return
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				find(c)
			}
		}
		find(doc)
		if found != "" {
			if len([]rune(found)) > 80 {
				found = string([]rune(found)[:80])
			}
			return found
		}
	}
	return fallback
}

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return b.String()
}
