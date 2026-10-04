package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"condor-init/epub"
)

// The store's book sources. A search asks all of them at once; the same book found in
// several places becomes one item with several offers, and books you can read in full come
// first. Full books:
//   - Project Gutenberg (75,000+ public-domain books, clean EPUBs): gutendex.com's JSON API,
//     or gutenberg.org's own OPDS feed when gutendex is slow (opds.go)
//   - Internet Archive (archive.org): public-domain scans with generated EPUBs
//   - Google Books: public-domain volumes with an EPUB download
//   - Open Library: its "public" books are Internet Archive items
// Information only (to look at before buying elsewhere): Google Books volumes for sale
// (description, price) and Open Library's lending-only books.

var (
	gutendexURL      = "https://gutendex.com/books/"
	gutenbergBase    = "https://www.gutenberg.org"
	archiveBase      = "https://archive.org"
	openLibraryBase  = "https://openlibrary.org"
	openLibraryCover = "https://covers.openlibrary.org"
	googleBooksURL   = "https://www.googleapis.com/books/v1/volumes"
	storeDir         = "/data/media/0/Books" // the first of bookDirs: internal storage
	previewDir       = condorHome + "/previews"
)

// Sources, in the order they're preferred for the same book.
const (
	srcGutenberg = iota
	srcGoogle
	srcArchive
	srcOpenLibrary
	numSources
)

var sourceNames = [numSources]string{"Project Gutenberg", "Google Books", "Internet Archive", "Open Library"}
var sourceShort = [numSources]string{"gutenberg", "google", "archive.org", "openlibrary"}

// storeOffer is one source's copy of a book.
type storeOffer struct {
	src   int
	full  bool     // the whole book can be downloaded here
	urls  []string // EPUB downloads, best first
	iaID  string   // an Internet Archive item: its EPUB is found through its file list
	price string   // info only: e.g. "9.99 USD"
	note  string   // info only: why it can't be read here
	rank  int      // position in that source's results (relevance)
}

// storeItem is a book, merged across sources.
type storeItem struct {
	key           string
	title, author string
	cover         string // image URL
	summary       string
	subjects      []string
	langs         []string
	year          int
	downloads     int
	olWork        string // Open Library work key, for a summary on demand
	offers        []storeOffer
}

func (it *storeItem) full() bool {
	for _, o := range it.offers {
		if o.full {
			return true
		}
	}
	return false
}

// score orders results: full books first, then by relevance, then by preferred source.
func (it *storeItem) score() int {
	best := 1 << 30
	for _, o := range it.offers {
		s := o.rank*3 + o.src
		if o.full {
			s -= 1 << 20
		}
		best = min(best, s)
	}
	return best
}

var unsafeName = regexp.MustCompile(`[^\pL\pN .,'()-]+`)

// fileName is "Title - Author.epub", safe on every filesystem.
func (it *storeItem) fileName() string {
	title, _, _ := strings.Cut(it.title, ";") // "Frankenstein; Or, The Modern Prometheus"
	n := strings.TrimSpace(unsafeName.ReplaceAllString(title+" - "+it.author, " "))
	if r := []rune(n); len(r) > 100 {
		n = string(r[:100])
	}
	return n + ".epub"
}

func hash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

// normKey makes "Frankenstein; or, the Modern Prometheus" by "Shelley, Mary" and
// "Frankenstein" by "Mary W. Shelley" the same book: the title up to its first ; : ( or ,
// in letters and digits, plus the author's surname.
func normKey(title, author string) string {
	t := strings.ToLower(title)
	if i := strings.IndexAny(t, ";:(,["); i > 0 {
		t = t[:i]
	}
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, s)
	}
	surname := ""
	a := strings.ToLower(author)
	if first, _, ok := strings.Cut(a, ","); ok && !strings.Contains(first, " ") {
		surname = first // "shelley, mary"
	} else if f := strings.Fields(strings.Split(a, ",")[0]); len(f) > 0 {
		surname = f[len(f)-1] // "mary w. shelley"
	}
	return clean(t) + "|" + clean(surname)
}

// personName turns "Shelley, Mary Wollstonecraft" into "Mary Wollstonecraft Shelley".
func personName(n string) string {
	n = strings.TrimSpace(n)
	if last, first, ok := strings.Cut(n, ", "); ok && !strings.ContainsAny(first, ",") {
		if i := strings.IndexByte(first, '('); i > 0 { // "Twain, Mark (Samuel Clemens)"
			first = strings.TrimSpace(first[:i])
		}
		n = first + " " + last
	}
	return n
}

