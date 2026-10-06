package main

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A PriceSet is one version of the prices for 4D, 3D and 2D. Changing
// prices adds a new set instead of overwriting the old one, so there is
// a full history. At most one set is active; selling always uses it, and
// when none is active, selling is stopped.
type PriceSet struct {
	ID        int            `json:"id"`
	Prices    map[Game]Cents `json:"prices"`
	Note      string         `json:"note"`
	CreatedAt time.Time      `json:"created_at"`
	CreatedBy string         `json:"created_by"` // name at the time
	Active    bool           `json:"active"`
	Events    []PriceEvent   `json:"events"` // activations and deactivations, oldest first
}

// PriceEvent records who activated or deactivated a price set, and when.
type PriceEvent struct {
	Action string    `json:"action"` // "activated" or "deactivated"
	At     time.Time `json:"at"`
	By     string    `json:"by"`
}

const (
	evActivated   = "activated"
	evDeactivated = "deactivated"
)

// Status is "Active", "Deactivated" or "Not activated".
func (p PriceSet) Status() string {
	switch {
	case p.Active:
		return "Active"
	case len(p.Events) > 0:
		return "Deactivated"
	default:
		return "Not activated"
	}
}

// Since is when the set was last activated (for the active set).
func (p PriceSet) Since() (PriceEvent, bool) {
	for i := len(p.Events) - 1; i >= 0; i-- {
		if p.Events[i].Action == evActivated {
			return p.Events[i], true
		}
	}
	return PriceEvent{}, false
}

var (
	ErrSalesStopped   = errors.New("Selling is stopped: there are no active prices. Ask an admin to activate a price list.")
	ErrPriceSetExists = errors.New("Price list not found.")
)

// --- store ------------------------------------------------------------------

func (s *Store) activeSet() *PriceSet {
	for _, p := range s.d.PriceSets {
		if p.Active {
			return p
		}
	}
	return nil
}

