package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DigitBoxes = 4 // PIN-style boxes per line
)

// Lines on a fresh sell form, and the most one ticket can have: both set
// on the App setup page.
func startLines() int { return brand().StartLines }
func maxLines() int   { return brand().MaxLines }

func errTooManyLines() error { return fmt.Errorf("a ticket can have at most %d lines", maxLines()) }

// FormRow is one line of the sell form. Key identifies the line in the
// page (ids like game-<key>) and stays fixed when other lines are removed,
// while error messages use the line's position.
type FormRow struct {
	Key    int
	Digits [DigitBoxes]string // one value per box, left to right
	Qty    string
}

var (
	errNotDigit = errors.New("only numbers 0–9 are allowed, one per box")
	errGap      = errors.New("the boxes of a number must be next to each other, without gaps")
	errNotAtEnd = errors.New("the number must end in the last box: leave the first box empty for 3D, the first two for 2D")
	errOneDigit = errors.New("fill at least 2 boxes")
)

// Number reads the boxes like the paper slip: the number always ends in
// the last box, and the empty boxes on the left mark 3D (✕623) or 2D
// (✕✕23). It returns "" for an untouched line, and incomplete=true while
// digits are typed but don't reach the last box yet.
func (r FormRow) Number() (number string, incomplete bool, err error) {
	first, last := -1, -1
	for i, d := range r.Digits {
		if d == "" {
			continue
		}
		if len(d) != 1 || d[0] < '0' || d[0] > '9' {
			return "", false, errNotDigit
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return "", false, nil
	}
	for i := first; i <= last; i++ {
		if r.Digits[i] == "" {
			return "", false, errGap
		}
	}
	if last != DigitBoxes-1 {
		return "", true, errNotAtEnd
	}
	if last == first {
		return "", false, errOneDigit
	}
	return strings.Join(r.Digits[first:], ""), false, nil
}

type SellForm struct {
	Buyer    string
	DrawDate string
	Rows     []FormRow
	Errors   []string
}

// NextKey is the key for a newly added line.
func (f SellForm) NextKey() int {
	next := 0
	for _, r := range f.Rows {
		if r.Key >= next {
			next = r.Key + 1
		}
	}
	return next
}

// LinePreview is the live calculation for one line of the sell form.
type LinePreview struct {
	Key       int
	Game      Game
	Qty       int
	Amount    Cents
	Valid     bool
	Pending   bool // still being typed: digits don't reach the last box yet
	Invalid   bool // something was typed but it isn't a valid bet
	Duplicate bool // same number as an earlier line
}

func newSellForm() SellForm {
	f := SellForm{DrawDate: today()}
	for i := 0; i < startLines(); i++ {
		f.Rows = append(f.Rows, FormRow{Key: i})
	}
	return f
}

// readSellRows reads the lines in page order. Every line posts a key "k",
// four "d" boxes and a "qty", so lines are matched up by position and
// removing a line leaves no gaps.
func readSellRows(r *http.Request) ([]FormRow, error) {
	keys, digits, qtys := r.Form["k"], r.Form["d"], r.Form["qty"]
	n := len(keys)
	var err error
	if n > maxLines() {
		n, err = maxLines(), errTooManyLines()
	}
	get := func(vals []string, i int) string {
		if i < len(vals) {
			return strings.TrimSpace(vals[i])
		}
		return ""
	}
	rows := make([]FormRow, n)
	used := map[int]bool{}
	for i := range rows {
		key, convErr := strconv.Atoi(keys[i])
		if convErr != nil || key < 0 || key > 100_000 || used[key] {
			key = 100_001 + i // keep ids unique and safe even for odd input
		}
		used[key] = true
		rows[i].Key = key
		for j := 0; j < DigitBoxes; j++ {
			rows[i].Digits[j] = get(digits, i*DigitBoxes+j)
		}
		rows[i].Qty = get(qtys, i)
	}
	return rows, err
}

func parseSellForm(r *http.Request) (SellForm, []LinePreview, Ticket) {
	_ = r.ParseForm()
	f := SellForm{
		Buyer:    strings.TrimSpace(r.FormValue("buyer")),
		DrawDate: r.FormValue("draw_date"),
	}
	rows, err := readSellRows(r)
	f.Rows = rows
	if err != nil {
		f.Errors = append(f.Errors, err.Error())
	}

	u, _ := CurrentUser(r.Context())
	t := Ticket{Seller: u.Username, SellerName: u.Name, Buyer: f.Buyer, DrawDate: f.DrawDate}
	previews := make([]LinePreview, len(rows))
	seen := map[string]int{} // number -> line it first appeared on
	for i, row := range rows {
		previews[i].Key = row.Key
		number, incomplete, err := row.Number()
		if incomplete {
			f.Errors = append(f.Errors, fmt.Sprintf("Line %d isn't finished: %v.", i+1, err))
			previews[i].Pending = true
			continue
		}
		if err != nil {
			f.Errors = append(f.Errors, fmt.Sprintf("Line %d: %v.", i+1, err))
			previews[i].Invalid = true
			continue
		}
		if number == "" {
			continue
		}
		game, err := GameForNumber(number)
		if err != nil {
			f.Errors = append(f.Errors, fmt.Sprintf("Line %d: %v.", i+1, err))
			previews[i].Invalid = true
			continue
		}
		if first, dup := seen[number]; dup {
			f.Errors = append(f.Errors, fmt.Sprintf("Line %d: %s is already on line %d. Each number can only appear once per ticket.", i+1, number, first))
			previews[i].Duplicate = true
			continue
		}
		seen[number] = i + 1
		qty := 1
		if row.Qty != "" {
			qty, err = strconv.Atoi(row.Qty)
			if err != nil || qty < 1 || qty > 1000 {
				f.Errors = append(f.Errors, fmt.Sprintf("Line %d: quantity must be a whole number from 1 to 1000.", i+1))
				previews[i].Invalid = true
				continue
			}
		}
		previews[i] = LinePreview{Key: row.Key, Game: game, Qty: qty, Valid: true}
		t.Lines = append(t.Lines, Line{Number: number, Game: game, Qty: qty})
	}
	return f, previews, t
}

func (a *app) sellPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, http.StatusOK, "Sell", "sell", SellPage(a.store.Settings(), newSellForm()))
}

