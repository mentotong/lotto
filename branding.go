package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // logo formats
	_ "image/jpeg" //
	_ "image/png"  //
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Branding is the app's own setup, changed by admins on the App setup
// page: its name and logo, colours, money format, contact details, the
// text printed on receipts, and a few limits.
type Branding struct {
	Name          string `json:"name"`
	Tagline       string `json:"tagline"`        // under the name on receipts and results
	Color         string `json:"color"`          // main colour: header, buttons
	Accent        string `json:"accent"`         // "red pen": numbers and totals
	Currency      string `json:"currency"`       // "$", "US$", "Rp"…
	CurrencyAfter bool   `json:"currency_after"` // "10.00 Rp" instead of "$10.00"
	Phone         string `json:"phone"`
	Address       string `json:"address"`
	ReceiptNote   string `json:"receipt_note"` // bottom of every receipt
	ResultsNote   string `json:"results_note"` // under the results board
	StartLines    int    `json:"start_lines"`  // lines on a fresh sell form
	MaxLines      int    `json:"max_lines"`    // most lines one ticket can have
	SessionHours  int    `json:"session_hours"`

	LogoType  string    `json:"logo_type,omitempty"` // image/png…; "" = built-in icon
	LogoHash  string    `json:"logo_hash,omitempty"` // changes the logo's address when it changes
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// Limits for the setup form.
const (
	maxLogoBytes   = 1 << 20 // 1 MB
	maxLogoPixels  = 4000    // per side
	maxLinesLimit  = 100
	maxSessionHrs  = 72
	brandNameMax   = 40
	brandTaglineMx = 40
)

func defaultBranding() Branding {
	return Branding{
		Name:         "Lotaria",
		Tagline:      "4D / 3D / 2D",
		Color:        "#2340A0",
		Accent:       "#D0263A",
		Currency:     "$",
		ReceiptNote:  "Keep this receipt. It's needed to claim a prize.",
		StartLines:   3,
		MaxLines:     50,
		SessionHours: 12,
	}
}

// fill gives missing fields (data from older versions) their defaults.
func (b *Branding) fill() bool {
	d, changed := defaultBranding(), false
	set := func(dst *string, v string) {
		if strings.TrimSpace(*dst) == "" {
			*dst, changed = v, true
		}
	}
	set(&b.Name, d.Name)
	set(&b.Color, d.Color)
	set(&b.Accent, d.Accent)
	set(&b.Currency, d.Currency)
	if b.StartLines == 0 && b.MaxLines == 0 && b.SessionHours == 0 {
		// Never saved before: also take the default texts.
		b.Tagline, b.ReceiptNote = d.Tagline, d.ReceiptNote
		changed = true
	}
	for _, n := range []struct {
		p *int
		v int
	}{{&b.StartLines, d.StartLines}, {&b.MaxLines, d.MaxLines}, {&b.SessionHours, d.SessionHours}} {
		if *n.p <= 0 {
			*n.p, changed = n.v, true
		}
	}
	return changed
}

// The current branding is read on every page (header, prices, receipts),
// so it's kept in an atomic pointer that the store replaces on save.
var current atomic.Pointer[Branding]

func init() { b := defaultBranding(); current.Store(&b) }

// brand returns the current branding.
func brand() Branding { return *current.Load() }

func (b Branding) HasLogo() bool { return b.LogoType != "" }

// LogoURL changes whenever the logo does, so browsers can cache it for long.
func (b Branding) LogoURL() string { return "/brand/logo?v=" + b.LogoHash }

// ColorDark and AccentDark are the hover shades.
func (b Branding) ColorDark() string  { return shade(b.Color, 0.25) }
func (b Branding) AccentDark() string { return shade(b.Accent, 0.16) }

// Contact is the phone and address on one line, for receipts.
func (b Branding) Contact() string {
	var parts []string
	for _, s := range []string{b.Phone, b.Address} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// FileSlug is the name in saved image files, e.g. "lotaria-amizade".
func (b Branding) FileSlug() string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(fold(b.Name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			dash = false
		} else if !dash && sb.Len() > 0 {
			sb.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(sb.String(), "-")
	if s == "" {
		return "lottery"
	}
	return s
}

// Money formats cents with the configured currency.
func (b Branding) Money(c Cents) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	n := fmt.Sprintf("%d.%02d", c/100, c%100)
	if b.CurrencyAfter {
		return sign + n + " " + b.Currency
	}
	return sign + b.Currency + n
}

// themeCSS overrides the colour tokens of the Tailwind theme.
func (b Branding) themeCSS() string {
	return fmt.Sprintf(":root{--color-form:%s;--color-form-dark:%s;--color-pen:%s;--color-pen-dark:%s}",
		b.Color, b.ColorDark(), b.Accent, b.AccentDark())
}

// --- Colours ----------------------------------------------------------------

func parseHex(s string) (r, g, b float64, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255), true
}

func normHex(s string) (string, bool) {
	r, g, b, ok := parseHex(s)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("#%02X%02X%02X", int(r), int(g), int(b)), true
}

// shade mixes a colour with black.
func shade(hex string, amount float64) string {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return hex
	}
	k := 1 - amount
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round(r*k)), int(math.Round(g*k)), int(math.Round(b*k)))
}