func (s *Store) findPriceSet(id int) *PriceSet {
	for _, p := range s.d.PriceSets {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func copyPriceSet(p *PriceSet) PriceSet {
	c := *p
	c.Prices = map[Game]Cents{}
	for k, v := range p.Prices {
		c.Prices[k] = v
	}
	c.Events = append([]PriceEvent(nil), p.Events...)
	return c
}

// migratePrices turns the single price setting of older versions into
// price list #1, already active.
func (s *Store) migratePrices() bool {
	if len(s.d.PriceSets) > 0 {
		return false
	}
	now := time.Now()
	prices := map[Game]Cents{}
	for _, g := range Games {
		prices[g] = s.d.Settings.Prices[g]
	}
	s.d.PriceSets = []*PriceSet{{
		ID: 1, Prices: prices, Note: "Prices in use before price history was kept",
		CreatedAt: now, CreatedBy: "System", Active: true,
		Events: []PriceEvent{{Action: evActivated, At: now, By: "System"}},
	}}
	return true
}

// PriceSets returns every set, newest first.
func (s *Store) PriceSets() []PriceSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PriceSet, 0, len(s.d.PriceSets))
	for _, p := range s.d.PriceSets {
		out = append(out, copyPriceSet(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func (s *Store) PriceSet(id int) (PriceSet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p := s.findPriceSet(id); p != nil {
		return copyPriceSet(p), true
	}
	return PriceSet{}, false
}

// activate switches the active set; callers hold the write lock.
func (s *Store) activate(p *PriceSet, by string, now time.Time) {
	if cur := s.activeSet(); cur != nil && cur != p {
		cur.Active = false
		cur.Events = append(cur.Events, PriceEvent{Action: evDeactivated, At: now, By: by})
	}
	if !p.Active {
		p.Active = true
		p.Events = append(p.Events, PriceEvent{Action: evActivated, At: now, By: by})
	}
	s.d.Settings.Prices = p.Prices // kept in step for older versions of the file
}

// CreatePriceSet adds a new price list, optionally activating it at once
// (which deactivates the current one).
func (s *Store) CreatePriceSet(prices map[Game]Cents, note, by string, activate bool) (PriceSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := 1
	for _, p := range s.d.PriceSets {
		if p.ID >= next {
			next = p.ID + 1
		}
	}
	now := time.Now()
	p := &PriceSet{ID: next, Prices: prices, Note: note, CreatedAt: now, CreatedBy: by}
	s.d.PriceSets = append(s.d.PriceSets, p)
	if activate {
		s.activate(p, by, now)
	}
	return copyPriceSet(p), s.save()
}

func (s *Store) ActivatePriceSet(id int, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findPriceSet(id)
	if p == nil {
		return ErrPriceSetExists
	}
	s.activate(p, by, time.Now())
	return s.save()
}

// DeactivatePriceSet stops selling until another set is activated.
func (s *Store) DeactivatePriceSet(id int, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findPriceSet(id)
	if p == nil {
		return ErrPriceSetExists
	}
	if p.Active {
		p.Active = false
		p.Events = append(p.Events, PriceEvent{Action: evDeactivated, At: time.Now(), By: by})
	}
	return s.save()
}

// --- handlers (admin only) ----------------------------------------------------

const perPagePrices = 10

// PriceRow is one price list in the history, with how many tickets were
// sold at its prices.
type PriceRow struct {
	PriceSet
	Tickets int
}

type PricesView struct {
	Active *PriceRow
	Rows   []PriceRow
	Pager  Pager
}

func (a *app) pricesView(r *http.Request) PricesView {
	sold := map[int]int{}
	for _, t := range a.store.Tickets(TicketFilter{}) {
		sold[t.PriceSet]++
	}
	var v PricesView
	var rows []PriceRow
	for _, p := range a.store.PriceSets() {
		row := PriceRow{PriceSet: p, Tickets: sold[p.ID]}
		if p.Active {
			act := row
			v.Active = &act
		}
		rows = append(rows, row)
	}
	v.Pager = newPager(r, "/settings", perPagePrices, len(rows))
	v.Rows = pageOf(rows, v.Pager)
	return v
}

func (a *app) pricesPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, http.StatusOK, "Prices", "prices", PricesPage(a.pricesView(r)))
}

// PriceForm is the "New prices" form.
type PriceForm struct {
	Prices   map[Game]string
	Note     string
	Activate bool
}

func (a *app) newPricesPage(w http.ResponseWriter, r *http.Request) {
	f := PriceForm{Prices: map[Game]string{}, Activate: true}
	for g, c := range a.store.Settings().Prices {
		f.Prices[g] = c.Input() // start from the current prices
	}
	formPage(w, r, http.StatusOK, "New prices", "prices", NewPricesDialog(f, nil), NewPricesForm(f, nil))
}

func (a *app) createPrices(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := PriceForm{Prices: map[Game]string{}, Note: strings.TrimSpace(r.FormValue("note")), Activate: r.FormValue("activate") == "on"}
	prices := map[Game]Cents{}
	var errs []string
	for _, g := range Games {
		f.Prices[g] = strings.TrimSpace(r.FormValue("price_" + string(g)))
		c, err := ParseCents(f.Prices[g])
		if err != nil || c <= 0 || c > 1_000_000 {
			errs = append(errs, fmt.Sprintf("%s price must be an amount like 0.25.", g))
			continue
		}
		prices[g] = c
	}
	if len(f.Note) > 200 {
		errs = append(errs, "The note can be at most 200 characters.")
	}
	if len(errs) == 0 && f.Activate {
		cur := a.store.Settings()
		same := !cur.Stopped
		for _, g := range Games {
			same = same && cur.Prices[g] == prices[g]
		}
		if same {
			errs = append(errs, "These are the same as the active prices. Change at least one price.")
		}
	}
	if len(errs) > 0 {
		formPage(w, r, http.StatusUnprocessableEntity, "New prices", "prices", NewPricesDialog(f, errs), NewPricesForm(f, errs))
		return
	}
	me, _ := CurrentUser(r.Context())
	p, err := a.store.CreatePriceSet(prices, f.Note, me.Name, f.Activate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.record(r, "prices", "prices.created", "/settings", "Added price list #%d (%s)%s", p.ID, priceText(prices), map[bool]string{true: " and activated it", false: ", not active"}[f.Activate])
	msg := fmt.Sprintf("Price list #%d saved, not active yet.", p.ID)
	if f.Activate {
		msg = fmt.Sprintf("Price list #%d is now active. New tickets use these prices.", p.ID)
	}
	formDone(w, r, "/settings", msg)
}

// priceAction serves the confirmation for activating or deactivating a
// set (GET) and carries it out (POST).
func (a *app) priceAction(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	action := r.PathValue("action")
	p, ok := a.store.PriceSet(id)
	if !ok || (action != "activate" && action != "deactivate") {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		var current *PriceSet
		for _, s := range a.store.PriceSets() {
			if s.Active {
				s := s
				current = &s
			}
		}
		formPage(w, r, http.StatusOK, "Change prices", "prices",
			PriceConfirmDialog(p, action, current), PriceConfirmForm(p, action, current))
		return
	}
	me, _ := CurrentUser(r.Context())
	var err error
	msg := ""
	if action == "activate" {
		err = a.store.ActivatePriceSet(id, me.Name)
		msg = fmt.Sprintf("Price list #%d is now active. New tickets use these prices.", id)
	} else {
		err = a.store.DeactivatePriceSet(id, me.Name)
		msg = fmt.Sprintf("Price list #%d was deactivated. Selling is stopped until a price list is activated.", id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if action == "activate" {
		a.record(r, "prices", "prices.activated", "/settings", "Activated price list #%d (%s)", id, priceText(p.Prices))
	} else {
		a.record(r, "prices", "prices.deactivated", "/settings", "Deactivated price list #%d: selling stopped", id)
	}
	formDone(w, r, "/settings", msg)
}