// mergeItems adds new items into list: a book already there gains the new offers and any
// details it lacked. The result is sorted best first.
func mergeItems(list, add []*storeItem) []*storeItem {
	byKey := map[string]*storeItem{}
	for _, it := range list {
		byKey[it.key] = it
	}
	for _, n := range add {
		if n.key == "" {
			n.key = normKey(n.title, n.author)
		}
		old := byKey[n.key]
		if old != nil && sameSource(old, n) { // one library's separate editions stay apart
			for i := 2; byKey[n.key] != nil; i++ {
				n.key = fmt.Sprintf("%s#%d", strings.SplitN(n.key, "#", 2)[0], i)
			}
			old = nil
		}
		if old == nil {
			byKey[n.key] = n
			list = append(list, n)
			continue
		}
		better := n.offers[0].src < bestSource(old)
		for _, o := range n.offers {
			dup := false
			for _, e := range old.offers {
				dup = dup || (e.src == o.src && e.iaID == o.iaID && strings.Join(e.urls, " ") == strings.Join(o.urls, " "))
			}
			if !dup {
				old.offers = append(old.offers, o)
			}
		}
		// (better, above:) the preferred library's details win (Gutenberg's titles and covers are the cleanest),
		// whatever order the answers came in.
		if old.cover == "" || (better && n.cover != "") {
			old.cover = n.cover
		}
		if old.summary == "" || (better && n.summary != "") {
			old.summary = n.summary
		}
		if better {
			old.title, old.author = n.title, n.author
		}
		if old.year == 0 || (n.year > 0 && n.year < old.year) {
			old.year = n.year
		}
		if old.olWork == "" {
			old.olWork = n.olWork
		}
		old.downloads = max(old.downloads, n.downloads)
		if len(old.subjects) == 0 {
			old.subjects = n.subjects
		}
	}
	for _, it := range list {
		sort.SliceStable(it.offers, func(i, j int) bool {
			a, b := it.offers[i], it.offers[j]
			if a.full != b.full {
				return a.full
			}
			return a.src < b.src
		})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score() < list[j].score() })
	return list
}

func bestSource(it *storeItem) int {
	best := numSources
	for _, o := range it.offers {
		best = min(best, o.src)
	}
	return best
}

func sameSource(a, b *storeItem) bool {
	for _, x := range a.offers {
		for _, y := range b.offers {
			if x.src == y.src {
				return true
			}
		}
	}
	return false
}

// storeSearch is what to look for.
type storeSearch struct {
	query, topic, lang string
	page               int // 1-based, for browsing Gutenberg
}

type sourceResult struct {
	items   []*storeItem
	hasNext bool
}

// --- Project Gutenberg -------------------------------------------------------------------

type gbook struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Summaries []string          `json:"summaries"`
	Subjects  []string          `json:"subjects"`
	Languages []string          `json:"languages"`
	Formats   map[string]string `json:"formats"`
	Downloads int               `json:"download_count"`
}

// gutenbergItem is a Gutenberg book as a store item. The reader shows text only, so the
// no-images EPUB (often a tenth of the size) is tried first.
func gutenbergItem(id int, title string, authors []string, rank int) *storeItem {
	var names []string
	for _, a := range authors {
		names = append(names, personName(a))
	}
	author := strings.Join(names, ", ")
	if author == "" {
		author = "unknown author"
	}
	return &storeItem{
		title: strings.Join(strings.Fields(title), " "), author: author,
		cover: fmt.Sprintf("%s/cache/epub/%d/pg%d.cover.medium.jpg", gutenbergBase, id, id),
		offers: []storeOffer{{src: srcGutenberg, full: true, rank: rank, urls: []string{
			fmt.Sprintf("%s/ebooks/%d.epub.noimages", gutenbergBase, id),
			fmt.Sprintf("%s/ebooks/%d.epub3.images", gutenbergBase, id),
		}}},
	}
}

