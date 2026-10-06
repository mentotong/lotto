package main

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/a-h/templ"
)

// DrawResult is the outcome of one draw: three top prizes, ten Starter
// and ten Consolation numbers, all 4 digits and all different.
type DrawResult struct {
	Date        string     `json:"date"` // YYYY-MM-DD
	First       string     `json:"first"`
	Second      string     `json:"second"`
	Third       string     `json:"third"`
	Starter     [10]string `json:"starter"`
	Consolation [10]string `json:"consolation"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	UpdatedAt   time.Time  `json:"updated_at"`
	UpdatedBy   string     `json:"updated_by"`
	// Draft results are saved but not published: only admins can see
	// them. Results from before drafts existed count as published.
	Draft  bool          `json:"draft,omitempty"`
	Events []ResultEvent `json:"events,omitempty"` // oldest first
}

// ResultEvent records who saved, published, corrected or unpublished a
// draw's results, and when.
type ResultEvent struct {
	Action string    `json:"action"`
	At     time.Time `json:"at"`
	By     string    `json:"by"`
}

const (
	resDraft       = "saved as draft"
	resPublished   = "published"
	resCorrected   = "corrected"
	resUnpublished = "unpublished"
)

// Published is when and by whom the results were last published.
func (d DrawResult) Published() (ResultEvent, bool) {
	if d.Draft {
		return ResultEvent{}, false
	}
	for i := len(d.Events) - 1; i >= 0; i-- {
		if d.Events[i].Action == resPublished {
			return d.Events[i], true
		}
	}
	return ResultEvent{Action: resPublished, At: d.CreatedAt, By: d.CreatedBy}, true
}

// Numbers lists all 23 numbers in order: 1st, 2nd, 3rd, Starter, Consolation.
func (d DrawResult) Numbers() []string {
	out := []string{d.First, d.Second, d.Third}
	out = append(out, d.Starter[:]...)
	return append(out, d.Consolation[:]...)
}

func (d DrawResult) Edited() bool { return !d.UpdatedAt.Equal(d.CreatedAt) }

// --- store ------------------------------------------------------------------

func (s *Store) findResult(date string) *DrawResult {
	for _, r := range s.d.Results {
		if r.Date == date {
			return r
		}
	}
	return nil
}

func (s *Store) Result(date string) (DrawResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r := s.findResult(date); r != nil {
		return *r, true
	}
	return DrawResult{}, false
}

// ResultDates lists the draw dates that have results, newest first.
// Drafts are included only for admins.
func (s *Store) ResultDates(withDrafts bool) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.d.Results))
	for _, r := range s.d.Results {
		if !r.Draft || withDrafts {
			out = append(out, r.Date)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// SaveResult adds or changes a draw's results. Published results stay
// published (the change is recorded as a correction); new results and
// drafts are published only when publish is true.
func (s *Store) SaveResult(r DrawResult, by string, publish bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	r.UpdatedAt, r.UpdatedBy = now, by
	action := resDraft
	if cur := s.findResult(r.Date); cur != nil {
		r.CreatedAt, r.CreatedBy, r.Events = cur.CreatedAt, cur.CreatedBy, cur.Events
		switch {
		case !cur.Draft:
			action = resCorrected
		case publish:
			action = resPublished
		}
		r.Draft = cur.Draft && !publish
		*cur = r
	} else {
		r.CreatedAt, r.CreatedBy = now, by
		r.Draft = !publish
		if publish {
			action = resPublished
		}
		s.d.Results = append(s.d.Results, &r)
	}
	cur := s.findResult(r.Date)
	cur.Events = append(cur.Events, ResultEvent{Action: action, At: now, By: by})
	return action, s.save()
}

// SetPublished publishes a draft, or takes published results back to a
// draft (hidden from everyone but admins).
func (s *Store) SetPublished(date string, publish bool, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.findResult(date)
	if cur == nil {
		return errors.New("No results for that date.")
	}
	if cur.Draft == !publish {
		return nil // already in that state
	}
	cur.Draft = !publish
	action := resPublished
	if !publish {
		action = resUnpublished
	}
	cur.Events = append(cur.Events, ResultEvent{Action: action, At: time.Now(), By: by})
	return s.save()
}

// --- handlers -----------------------------------------------------------------

// ResultsView is the results page: one draw plus its neighbours.
type ResultsView struct {
	Date       string // the draw shown ("" when there are no results)
	Result     *DrawResult
	Prev, Next string // neighbouring draw dates with results
	Dates      []string
	Asked      string // a date asked for that has no results
	Share      *ResultsShare
}

// ResultsShare is what the "Share results" image needs: the public link
// to this draw and a QR code for it.
type ResultsShare struct {
	Link      string
	QR        templ.Component
	LocalOnly bool
}

// resultsPage is the main page. It's public, so buyers can check results
// without an account; signed-in staff also get the app's navigation.
func (a *app) resultsPage(w http.ResponseWriter, r *http.Request) {
	if !a.store.HasUsers() {
		redirect(w, r, "/setup")
		return
	}
	if u, ok := a.loadUser(r); ok {
		r = r.WithContext(a.withUser(r.Context(), u))
	}
	admin := userOf(r.Context()).Can(PermResults)
	dates := a.store.ResultDates(admin) // only admins see drafts
	v := ResultsView{Dates: dates}
	want := r.URL.Query().Get("date")
	if want == "" && len(dates) > 0 {
		want = dates[0]
	}
	for i, d := range dates {
		if d == want {
			res, _ := a.store.Result(d)
			v.Date, v.Result = d, &res
			if i+1 < len(dates) {
				v.Prev = dates[i+1]
			}
			if i > 0 {
				v.Next = dates[i-1]
			}
		}
	}
	if v.Result == nil && want != "" {
		v.Asked = want
		// Offer the nearest draws around the date that was asked for.
		for _, d := range dates {
			if d < want && v.Prev == "" {
				v.Prev = d
			}
			if d > want {
				v.Next = d
			}
		}
	}
	if v.Result != nil && !v.Result.Draft {
		base := a.publicBase(r)
		link := base + "/?date=" + v.Date
		host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
		v.Share = &ResultsShare{Link: link, QR: qrSVG(link), LocalOnly: isLocalHost(host)}
	}
	title := "Results"
	if v.Result != nil {
		title = "Results " + longDate(v.Date)
	}
	page(w, r, http.StatusOK, title, "results", ResultsPage(v))
}

// ResultForm holds the 23 boxes of the results form as typed.
type ResultForm struct {
	Date        string
	Top         [3]string
	Starter     [10]string
	Consolation [10]string
	Existing    bool
	Published   bool // existing results are already public
	New         bool // "Enter results": a draw that has no results yet
}

// newResultPage serves the results form. With ?date= (Edit draft or
// Correct on a draw) it's filled in with that draw's numbers. Without it
// ("Enter results") it's always empty: a new draw, dated today unless
// today already has results.
func (a *app) newResultPage(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if res, ok := a.store.Result(date); ok && date != "" {
		f := ResultForm{Date: date, Top: [3]string{res.First, res.Second, res.Third}, Starter: res.Starter, Consolation: res.Consolation, Existing: true, Published: !res.Draft}
		formPage(w, r, http.StatusOK, "Enter results", "results", ResultDialog(f, nil), ResultFormView(f, nil))
		return
	}
	f := ResultForm{New: true, Date: today()}
	if _, taken := a.store.Result(f.Date); taken {
		f.Date = "" // today is done; pick the draw date
	}
	formPage(w, r, http.StatusOK, "Enter results", "results", ResultDialog(f, nil), ResultFormView(f, nil))
}

var errResultDigits = errors.New("must be exactly 4 digits")

func cleanNumber(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) != 4 {
		return s, errResultDigits
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return s, errResultDigits
		}
	}
	return s, nil
}

func (a *app) saveResult(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := ResultForm{Date: r.FormValue("date"), New: r.FormValue("mode") == "new"}
	get := func(name string, i int) string {
		if vs := r.Form[name]; i < len(vs) {
			return strings.TrimSpace(vs[i])
		}
		return ""
	}
	for i := range f.Top {
		f.Top[i] = get("top", i)
	}
	for i := 0; i < 10; i++ {
		f.Starter[i] = get("starter", i)
		f.Consolation[i] = get("consolation", i)
	}
	if cur, ok := a.store.Result(f.Date); ok && !f.New {
		f.Existing, f.Published = true, !cur.Draft
	}

	var errs []string
	d, err := time.Parse("2006-01-02", f.Date)
	if err != nil {
		errs = append(errs, "Pick the draw date.")
	} else if d.After(time.Now()) {
		errs = append(errs, "The draw date can't be in the future.")
	} else if _, taken := a.store.Result(f.Date); taken && f.New {
		// A new entry never overwrites a draw that already has results.
		errs = append(errs, "Results for "+longDate(f.Date)+" already exist. Pick another date, or open that draw and use Edit draft or Correct.")
	}
	type slot struct{ label, value string }
	var slots []slot
	for i, label := range []string{"1st prize", "2nd prize", "3rd prize"} {
		slots = append(slots, slot{label, f.Top[i]})
	}
	for i := range f.Starter {
		slots = append(slots, slot{fmt.Sprintf("Starter %d", i+1), f.Starter[i]})
	}
	for i := range f.Consolation {
		slots = append(slots, slot{fmt.Sprintf("Consolation %d", i+1), f.Consolation[i]})
	}
	seen := map[string]string{}
	missing := 0
	for _, s := range slots {
		if s.value == "" {
			missing++
			continue
		}
		if _, err := cleanNumber(s.value); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %q %v.", s.label, s.value, err))
			continue
		}
		if first, dup := seen[s.value]; dup {
			errs = append(errs, fmt.Sprintf("%s: %s is already the %s. All 23 numbers must be different.", s.label, s.value, first))
			continue
		}
		seen[s.value] = s.label
	}
	if missing > 0 {
		errs = append([]string{fmt.Sprintf("Fill in all 23 numbers (%d still empty).", missing)}, errs...)
	}
	if len(errs) > 0 {
		formPage(w, r, http.StatusUnprocessableEntity, "Enter results", "results", ResultDialog(f, errs), ResultFormView(f, errs))
		return
	}
	me, _ := CurrentUser(r.Context())
	res := DrawResult{Date: f.Date, First: f.Top[0], Second: f.Top[1], Third: f.Top[2], Starter: f.Starter, Consolation: f.Consolation}
	action, err := a.store.SaveResult(res, me.Name, r.FormValue("action") == "publish")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.record(r, "results", "results."+action, "/?date="+f.Date, "%s results for %s (1st %s, 2nd %s, 3rd %s)",
		map[string]string{resDraft: "Saved draft", resPublished: "Published", resCorrected: "Corrected"}[action], longDate(f.Date), res.First, res.Second, res.Third)
	msg := map[string]string{
		resDraft:     "Results for " + longDate(f.Date) + " saved as a draft. Only admins can see them until you publish.",
		resPublished: "Results for " + longDate(f.Date) + " published. Everyone can see them now.",
		resCorrected: "Results for " + longDate(f.Date) + " corrected.",
	}[action]
	formDone(w, r, "/?date="+f.Date, msg)
}

// resultAction confirms (GET) and carries out (POST) publishing or
// unpublishing a draw's results.
func (a *app) resultAction(w http.ResponseWriter, r *http.Request) {
	date, action := r.PathValue("date"), r.PathValue("action")
	res, ok := a.store.Result(date)
	if !ok || (action != "publish" && action != "unpublish") {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		formPage(w, r, http.StatusOK, "Publish results", "results", ResultConfirmDialog(res, action), ResultConfirmForm(res, action))
		return
	}
	me, _ := CurrentUser(r.Context())
	if err := a.store.SetPublished(date, action == "publish", me.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.record(r, "results", "results."+action+"ed", "/?date="+date, "%sed results for %s", strings.ToUpper(action[:1])+action[1:], longDate(date))
	msg := "Results for " + longDate(date) + " published. Everyone can see them now."
	if action == "unpublish" {
		msg = "Results for " + longDate(date) + " unpublished. Only admins can see them now."
	}
	formDone(w, r, "/?date="+date, msg)
}

// longDate turns 2026-10-04 into "Sun 04 Oct 26", like the results board.
func longDate(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("Mon 02 Jan 06")
}