// contrastWithWhite is the WCAG contrast ratio of white text on the colour.
func contrastWithWhite(hex string) float64 {
	r, g, b, _ := parseHex(hex)
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	l := 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	return 1.05 / (l + 0.05)
}

// --- Store ------------------------------------------------------------------

// The logo is kept in its own file next to the data file (lotto.json →
// lotto-logo), so the data file stays small.
func (s *Store) logoPath() string {
	base := strings.TrimSuffix(filepath.Base(s.path), filepath.Ext(s.path))
	return filepath.Join(filepath.Dir(s.path), base+"-logo")
}

func (s *Store) loadBranding() bool {
	changed := s.d.Branding.fill()
	if s.d.Branding.HasLogo() {
		b, err := os.ReadFile(s.logoPath())
		if err != nil {
			s.d.Branding.LogoType, s.d.Branding.LogoHash = "", "" // logo file lost: back to the icon
			changed = true
		} else {
			s.logo = b
		}
	}
	b := s.d.Branding
	current.Store(&b)
	return changed
}

// Logo returns the uploaded logo, if there is one.
func (s *Store) Logo() ([]byte, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.logo, s.d.Branding.LogoType, s.d.Branding.HasLogo()
}

// LogoChange says what to do with the logo when saving the setup.
type LogoChange struct {
	Data   []byte // new logo (already checked), or nil
	Type   string
	Remove bool
}