func searchGutendex(ctx context.Context, q storeSearch) (sourceResult, error) {
	v := url.Values{}
	v.Set("mime_type", "application/epub")
	if q.query != "" {
		v.Set("search", q.query)
	}
	if q.topic != "" {
		v.Set("topic", q.topic)
	}
	if q.lang != "" {
		v.Set("languages", q.lang)
	}
	if q.page > 1 {
		v.Set("page", fmt.Sprint(q.page))
	}
	var res struct {
		Next    string  `json:"next"`
		Results []gbook `json:"results"`
	}
	if err := getJSON(ctx, gutendexURL+"?"+v.Encode(), &res); err != nil {
		return sourceResult{}, err
	}
	out := sourceResult{hasNext: res.Next != ""}
	for i, b := range res.Results {
		var authors []string
		for _, a := range b.Authors {
			authors = append(authors, a.Name)
		}
		it := gutenbergItem(b.ID, b.Title, authors, i)
		it.summary = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.Join(b.Summaries, " ")),
			"(This is an automatically generated summary.)"))
		it.subjects, it.langs, it.downloads = b.Subjects, b.Languages, b.Downloads
		if u := b.Formats["image/jpeg"]; u != "" {
			it.cover = u
		}
		for mt, u := range b.Formats {
			if strings.HasPrefix(mt, "application/epub") {
				it.offers[0].urls = append(it.offers[0].urls, u)
			}
		}
		out.items = append(out.items, it)
	}
	return out, nil
}

// gutenbergTimeout is how long gutendex may take before gutenberg.org's feed is asked.
var gutenbergTimeout = 25 * time.Second

// searchGutenberg asks gutendex.com, then gutenberg.org's own feed; preferOPDS remembers
// that gutendex failed, so the next search asks gutenberg.org first.
func searchGutenberg(ctx context.Context, q storeSearch, preferOPDS *bool) (sourceResult, error) {
	type try struct {
		name string
		f    func(context.Context, storeSearch) (sourceResult, error)
	}
	tries := []try{{"gutendex.com", searchGutendex}, {"gutenberg.org", searchGutenbergOPDS}}
	if *preferOPDS {
		tries[0], tries[1] = tries[1], tries[0]
	}
	var errs []string
	for _, t := range tries {
		c, cancel := context.WithTimeout(ctx, gutenbergTimeout)
		res, err := t.f(c, q)
		cancel()
		if err == nil {
			*preferOPDS = t.name == "gutenberg.org"
			return res, nil
		}
		log.Printf("store: %s: %v", t.name, err)
		errs = append(errs, t.name+": "+shortErr(err))
	}
	return sourceResult{}, fmt.Errorf("%s", strings.Join(errs, "; "))
}

// --- Internet Archive --------------------------------------------------------------------

// iaLang is Internet Archive's and Open Library's language code (MARC).
var iaLang = map[string]string{"en": "eng", "fr": "fre", "es": "spa", "de": "ger", "it": "ita", "ar": "ara"}

// flexStrings decodes a JSON string or array of strings.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*f = flexStrings{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return nil // numbers, objects: ignore
	}
	*f = many
	return nil
}