// previewTicket powers the live amounts while typing: it returns
// out-of-band fragments for each line's game tag and amount, the total,
// and the "Add line" button (its counter changes when lines are removed).
func (a *app) previewTicket(w http.ResponseWriter, r *http.Request) {
	f, previews, _ := parseSellForm(r)
	prices := a.store.Settings().Prices
	var total Cents
	for i := range previews {
		if previews[i].Valid {
			previews[i].Amount = prices[previews[i].Game] * Cents(previews[i].Qty)
			total += previews[i].Amount
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := PreviewFragments(previews, total, len(f.Rows)).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// addLine appends one empty line to the sell form (htmx, beforeend into
// the lines table) and refreshes the "Add line" button out-of-band.
func (a *app) addLine(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	rows, _ := readSellRows(r)
	f := SellForm{Rows: rows}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(rows) >= maxLines() {
		_ = addLineButton(len(rows), true).Render(r.Context(), w)
		return
	}
	row := FormRow{Key: f.NextKey()}
	if err := NewLine(row, len(rows)+1).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *app) createTicket(w http.ResponseWriter, r *http.Request) {
	f, _, t := parseSellForm(r)
	if a.store.Settings().Stopped {
		f.Errors = append([]string{ErrSalesStopped.Error()}, f.Errors...)
	}
	if _, err := time.Parse("2006-01-02", f.DrawDate); err != nil {
		f.Errors = append(f.Errors, "Pick a valid draw date.")
	}
	if len(t.Lines) == 0 && len(f.Errors) == 0 {
		f.Errors = append(f.Errors, "Enter at least one number.")
	}
	if len(f.Rows) == 0 {
		f.Rows = newSellForm().Rows
	}
	if len(f.Errors) > 0 {
		page(w, r, http.StatusUnprocessableEntity, "Sell", "sell", SellPage(a.store.Settings(), f))
		return
	}

	// Stamp the seller's agent so reports by agent stay right even if the
	// user later moves to another agent.
	u, _ := CurrentUser(r.Context())
	if ag, ok := a.store.Agent(u.AgentCode); ok {
		t.AgentCode, t.Agent = ag.Code, ag.Name
	}
	saved, err := a.store.CreateTicket(t)
	if errors.Is(err, ErrSalesStopped) { // prices were deactivated a moment ago
		f.Errors = []string{err.Error()}
		page(w, r, http.StatusUnprocessableEntity, "Sell", "sell", SellPage(a.store.Settings(), f))
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	url := fmt.Sprintf("/tickets/%d", saved.ID)
	a.record(r, "sales", "ticket.sold", url, "Sold ticket %s: %s, %s, draw %s%s", saved.Serial, plural(len(saved.Lines), "number"), saved.Total(), saved.DrawDate, buyerSuffix(saved.Buyer))
	if isPartial(r) {
		// Show the ticket right away and put its URL in the address bar.
		w.Header().Set("HX-Push-Url", url)
		page(w, r, http.StatusOK, "Ticket "+saved.Serial, "sell", TicketPage(*saved, true, a.receiptShare(r, *saved)))
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}
