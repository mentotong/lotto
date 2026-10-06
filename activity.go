package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
)

// Activity is one entry in the activity log: who did what, when, and from
// where. Entries are only ever added, never changed or removed.
type Activity struct {
	ID      int       `json:"id"`
	At      time.Time `json:"at"`
	User    string    `json:"user,omitempty"` // username; "" for signed-out visitors
	Name    string    `json:"name,omitempty"` // their display name at the time
	IP      string    `json:"ip,omitempty"`
	Agent   string    `json:"agent,omitempty"` // the user's agent at the time, so owners see their agent's activity
	Kind    string    `json:"kind"`            // area, see activityKinds
	Action  string    `json:"action"`          // e.g. "ticket.sold"
	Summary string    `json:"summary"`         // what happened, in words
	Link    string    `json:"link,omitempty"`  // page about the thing it's about
	Prev    string    `json:"prev"`            // hash of the entry before
	Hash    string    `json:"hash"`            // hash of this entry, including Prev
}

// Areas of the activity log, for the filter and the icons.
var activityKinds = []struct{ Key, Label, Icon string }{
	{"auth", "Sign-in", "logout"},
	{"sales", "Sales", "ticket"},
	{"users", "Users", "user"},
	{"agents", "Agents", "shop"},
	{"prices", "Prices", "tag"},
	{"results", "Results", "trophy"},
	{"setup", "Setup", "gear"},
	{"security", "Security", "shield"},
}

func kindLabel(k string) string {
	for _, x := range activityKinds {
		if x.Key == k {
			return x.Label
		}
	}
	return k
}

func kindIcon(k string) string {
	for _, x := range activityKinds {
		if x.Key == k {
			return x.Icon
		}
	}
	return "check"
}

// Warning is true for entries an admin should notice: failed sign-ins,
// lockouts and refused requests.
func (e Activity) Warning() bool {
	return (e.Kind == "security" && e.Action != "activity.exported") || strings.HasSuffix(e.Action, ".failed") || strings.HasSuffix(e.Action, ".locked")
}

// sum is the entry's hash: SHA-256 over its fields and the previous
// entry's hash. Each entry seals the one before it, so editing or
// deleting a line in the file breaks the chain from that point on.
func (e Activity) sum() string {
	c := e
	c.Hash = ""
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ActivityLog keeps the log in memory and appends each entry as one JSON
// line to its own file next to the data file (lotto.json →
// lotto-activity.jsonl), so the log never needs rewriting.
type ActivityLog struct {
	mu      sync.RWMutex
	path    string
	entries []Activity
	broken  int // ID of the first entry that fails the check; 0 = intact
}

func activityPath(dataPath string) string {
	base := strings.TrimSuffix(filepath.Base(dataPath), filepath.Ext(dataPath))
	return filepath.Join(filepath.Dir(dataPath), base+"-activity.jsonl")
}

func OpenActivityLog(path string) (*ActivityLog, error) {
	l := &ActivityLog{path: path}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	prev, line := "", 0
	for sc.Scan() {
		line++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e Activity
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			// A damaged line: keep going, but the log is no longer intact.
			if l.broken == 0 {
				l.broken = len(l.entries) + 1
			}
			continue
		}
		if l.broken == 0 && (e.Prev != prev || e.sum() != e.Hash || e.ID != len(l.entries)+1) {
			l.broken = e.ID
			if l.broken == 0 {
				l.broken = len(l.entries) + 1
			}
		}
		prev = e.Hash
		l.entries = append(l.entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if l.broken != 0 {
		log.Printf("WARNING: activity log %s fails its check from entry %d: it may have been edited", path, l.broken)
	}
	return l, nil
}

// Add appends an entry and writes it to disk.
func (l *ActivityLog) Add(e Activity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.ID = len(l.entries) + 1
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if n := len(l.entries); n > 0 {
		e.Prev = l.entries[n-1].Hash
	}
	e.Hash = e.sum()
	b, _ := json.Marshal(e)
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		log.Printf("activity log: %v", err) // keep it in memory anyway
	}
	l.entries = append(l.entries, e)
}

// ActivityFilter narrows the log; empty fields match everything.
type ActivityFilter struct {
	Search Search
	Kind   string
	User   string
	Date   string // YYYY-MM-DD, local time
	Agent  string // owners: only their agent's people (not a visible filter)
}

func (f ActivityFilter) Active() bool {
	return !f.Search.Empty() || f.Kind != "" || f.User != "" || f.Date != ""
}

// Find returns matching entries, newest first.
func (l *ActivityLog) Find(f ActivityFilter) []Activity {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Activity
	for i := len(l.entries) - 1; i >= 0; i-- {
		e := l.entries[i]
		if (f.Kind == "" || e.Kind == f.Kind) &&
			(f.User == "" || e.User == f.User) &&
			(f.Agent == "" || e.Agent == f.Agent) &&
			(f.Date == "" || e.At.Format("2006-01-02") == f.Date) &&
			(f.Search.Empty() || f.Search.Match(e.Summary, e.User, e.Name, e.IP, e.Action, kindLabel(e.Kind))) {
			out = append(out, e)
		}
	}
	return out
}

// Check reports how many entries there are and the first one that fails
// the hash-chain check (0 if all pass).
func (l *ActivityLog) Check() (total, broken int) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries), l.broken
}

