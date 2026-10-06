package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Game is the bet type, decided by how many digits the buyer picks.
type Game string

const (
	Game4D Game = "4D"
	Game3D Game = "3D"
	Game2D Game = "2D"
)

var Games = []Game{Game4D, Game3D, Game2D}

func GameForNumber(n string) (Game, error) {
	for _, r := range n {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("%q: only digits are allowed", n)
		}
	}
	switch len(n) {
	case 4:
		return Game4D, nil
	case 3:
		return Game3D, nil
	case 2:
		return Game2D, nil
	}
	return "", fmt.Errorf("%s has %d digit(s); fill 2, 3 or 4 boxes", n, len(n))
}

// Cents keeps money as integer cents to avoid float rounding.
type Cents int64

// String formats the amount with the app's currency, e.g. "$1.50".
func (c Cents) String() string { return brand().Money(c) }

// Input renders the value for an <input> field, e.g. "0.25".
func (c Cents) Input() string {
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}

func ParseCents(s string) (Cents, error) {
	s = strings.TrimSpace(s)
	if cur := brand().Currency; cur != "" {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, cur), cur))
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "$"))
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, errors.New("empty amount")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("%q: at most 2 decimals", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("%q is not a valid amount", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("%q is not a valid amount", s)
	}
	return Cents(w*100 + f), nil
}

// Settings is what selling needs to know: the active prices, or that
// selling is stopped because no price list is active. Prices is also
// stored in the file (kept in step with the active price list).
type Settings struct {
	Prices  map[Game]Cents `json:"prices"`
	SetID   int            `json:"-"` // active price list
	Stopped bool           `json:"-"` // no active price list
}

type Line struct {
	Number string `json:"number"`
	Game   Game   `json:"game"`
	Qty    int    `json:"qty"`
	Price  Cents  `json:"price"` // unit price at time of sale
}

func (l Line) Amount() Cents { return l.Price * Cents(l.Qty) }

type Ticket struct {
	ID         int       `json:"id"`
	Serial     string    `json:"serial"`
	Code       string    `json:"code"`
	PriceSet   int       `json:"price_set,omitempty"` // price list the ticket was sold under        // random code for the public check page (QR)
	Seller     string    `json:"seller"`              // username of the user who sold it
	SellerName string    `json:"seller_name"`         // their display name at sale time
	AgentCode  string    `json:"agent_code"`          // agent (sales point) at sale time
	Agent      string    `json:"agent"`               // agent name at sale time
	Buyer      string    `json:"buyer"`
	DrawDate   string    `json:"draw_date"` // YYYY-MM-DD
	SoldAt     time.Time `json:"sold_at"`
	Lines      []Line    `json:"lines"`
}

func (t Ticket) Total() Cents {
	var sum Cents
	for _, l := range t.Lines {
		sum += l.Amount()
	}
	return sum
}

type data struct {
	NextID    int           `json:"next_id"`
	Branding  Branding      `json:"branding"`
	Settings  Settings      `json:"settings"`
	PriceSets []*PriceSet   `json:"price_sets"`
	Results   []*DrawResult `json:"results"`
	Tickets   []*Ticket     `json:"tickets"`
	Users     []*User       `json:"users"`
	Agents    []*Agent      `json:"agents"`
}

// Store is an in-memory store persisted to a JSON file after each write.
type Store struct {
	mu   sync.RWMutex
	path string
	d    data
	logo []byte // uploaded logo, see branding.go
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, d: data{
		NextID: 1,
		Settings: Settings{Prices: map[Game]Cents{
			Game4D: 50, Game3D: 25, Game2D: 25,
		}},
	}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.migrate()
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if s.migrate() {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// migrate upgrades data written by older versions. It reports whether
// anything changed.
func (s *Store) migrate() bool {
	changed := s.migratePrices()
	changed = s.loadBranding() || changed
	for _, u := range s.d.Users {
		if u.Role == "agent" { // the "agent" role was renamed to "seller"
			u.Role = RoleSeller
			changed = true
		}
	}
	for _, t := range s.d.Tickets {
		// Before agents existed, Ticket.Agent held the seller's name.
		if t.Seller != "" && t.SellerName == "" && t.AgentCode == "" {
			t.SellerName, t.Agent = t.Agent, ""
			changed = true
		}
		// Tickets from before QR sharing get a public check code.
		if t.Code == "" {
			t.Code = s.newTicketCode()
			changed = true
		}
	}
	return changed
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Settings returns the active prices, or Stopped when no price list is
// active.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.activeSet()
	if p == nil {
		return Settings{Prices: map[Game]Cents{}, Stopped: true}
	}
	c := copyPriceSet(p)
	return Settings{Prices: c.Prices, SetID: c.ID}
}

func (s *Store) CreateTicket(t Ticket) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.activeSet()
	if active == nil {
		return nil, ErrSalesStopped
	}
	for i := range t.Lines {
		t.Lines[i].Price = active.Prices[t.Lines[i].Game]
	}
	t.PriceSet = active.ID
	t.ID = s.d.NextID
	t.Serial = fmt.Sprintf("%05d", t.ID)
	t.SoldAt = time.Now()
	t.Code = s.newTicketCode()
	s.d.NextID++
	s.d.Tickets = append(s.d.Tickets, &t)
	if err := s.save(); err != nil {
		return nil, err
	}
	return &t, nil
}

// newTicketCode makes a random, unguessable code (60 bits) for the public
// check page, so a buyer's link can't be used to find other tickets.
// Callers hold the write lock.
func (s *Store) newTicketCode() string {
	for {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		code := strings.ToLower(base32.StdEncoding.EncodeToString(b))[:12]
		taken := false
		for _, t := range s.d.Tickets {
			taken = taken || t.Code == code
		}
		if !taken {
			return code
		}
	}
}

// TicketByCode finds a ticket by its public check code.
func (s *Store) TicketByCode(code string) (Ticket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(code) != 12 {
		return Ticket{}, false
	}
	for _, t := range s.d.Tickets {
		if subtle.ConstantTimeCompare([]byte(t.Code), []byte(code)) == 1 {
			return *t, true
		}
	}
	return Ticket{}, false
}

func (s *Store) Ticket(id int) (Ticket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.d.Tickets {
		if t.ID == id {
			return *t, true
		}
	}
	return Ticket{}, false
}

// TicketFilter narrows Tickets; empty fields match everything.
type TicketFilter struct {
	DrawDate  string
	Seller    string
	AgentCode string
	Search    Search // serial, buyer, seller, agent or a number on the ticket
}

func (t Ticket) matches(q Search) bool {
	if q.Empty() {
		return true
	}
	fields := []string{t.Serial, t.Buyer, t.SellerName, t.Seller, t.AgentCode, t.Agent}
	for _, l := range t.Lines {
		fields = append(fields, l.Number)
	}
	return q.Match(fields...)
}

// Tickets returns matching tickets, newest first.
func (s *Store) Tickets(f TicketFilter) []Ticket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Ticket
	for _, t := range s.d.Tickets {
		if (f.DrawDate == "" || t.DrawDate == f.DrawDate) &&
			(f.Seller == "" || t.Seller == f.Seller) &&
			(f.AgentCode == "" || t.AgentCode == f.AgentCode) &&
			t.matches(f.Search) {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}