func searchArchive(ctx context.Context, q storeSearch) (sourceResult, error) {
	terms := []string{"mediatype:texts", "format:EPUB", "-access-restricted-item:true"}
	if q.query != "" {
		terms = append(terms, "("+q.query+")")
	}
	if l := iaLang[q.lang]; l != "" {
		terms = append(terms, "language:("+l+")")
	}
	v := url.Values{}
	v.Set("q", strings.Join(terms, " AND "))
	for _, f := range []string{"identifier", "title", "creator", "downloads", "description", "year", "language"} {
		v.Add("fl[]", f)
	}
	v.Set("sort[]", "downloads desc")
	v.Set("rows", "24")
	v.Set("output", "json")
	var res struct {
		Response struct {
			Docs []struct {
				ID          string      `json:"identifier"`
				Title       flexStrings `json:"title"`
				Creator     flexStrings `json:"creator"`
				Description flexStrings `json:"description"`
				Language    flexStrings `json:"language"`
				Downloads   int         `json:"downloads"`
				Year        json.Number `json:"year"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := getJSON(ctx, archiveBase+"/advancedsearch.php?"+v.Encode(), &res); err != nil {
		return sourceResult{}, err
	}
	var out sourceResult
	for i, d := range res.Response.Docs {
		if d.ID == "" || len(d.Title) == 0 {
			continue
		}
		author := "unknown author"
		if len(d.Creator) > 0 {
			author = personName(d.Creator[0])
		}
		year, _ := strconv.Atoi(string(d.Year))
		out.items = append(out.items, &storeItem{
			title: d.Title[0], author: author, year: year, downloads: d.Downloads, langs: d.Language,
			summary: stripTags(strings.Join(d.Description, " ")),
			cover:   archiveBase + "/services/img/" + url.PathEscape(d.ID),
			offers:  []storeOffer{{src: srcArchive, full: true, iaID: d.ID, rank: i}},
		})
	}
	return out, nil
}

var reTag = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string {
	s = strings.Join(strings.Fields(reTag.ReplaceAllString(s, " ")), " ")
	// A tag closed just before punctuation leaves a space in front of it ("asylum , refuge").
	return reSpacePunct.ReplaceAllString(s, "$1")
}

var reSpacePunct = regexp.MustCompile(` ([,.)])`)

// archiveEPUBs lists an Internet Archive item's EPUB files.
func archiveEPUBs(ctx context.Context, id string) ([]string, error) {
	var md struct {
		Files []struct {
			Name   string `json:"name"`
			Format string `json:"format"`
		} `json:"files"`
	}
	if err := getJSON(ctx, archiveBase+"/metadata/"+url.PathEscape(id), &md); err != nil {
		return nil, err
	}
	var urls []string
	for _, f := range md.Files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".epub") {
			urls = append(urls, archiveBase+"/download/"+url.PathEscape(id)+"/"+url.PathEscape(f.Name))
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no EPUB in archive.org item %s", id)
	}
	return urls, nil
}

// --- Open Library ------------------------------------------------------------------------

func searchOpenLibrary(ctx context.Context, q storeSearch) (sourceResult, error) {
	if q.query == "" {
		return sourceResult{}, nil
	}
	v := url.Values{}
	v.Set("q", q.query)
	v.Set("fields", "key,title,author_name,cover_i,ebook_access,ia,first_publish_year,language")
	v.Set("limit", "20")
	if l := iaLang[q.lang]; l != "" {
		v.Set("language", l)
	}
	var res struct {
		Docs []struct {
			Key     string   `json:"key"`
			Title   string   `json:"title"`
			Authors []string `json:"author_name"`
			Cover   int      `json:"cover_i"`
			Access  string   `json:"ebook_access"`
			IA      []string `json:"ia"`
			Year    int      `json:"first_publish_year"`
			Langs   []string `json:"language"`
		} `json:"docs"`
	}
	if err := getJSON(ctx, openLibraryBase+"/search.json?"+v.Encode(), &res); err != nil {
		return sourceResult{}, err
	}
	var out sourceResult
	for i, d := range res.Docs {
		if d.Title == "" {
			continue
		}
		author := "unknown author"
		if len(d.Authors) > 0 {
			author = d.Authors[0]
		}
		it := &storeItem{title: d.Title, author: author, year: d.Year, langs: d.Langs, olWork: d.Key}
		if d.Cover > 0 {
			it.cover = fmt.Sprintf("%s/b/id/%d-M.jpg", openLibraryCover, d.Cover)
		}
		o := storeOffer{src: srcOpenLibrary, rank: i}
		switch {
		case d.Access == "public" && len(d.IA) > 0:
			o.full, o.iaID = true, d.IA[0]
		case d.Access == "borrowable" || d.Access == "printdisabled":
			o.note = "lending only on openlibrary.org (needs an account and a web browser)"
		default:
			o.note = "no ebook in the library: look for it in a shop"
		}
		it.offers = []storeOffer{o}
		out.items = append(out.items, it)
	}
	return out, nil
}

// openLibrarySummary is a work's description, fetched when its page opens.
func openLibrarySummary(ctx context.Context, work string) string {
	var w struct {
		Description json.RawMessage `json:"description"`
	}
	if getJSON(ctx, openLibraryBase+work+".json", &w) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(w.Description, &s) != nil {
		var obj struct {
			Value string `json:"value"`
		}
		json.Unmarshal(w.Description, &obj)
		s = obj.Value
	}
	return strings.TrimSpace(s)
}

// --- Google Books ------------------------------------------------------------------------

func searchGoogle(ctx context.Context, q storeSearch) (sourceResult, error) {
	if q.query == "" {
		return sourceResult{}, nil
	}
	v := url.Values{}
	v.Set("q", q.query)
	v.Set("maxResults", "20")
	v.Set("printType", "books")
	if q.lang != "" {
		v.Set("langRestrict", q.lang)
	}
	var res struct {
		Items []struct {
			ID   string `json:"id"`
			Info struct {
				Title       string   `json:"title"`
				Subtitle    string   `json:"subtitle"`
				Authors     []string `json:"authors"`
				Published   string   `json:"publishedDate"`
				Description string   `json:"description"`
				Language    string   `json:"language"`
				Categories  []string `json:"categories"`
				Images      struct {
					Thumbnail string `json:"thumbnail"`
				} `json:"imageLinks"`
			} `json:"volumeInfo"`
			Sale struct {
				Saleability string `json:"saleability"`
				Price       *struct {
					Amount   float64 `json:"amount"`
					Currency string  `json:"currencyCode"`
				} `json:"listPrice"`
			} `json:"saleInfo"`
			Access struct {
				PublicDomain bool   `json:"publicDomain"`
				View         string `json:"accessViewStatus"`
				EPUB         struct {
					Download string `json:"downloadLink"`
				} `json:"epub"`
			} `json:"accessInfo"`
		} `json:"items"`
	}
	if err := getJSON(ctx, googleBooksURL+"?"+v.Encode(), &res); err != nil {
		return sourceResult{}, err
	}
	var out sourceResult
	for i, g := range res.Items {
		if g.Info.Title == "" {
			continue
		}
		author := "unknown author"
		if len(g.Info.Authors) > 0 {
			author = g.Info.Authors[0]
		}
		year, _ := strconv.Atoi(strings.SplitN(g.Info.Published, "-", 2)[0])
		it := &storeItem{title: g.Info.Title, author: author, year: year, summary: stripTags(g.Info.Description),
			subjects: g.Info.Categories, cover: strings.Replace(g.Info.Images.Thumbnail, "http://", "https://", 1)}
		if g.Info.Language != "" {
			it.langs = []string{g.Info.Language}
		}
		o := storeOffer{src: srcGoogle, rank: i}
		switch {
		case g.Access.EPUB.Download != "" && (g.Access.PublicDomain || g.Sale.Saleability == "FREE"):
			o.full, o.urls = true, []string{strings.Replace(g.Access.EPUB.Download, "http://", "https://", 1)}
		case g.Sale.Price != nil:
			o.price = fmt.Sprintf("%.2f %s", g.Sale.Price.Amount, g.Sale.Price.Currency)
			o.note = "sold on Google Play Books: buy it there, then copy the EPUB here if it has no DRM"
		default:
			o.note = "not for sale as an ebook on Google Books"
		}
		it.offers = []storeOffer{o}
		out.items = append(out.items, it)
	}
	return out, nil
}

// --- downloads ---------------------------------------------------------------------------

func getJSON(ctx context.Context, u string, v any) error {
	resp, err := webGetCtx(ctx, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// fetchItem downloads the book to dst from its full offers in turn, checking that it opens.
// progress gets the bytes so far.
func fetchItem(it *storeItem, dst string, progress func(int64)) error {
	var errs []string
	for _, o := range it.offers {
		if !o.full {
			continue
		}
		urls := o.urls
		if o.iaID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			u, err := archiveEPUBs(ctx, o.iaID)
			cancel()
			if err != nil {
				errs = append(errs, sourceShort[o.src]+": "+shortErr(err))
				continue
			}
			urls = u
		}
		for _, u := range urls {
			err := fetchEPUB(u, dst, progress)
			if err == nil {
				return nil
			}
			log.Printf("store: %s: %v", u, err)
			errs = append(errs, sourceShort[o.src]+": "+shortErr(err))
		}
	}
	if len(errs) == 0 {
		return fmt.Errorf("no source has the full book")
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

func fetchEPUB(u, dst string, progress func(int64)) error {
	resp, err := webGet(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var n int64
	buf := make([]byte, 64<<10)
	for err == nil {
		var k int
		k, err = resp.Body.Read(buf)
		if k > 0 {
			if _, werr := f.Write(buf[:k]); werr != nil {
				err = werr
				break
			}
			n += int64(k)
			if n > 200<<20 {
				err = fmt.Errorf("book too big")
				break
			}
			progress(n)
		}
	}
	f.Close()
	if err != io.EOF {
		os.Remove(tmp)
		return err
	}
	if eb, err := epub.Open(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("not a readable EPUB: %v", err)
	} else {
		eb.Close()
	}
	return os.Rename(tmp, dst)
}
