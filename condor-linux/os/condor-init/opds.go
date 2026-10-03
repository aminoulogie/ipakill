package main

import (
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Project Gutenberg's own catalogue feed (OPDS 1.1, Atom XML): the store's fallback when
// gutendex.com doesn't answer. Search results list each book as an entry whose id is
// .../ebooks/<number>.opds, with the title and the author (as the entry's text content);
// the EPUB itself is then /ebooks/<number>.epub3.images, as for gutendex.

var gutenbergOPDSURL = "https://www.gutenberg.org/ebooks/search.opds/"

const opdsPageSize = 25

type opdsLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type opdsFeed struct {
	Total   int        `xml:"totalResults"`
	Links   []opdsLink `xml:"link"`
	Entries []struct {
		Title   string `xml:"title"`
		Content string `xml:"content"`
		ID      string `xml:"id"`
		Authors []struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Links []opdsLink `xml:"link"`
	} `xml:"entry"`
}

var (
	reEbookID   = regexp.MustCompile(`/ebooks/(\d+)(?:\.opds)?/?$`)
	reDownloads = regexp.MustCompile(`(?i)^\s*([\d,]+)\s+downloads?\s*$`)
)

// opdsTerms is Gutenberg's search syntax: words, s.<subject word>, l.<language>.
func opdsTerms(q storeSearch) string {
	terms := strings.Fields(q.query)
	for _, w := range strings.Fields(q.topic) {
		terms = append(terms, "s."+w)
	}
	if q.lang != "" {
		terms = append(terms, "l."+q.lang)
	}
	return strings.Join(terms, " ")
}

func searchGutenbergOPDS(ctx context.Context, q storeSearch) (sourceResult, error) {
	v := url.Values{}
	if t := opdsTerms(q); t != "" {
		v.Set("query", t)
	}
	v.Set("sort_order", "downloads")
	if q.page > 1 {
		v.Set("start_index", strconv.Itoa((q.page-1)*opdsPageSize+1))
	}
	resp, err := webGetCtx(ctx, gutenbergOPDSURL+"?"+v.Encode())
	if err != nil {
		return sourceResult{}, err
	}
	defer resp.Body.Close()
	return parseOPDS(io.LimitReader(resp.Body, 8<<20))
}

func parseOPDS(r io.Reader) (sourceResult, error) {
	var f opdsFeed
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&f); err != nil {
		return sourceResult{}, err
	}
	var res sourceResult
	for _, l := range f.Links {
		if l.Rel == "next" {
			res.hasNext = true
		}
	}
	for _, e := range f.Entries {
		id := 0
		for _, ref := range append([]string{strings.TrimSpace(e.ID)}, hrefs(e.Links)...) {
			if m := reEbookID.FindStringSubmatch(ref); m != nil {
				id, _ = strconv.Atoi(m[1])
				break
			}
		}
		if id == 0 { // "sort alphabetically" and other navigation entries
			continue
		}
		author, downloads := strings.TrimSpace(e.Content), 0
		if m := reDownloads.FindStringSubmatch(author); m != nil {
			downloads, _ = strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
			author = ""
		}
		if author == "" && len(e.Authors) > 0 {
			author = e.Authors[0].Name
		}
		var authors []string
		if author != "" {
			authors = []string{author}
		}
		it := gutenbergItem(id, e.Title, authors, len(res.items))
		it.downloads = downloads
		res.items = append(res.items, it)
	}
	return res, nil
}

func hrefs(ls []opdsLink) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Href)
	}
	return out
}
