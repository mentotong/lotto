package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/a-h/templ"
)

// Page sizes for the long lists.
const (
	perPageTickets = 25
	perPageUsers   = 25
	perPageAgents  = 24 // fills 1, 2 or 3 card columns evenly
)

// Pager describes one page of a longer list and builds links to the
// other pages, keeping the current search and filters.
type Pager struct {
	Page, Per, Total int
	Path             string
	Params           url.Values // current search and filters, without "page"
}

// newPager reads ?page= and keeps it inside the valid range.
func newPager(r *http.Request, path string, per, total int) Pager {
	params := url.Values{}
	for k, vs := range r.URL.Query() {
		if k == "page" {
			continue
		}
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				params.Add(k, v)
			}
		}
	}
	p := Pager{Per: per, Total: total, Path: path, Params: params}
	p.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Page > p.Pages() {
		p.Page = p.Pages()
	}
	return p
}

func (p Pager) Pages() int {
	if p.Total == 0 {
		return 1
	}
	return (p.Total + p.Per - 1) / p.Per
}

// bounds are the slice indexes of the current page.
func (p Pager) bounds() (int, int) {
	start := (p.Page - 1) * p.Per
	end := start + p.Per
	if end > p.Total {
		end = p.Total
	}
	return start, end
}

// Summary is "26–50 of 312" (or "3 of 3" when it all fits).
func (p Pager) Summary() string {
	start, end := p.bounds()
	if p.Total == 0 {
		return "0"
	}
	if p.Pages() == 1 {
		return fmt.Sprintf("%d of %d", p.Total, p.Total)
	}
	return fmt.Sprintf("%d–%d of %d", start+1, end, p.Total)
}

func (p Pager) URL(page int) templ.SafeURL {
	q := url.Values{}
	for k, vs := range p.Params {
		q[k] = vs
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return templ.SafeURL(p.Path)
	}
	return templ.SafeURL(p.Path + "?" + q.Encode())
}

// pageLink is one numbered link, or a gap ("…") between numbers.
type pageLink struct {
	N   int
	Gap bool
}

// Links lists the first, last and nearby pages, with gaps between.
func (p Pager) Links() []pageLink {
	var out []pageLink
	last := 0
	for n := 1; n <= p.Pages(); n++ {
		if n == 1 || n == p.Pages() || (n >= p.Page-1 && n <= p.Page+1) {
			if last != 0 && n > last+1 {
				out = append(out, pageLink{Gap: true})
			}
			out = append(out, pageLink{N: n})
			last = n
		}
	}
	return out
}

// pageOf returns the items on the pager's current page.
func pageOf[T any](items []T, p Pager) []T {
	start, end := p.bounds()
	if start >= len(items) {
		return nil
	}
	return items[start:end]
}

// --- search -------------------------------------------------------------------

var accentFolder = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// fold makes text comparable: lower case and without accents, so
// "agencia" finds "Agência".
func fold(s string) string { return accentFolder.Replace(strings.ToLower(s)) }

// Search is a parsed search box: every word must match some field.
type Search struct {
	Raw   string
	terms []string
}

func newSearch(raw string) Search {
	raw = strings.TrimSpace(raw)
	if len(raw) > 100 {
		raw = raw[:100]
	}
	return Search{Raw: raw, terms: strings.Fields(fold(raw))}
}

func (s Search) Empty() bool { return len(s.terms) == 0 }

// Match reports whether every search word appears in at least one field.
func (s Search) Match(fields ...string) bool {
	if s.Empty() {
		return true
	}
	hay := fold(strings.Join(fields, "\x00"))
	for _, t := range s.terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// nounFor is "ticket" for 1 and "tickets" otherwise.
func nounFor(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return pluralWord(noun)
}