func (s *Store) SaveBranding(b Branding, logo LogoChange, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.d.Branding
	b.LogoType, b.LogoHash = old.LogoType, old.LogoHash
	switch {
	case logo.Data != nil:
		if err := writeFileAtomic(s.logoPath(), logo.Data); err != nil {
			return err
		}
		sum := sha256.Sum256(logo.Data)
		b.LogoType, b.LogoHash = logo.Type, hex.EncodeToString(sum[:6])
		s.logo = logo.Data
	case logo.Remove:
		b.LogoType, b.LogoHash = "", ""
		s.logo = nil
		_ = os.Remove(s.logoPath())
	}
	b.UpdatedAt, b.UpdatedBy = time.Now(), by
	s.d.Branding = b
	if err := s.save(); err != nil {
		s.d.Branding = old
		return err
	}
	cp := b
	current.Store(&cp)
	return nil
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- Form ---------------------------------------------------------------------

// BrandForm is the App setup form as typed.
type BrandForm struct {
	Name, Tagline, Color, Accent, Currency, CurrencyPos string
	Phone, Address, ReceiptNote, ResultsNote            string
	StartLines, MaxLines, SessionHours                  string
	RemoveLogo                                          bool
}

func brandForm(b Branding) BrandForm {
	pos := "before"
	if b.CurrencyAfter {
		pos = "after"
	}
	return BrandForm{
		Name: b.Name, Tagline: b.Tagline, Color: b.Color, Accent: b.Accent,
		Currency: b.Currency, CurrencyPos: pos, Phone: b.Phone, Address: b.Address,
		ReceiptNote: b.ReceiptNote, ResultsNote: b.ResultsNote,
		StartLines: strconv.Itoa(b.StartLines), MaxLines: strconv.Itoa(b.MaxLines),
		SessionHours: strconv.Itoa(b.SessionHours),
	}
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

// parse checks the form and returns the branding it describes, or a
// message per problem.
func (f BrandForm) parse() (Branding, []string) {
	var b Branding
	var errs []string
	text := func(label, v string, max int, required bool) string {
		v = clean(v)
		switch {
		case required && v == "":
			errs = append(errs, label+" can't be empty.")
		case utf8.RuneCountInString(v) > max:
			errs = append(errs, fmt.Sprintf("%s can be at most %d characters.", label, max))
		}
		return v
	}
	b.Name = text("The app name", f.Name, brandNameMax, true)
	b.Tagline = text("The tagline", f.Tagline, brandTaglineMx, false)
	b.Currency = text("The currency symbol", f.Currency, 5, true)
	b.CurrencyAfter = f.CurrencyPos == "after"
	b.Phone = text("The phone number", f.Phone, 40, false)
	b.Address = text("The address", f.Address, 120, false)
	b.ReceiptNote = text("The receipt note", f.ReceiptNote, 120, false)
	b.ResultsNote = text("The results note", f.ResultsNote, 160, false)
	if strings.ContainsAny(b.Currency, "0123456789.,-") {
		errs = append(errs, "The currency symbol can't contain digits, dots, commas or dashes.")
	}

	for _, c := range []struct {
		label string
		in    string
		out   *string
	}{{"Main colour", f.Color, &b.Color}, {"Accent colour", f.Accent, &b.Accent}} {
		hex, ok := normHex(c.in)
		if !ok {
			errs = append(errs, c.label+" must be a colour like #2340A0.")
			continue
		}
		*c.out = hex
		if cr := contrastWithWhite(hex); cr < 4.5 {
			errs = append(errs, fmt.Sprintf("%s %s is too light: white text on it would be hard to read (contrast %.1f:1, needs 4.5:1). Pick a darker colour.", c.label, hex, cr))
		}
	}

	num := func(label, v string, min, max int) int {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < min || n > max {
			errs = append(errs, fmt.Sprintf("%s must be a whole number from %d to %d.", label, min, max))
		}
		return n
	}
	b.StartLines = num("Lines on a new ticket", f.StartLines, 1, 20)
	b.MaxLines = num("Most lines per ticket", f.MaxLines, 1, maxLinesLimit)
	b.SessionHours = num("Sign-in length", f.SessionHours, 1, maxSessionHrs)
	if b.StartLines > 0 && b.MaxLines > 0 && b.StartLines > b.MaxLines {
		errs = append(errs, "Lines on a new ticket can't be more than the most lines per ticket.")
	}
	return b, errs
}

var errLogo = errors.New("the logo must be a PNG, JPEG or GIF image of at most 1 MB")

// readLogo checks an uploaded logo: a real PNG, JPEG or GIF (SVG isn't
// accepted, since it can carry scripts), not too big.
func readLogo(r io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxLogoBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", nil
	}
	if len(data) > maxLogoBytes {
		return nil, "", errLogo
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", errLogo
	}
	if cfg.Width > maxLogoPixels || cfg.Height > maxLogoPixels || cfg.Width < 16 || cfg.Height < 16 {
		return nil, "", fmt.Errorf("the logo must be between 16 and %d pixels on each side", maxLogoPixels)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", errLogo // header fine, image broken
	}
	return data, "image/" + format, nil
}

// --- Handlers -------------------------------------------------------------------

func (a *app) logo(w http.ResponseWriter, r *http.Request) {
	data, typ, ok := a.store.Logo()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	if r.URL.Query().Get("v") == brand().LogoHash {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Write(data)
}

func (a *app) brandingPage(w http.ResponseWriter, r *http.Request) {
	b := brand()
	page(w, r, http.StatusOK, "App setup", "setup", BrandingPage(brandForm(b), b, nil, r.URL.Query().Get("saved") == "1"))
}

func (a *app) saveBranding(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+64<<10)
	if err := r.ParseMultipartForm(maxLogoBytes + 64<<10); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		b := brand()
		page(w, r, http.StatusUnprocessableEntity, "App setup", "setup", BrandingPage(brandForm(b), b, []string{"The upload is too big. The logo can be at most 1 MB."}, false))
		return
	}
	f := BrandForm{
		Name: r.FormValue("name"), Tagline: r.FormValue("tagline"),
		Color: r.FormValue("color"), Accent: r.FormValue("accent"),
		Currency: r.FormValue("currency"), CurrencyPos: r.FormValue("currency_pos"),
		Phone: r.FormValue("phone"), Address: r.FormValue("address"),
		ReceiptNote: r.FormValue("receipt_note"), ResultsNote: r.FormValue("results_note"),
		StartLines: r.FormValue("start_lines"), MaxLines: r.FormValue("max_lines"),
		SessionHours: r.FormValue("session_hours"), RemoveLogo: r.FormValue("remove_logo") == "1",
	}
	b, errs := f.parse()
	var change LogoChange
	if file, _, err := r.FormFile("logo"); err == nil {
		data, typ, err := readLogo(file)
		file.Close()
		if err != nil {
			errs = append(errs, strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
		} else if data != nil {
			change = LogoChange{Data: data, Type: typ}
		}
	}
	if change.Data == nil && f.RemoveLogo {
		change.Remove = true
	}
	if len(errs) > 0 {
		page(w, r, http.StatusUnprocessableEntity, "App setup", "setup", BrandingPage(f, brand(), errs, false))
		return
	}
	u := userOf(r.Context())
	old := brand()
	if err := a.store.SaveBranding(b, change, u.Username); err != nil {
		page(w, r, http.StatusInternalServerError, "App setup", "setup", BrandingPage(f, brand(), []string{"Couldn't save: " + err.Error()}, false))
		return
	}
	what := changes("name", old.Name, b.Name, "tagline", old.Tagline, b.Tagline, "main colour", old.Color, b.Color,
		"accent colour", old.Accent, b.Accent, "currency", old.Money(100), b.Money(100), "phone", old.Phone, b.Phone,
		"address", old.Address, b.Address, "receipt note", old.ReceiptNote, b.ReceiptNote, "results note", old.ResultsNote, b.ResultsNote,
		"start lines", strconv.Itoa(old.StartLines), strconv.Itoa(b.StartLines), "max lines", strconv.Itoa(old.MaxLines), strconv.Itoa(b.MaxLines),
		"sign-in hours", strconv.Itoa(old.SessionHours), strconv.Itoa(b.SessionHours))
	switch {
	case change.Data != nil:
		what = strings.TrimPrefix(what+"; new logo", "no changes; ")
	case change.Remove:
		what = strings.TrimPrefix(what+"; logo removed", "no changes; ")
	}
	a.record(r, "setup", "setup.changed", "/setup/app", "Changed the app setup: %s", what)
	// Name, colours and logo are in the page head too, so load the whole
	// page again rather than just the content.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/setup/app?saved=1")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/setup/app?saved=1", http.StatusSeeOther)
}
