package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// IDDoc is a user's national ID document: which one, its number, when it
// expires, and a scan or photo of it.
type IDDoc struct {
	Type     string `json:"type"`                // see idTypes
	Number   string `json:"number"`              //
	Expiry   string `json:"expiry,omitempty"`    // YYYY-MM-DD, optional
	File     string `json:"file,omitempty"`      // stored file name in the documents folder
	FileType string `json:"file_type,omitempty"` // image/jpeg, image/png or application/pdf
	FileName string `json:"file_name,omitempty"` // name it was uploaded with
	Uploaded string `json:"uploaded,omitempty"`  // YYYY-MM-DD
}

func (d IDDoc) Empty() bool { return d.Type == "" && d.Number == "" && d.File == "" }

// The ID documents accepted in Timor-Leste.
var idTypes = []struct{ Key, Label string }{
	{"passport", "Passport"},
	{"bi", "Bilhete de Identidade"},
	{"eleitoral", "Kartaun Eleitoral"},
}

func idTypeLabel(k string) string {
	for _, t := range idTypes {
		if t.Key == k {
			return t.Label
		}
	}
	return k
}

// Expired reports whether the document's expiry date has passed.
func (d IDDoc) Expired() bool {
	return d.Expiry != "" && d.Expiry < time.Now().Format("2006-01-02")
}

// Summary is "Passport 12345678, expires 2030-01-31".
func (d IDDoc) Summary() string {
	if d.Empty() {
		return "none"
	}
	s := idTypeLabel(d.Type) + " " + d.Number
	if d.Expiry != "" {
		s += ", expires " + d.Expiry
	}
	return s
}

const maxDocBytes = 5 << 20 // 5 MB

var idNumberRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ./-]{2,29}$`)

// normID makes ID numbers comparable: "12 345-678" == "12345678".
func normID(n string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(n) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IDForm is the ID part of the user form as typed.
type IDForm struct {
	Type, Number, Expiry string
	HasFile              bool   // a document is already on file
	FileName             string // its name, to show
}

func idForm(d IDDoc) IDForm {
	return IDForm{Type: d.Type, Number: d.Number, Expiry: d.Expiry, HasFile: d.File != "", FileName: d.FileName}
}

func readIDForm(r *http.Request) IDForm {
	return IDForm{
		Type:   r.FormValue("id_type"),
		Number: strings.Join(strings.Fields(r.FormValue("id_number")), " "),
		Expiry: strings.TrimSpace(r.FormValue("id_expiry")),
	}
}

// check validates the ID fields. fileRequired is true when there's no
// document on file yet and none was uploaded.
func (f IDForm) check(fileRequired bool) []string {
	var errs []string
	if idTypeLabel(f.Type) == f.Type {
		errs = append(errs, "Pick the ID document: Passport, Bilhete de Identidade or Kartaun Eleitoral.")
		return errs
	}
	if !idNumberRe.MatchString(f.Number) {
		errs = append(errs, "Enter the "+idTypeLabel(f.Type)+" number (3–30 letters or digits).")
	}
	if f.Expiry != "" {
		if _, err := time.Parse("2006-01-02", f.Expiry); err != nil {
			errs = append(errs, "The expiry date isn't a valid date.")
		} else if f.Expiry < time.Now().Format("2006-01-02") {
			errs = append(errs, "This "+idTypeLabel(f.Type)+" expired on "+f.Expiry+". Ask for a valid document.")
		}
	}
	if fileRequired {
		errs = append(errs, "Upload a photo or scan of the "+idTypeLabel(f.Type)+" (JPEG, PNG or PDF, up to 5 MB).")
	}
	return errs
}

// idTaken refuses an ID number already used by someone else (or in a
// change waiting for approval), so one person can't hold two accounts.
// Callers hold the lock.
func (s *Store) idTaken(d IDDoc, username string) error {
	if d.Number == "" {
		return nil
	}
	n := normID(d.Number)
	for _, u := range s.d.Users {
		if u.Username == username {
			continue
		}
		same := func(o IDDoc) bool { return o.Type == d.Type && normID(o.Number) == n }
		if same(u.ID) || (u.Change != nil && same(u.Change.ID)) {
			return fmt.Errorf("%s %s is already registered to another user (%s).", idTypeLabel(d.Type), d.Number, u.Username)
		}
	}
	return nil
}

// --- Files ------------------------------------------------------------------

// Documents live in a folder next to the data file (lotto.json →
// lotto-documents/), under random names, never inside the web folders.
func (s *Store) docsDir() string {
	base := strings.TrimSuffix(filepath.Base(s.path), filepath.Ext(s.path))
	return filepath.Join(filepath.Dir(s.path), base+"-documents")
}

var errDocType = errors.New("The ID document must be a JPEG or PNG photo, or a PDF, of at most 5 MB.")

// saveDocument checks an upload and stores it. It returns the stored
// file's details, or a zero IDDoc if nothing was uploaded.
func (s *Store) saveDocument(fh *multipart.FileHeader) (IDDoc, error) {
	if fh == nil {
		return IDDoc{}, nil
	}
	if fh.Size > maxDocBytes {
		return IDDoc{}, errDocType
	}
	f, err := fh.Open()
	if err != nil {
		return IDDoc{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDocBytes+1))
	if err != nil {
		return IDDoc{}, err
	}
	if len(data) == 0 {
		return IDDoc{}, nil
	}
	if len(data) > maxDocBytes {
		return IDDoc{}, errDocType
	}
	// Go by the content, not the name or the browser's word for it.
	var typ, ext string
	switch ct := http.DetectContentType(data); {
	case ct == "image/jpeg":
		typ, ext = ct, ".jpg"
	case ct == "image/png":
		typ, ext = ct, ".png"
	case ct == "application/pdf":
		typ, ext = ct, ".pdf"
	default:
		return IDDoc{}, errDocType
	}
	if err := os.MkdirAll(s.docsDir(), 0o700); err != nil {
		return IDDoc{}, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return IDDoc{}, err
	}
	name := hex.EncodeToString(b) + ext
	if err := os.WriteFile(filepath.Join(s.docsDir(), name), data, 0o600); err != nil {
		return IDDoc{}, err
	}
	return IDDoc{File: name, FileType: typ, FileName: cleanFileName(fh.Filename), Uploaded: time.Now().Format("2006-01-02")}, nil
}

func cleanFileName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	n = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '/' {
			return -1
		}
		return r
	}, n)
	for utf8.RuneCountInString(n) > 80 {
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	if n == "" || n == "." {
		return "document"
	}
	return n
}

// removeDocument deletes a stored file (ignoring ones already gone).
func (s *Store) removeDocument(name string) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return
	}
	_ = os.Remove(filepath.Join(s.docsDir(), name))
}

func (s *Store) readDocument(name string) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(s.docsDir(), name))
}

// formDocument reads the "id_file" upload from a user form, if any.
func formDocument(r *http.Request) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	if fhs := r.MultipartForm.File["id_file"]; len(fhs) > 0 && fhs[0].Size > 0 {
		return fhs[0]
	}
	return nil
}

// parseUserUpload parses a multipart user form, capped at the document
// size plus room for the fields.
func parseUserUpload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes+256<<10)
	err := r.ParseMultipartForm(maxDocBytes + 256<<10)
	if errors.Is(err, http.ErrNotMultipart) {
		return r.ParseForm()
	}
	return err
}

// --- Handler ----------------------------------------------------------------

// document serves a user's ID document (or, with ?change=1, the one in a
// change waiting for approval) to people allowed to see it. Each view is
// logged, since these are personal documents.
func (a *app) document(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	t, ok := a.store.User(r.PathValue("username"))
	if !ok || !me.SeesDocument(t) {
		http.NotFound(w, r)
		return
	}
	d, which := t.ID, "ID document"
	if r.URL.Query().Get("change") == "1" {
		if t.Change == nil {
			http.NotFound(w, r)
			return
		}
		d, which = t.Change.ID, "proposed ID document"
	}
	data, err := a.store.readDocument(d.File)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if me.Username != t.Username {
		a.record(r, "users", "document.viewed", "/users/"+t.Username, "Viewed the %s of %s (%s): %s", which, t.Name, t.Username, idTypeLabel(d.Type))
	}
	w.Header().Set("Content-Type", d.FileType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(d.FileName, `"`, "")))
	if d.FileType != "application/pdf" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	w.Write(data)
}