// Users lists everyone who appears in the log, for the user filter.
func (l *ActivityLog) Users() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, e := range l.entries {
		if e.User != "" && !seen[e.User] {
			seen[e.User] = true
			out = append(out, e.User)
		}
	}
	sort.Strings(out)
	return out
}

// --- Recording ------------------------------------------------------------

// record logs something the signed-in user did.
func (a *app) record(r *http.Request, kind, action, link, format string, args ...any) {
	u, _ := CurrentUser(r.Context())
	a.recordAs(r, u, kind, action, link, format, args...)
}

// recordAs logs an action for a given user (e.g. at sign-in, before the
// user is in the request), or for a signed-out visitor (empty User).
func (a *app) recordAs(r *http.Request, u User, kind, action, link, format string, args ...any) {
	if a.activity == nil {
		return
	}
	a.activity.Add(Activity{
		User: u.Username, Name: u.Name, IP: a.clientIP(r), Agent: u.AgentCode,
		Kind: kind, Action: action, Link: link,
		Summary: fmt.Sprintf(format, args...),
	})
}

// changes describes what an edit changed, e.g. `role seller → admin,
// name "Ana" → "Ana P."`, from pairs of label, before, after.
func changes(pairs ...string) string {
	var out []string
	for i := 0; i+2 < len(pairs); i += 3 {
		label, before, after := pairs[i], pairs[i+1], pairs[i+2]
		if before == after {
			continue
		}
		if before == "" {
			before = "—"
		}
		if after == "" {
			after = "—"
		}
		out = append(out, fmt.Sprintf("%s %s → %s", label, before, after))
	}
	if len(out) == 0 {
		return "no changes"
	}
	return strings.Join(out, "; ")
}

func onOff(b bool) string {
	if b {
		return "active"
	}
	return "disabled"
}

// --- Page -------------------------------------------------------------------

const perPageActivity = 50

func activityFilter(r *http.Request) ActivityFilter {
	q := r.URL.Query()
	f := ActivityFilter{Search: newSearch(q.Get("q")), Kind: q.Get("kind"), User: NormalizeUsername(q.Get("user")), Date: q.Get("date")}
	if kindLabel(f.Kind) == f.Kind {
		f.Kind = "" // unknown area
	}
	if _, err := time.Parse("2006-01-02", f.Date); err != nil {
		f.Date = ""
	}
	if me := userOf(r.Context()); me.Role == RoleOwner {
		f.Agent = me.AgentCode // owners see what their agent's people did
	}
	return f
}

// ActivityView is what the Activity page shows.
type ActivityView struct {
	Filter   ActivityFilter
	Entries  []Activity // this page
	Pager    Pager
	UserName string // display name for the user filter chip
	Total    int    // all entries in the log
	Broken   int
}

func (a *app) activityPage(w http.ResponseWriter, r *http.Request) {
	f := activityFilter(r)
	all := a.activity.Find(f)
	pg := newPager(r, "/activity", perPageActivity, len(all))
	v := ActivityView{Filter: f, Entries: pageOf(all, pg), Pager: pg}
	v.Total, v.Broken = a.activity.Check()
	if f.User != "" {
		v.UserName = f.User
		if u, ok := a.store.User(f.User); ok {
			v.UserName = u.Name + " (" + u.Username + ")"
		}
	}
	page(w, r, http.StatusOK, "Activity", "activity", ActivityPage(v))
}

// activityCSV downloads the matching entries, e.g. for an auditor.
func (a *app) activityCSV(w http.ResponseWriter, r *http.Request) {
	f := activityFilter(r)
	entries := a.activity.Find(f)
	a.record(r, "security", "activity.exported", "/activity", "Downloaded the activity log (%s)", plural(len(entries), "entry"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-activity-%s.csv"`, brand().FileSlug(), time.Now().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "time", "user", "name", "ip", "area", "action", "summary", "hash"})
	for _, e := range entries {
		_ = cw.Write([]string{strconv.Itoa(e.ID), e.At.Format(time.RFC3339), e.User, csvSafe(e.Name), e.IP, kindLabel(e.Kind), e.Action, csvSafe(e.Summary), e.Hash})
	}
	cw.Flush()
}

// csvSafe stops spreadsheet apps from running text that starts like a
// formula (=, +, -, @), since names and summaries come from users.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// activityURL is the Activity page with one filter changed.
func activityURL(f ActivityFilter, key, value string) templ.SafeURL {
	q := map[string]string{"q": f.Search.Raw, "kind": f.Kind, "user": f.User, "date": f.Date}
	q[key] = value
	var parts []string
	for _, k := range []string{"q", "kind", "user", "date"} {
		if q[k] != "" {
			parts = append(parts, k+"="+url.QueryEscape(q[k]))
		}
	}
	if len(parts) == 0 {
		return "/activity"
	}
	return templ.SafeURL("/activity?" + strings.Join(parts, "&"))
}

func agentSuffix(code string) string {
	if code == "" {
		return ""
	}
	return " at agent " + code
}

func buyerSuffix(buyer string) string {
	if buyer == "" {
		return ""
	}
	return ", buyer " + buyer
}

// priceText is "4D $0.50, 3D $0.25, 2D $0.25".
func priceText(p map[Game]Cents) string {
	var parts []string
	for _, g := range Games {
		parts = append(parts, string(g)+" "+p[g].String())
	}
	return strings.Join(parts, ", ")
}

// csvQuery keeps the page's filters for the CSV download.
func csvQuery(f ActivityFilter) string {
	u := string(activityURL(f, "", ""))
	return strings.TrimPrefix(u, "/activity")
}
