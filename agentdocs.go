package main

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Agents must show they're a legal business before they can operate:
// documents are uploaded when the agent is added, and an admin or manager
// checks them and verifies the agent. Until then (or if rejected) the
// agent's owners and sellers can't sign in. Replacing documents of a
// verified agent flags it for review without stopping its sales.

// The documents asked of an agent.
var agentDocKinds = []struct {
	Key, Label, Help string
	Required         bool // needed before the agent can be verified
	NeedsNumber      bool
}{
	{"registration", "Business registration certificate", "Issued by SERVE (business registration). Enter the registration number.", true, true},
	{"tin", "Tax ID (TIN) certificate", "From the tax authority. Enter the TIN.", true, true},
	{"owner_id", "Owner's ID document", "Passport, Bilhete de Identidade or Kartaun Eleitoral of the business owner.", true, true},
	{"licence", "Lottery or gaming licence", "If the agent holds one.", false, false},
	{"premises", "Proof of premises", "Lease, ownership papers, or a letter from the Chefe de Suco.", false, false},
}

func agentDocLabel(k string) string {
	for _, d := range agentDocKinds {
		if d.Key == k {
			return d.Label
		}
	}
	return k
}

// Verification states. "" is verified (agents from before documents were
// asked for are verified, with documents missing).
const (
	VerifyPending  = "pending"
	VerifyRejected = "rejected"
)

func (a Agent) Verified() bool { return a.Verify == "" }
func (a Agent) Pending() bool  { return a.Verify == VerifyPending }
func (a Agent) Rejected() bool { return a.Verify == VerifyRejected }

// Doc returns the agent's document of a kind.
func (a Agent) Doc(kind string) (IDDoc, bool) {
	for _, d := range a.Docs {
		if d.Type == kind {
			return d, true
		}
	}
	return IDDoc{}, false
}

// MissingDocs lists the required documents the agent doesn't have.
func (a Agent) MissingDocs() []string {
	var out []string
	for _, k := range agentDocKinds {
		if _, ok := a.Doc(k.Key); k.Required && !ok {
			out = append(out, k.Label)
		}
	}
	return out
}

// ExpiredDocs lists documents whose expiry date has passed.
func (a Agent) ExpiredDocs() []string {
	var out []string
	for _, d := range a.Docs {
		if d.Expired() {
			out = append(out, agentDocLabel(d.Type))
		}
	}
	return out
}

// Waiting is true when an admin or manager has something to check.
func (a Agent) Waiting() bool { return a.Pending() || a.NeedsReview }

// MayVerify reports whether u may verify or reject agent a: admins and
// managers, but a manager not for an agent they added or whose documents
// they last changed (someone else checks their work).
func (u User) MayVerify(a Agent) bool {
	if !u.Can(PermAgents) || !a.Waiting() {
		return false
	}
	if u.Role == RoleAdmin {
		return true
	}
	return u.Username != a.CreatedBy && u.Username != a.DocsBy
}

// --- Form -------------------------------------------------------------------

// AgentDocForm is one document as typed in a form.
type AgentDocForm struct {
	Kind, Number, Expiry string
	Upload               *multipart.FileHeader
	OnFile               IDDoc // what's stored now, if anything
}

// readAgentDocs reads the documents part of an agent form. isNew requires
// every required document; on an edit, only documents being replaced are
// checked.
func readAgentDocs(r *http.Request, cur Agent, isNew bool) ([]AgentDocForm, []string) {
	var docs []AgentDocForm
	var errs []string
	today := time.Now().Format("2006-01-02")
	for _, k := range agentDocKinds {
		f := AgentDocForm{
			Kind:   k.Key,
			Number: strings.Join(strings.Fields(r.FormValue("doc_"+k.Key+"_number")), " "),
			Expiry: strings.TrimSpace(r.FormValue("doc_" + k.Key + "_expiry")),
		}
		f.OnFile, _ = cur.Doc(k.Key)
		if r.MultipartForm != nil {
			if fhs := r.MultipartForm.File["doc_"+k.Key+"_file"]; len(fhs) > 0 && fhs[0].Size > 0 {
				f.Upload = fhs[0]
			}
		}
		docs = append(docs, f)
		touched := f.Upload != nil || f.Number != "" || f.Expiry != ""
		if !isNew && !touched {
			continue
		}
		if !isNew && f.OnFile.File != "" && f.Upload == nil && f.Number == f.OnFile.Number && f.Expiry == f.OnFile.Expiry {
			continue // unchanged
		}
		if !k.Required && !touched {
			continue
		}
		if k.NeedsNumber && !idNumberRe.MatchString(f.Number) {
			errs = append(errs, k.Label+": enter its number (3–30 letters or digits).")
		} else if f.Number != "" && !idNumberRe.MatchString(f.Number) {
			errs = append(errs, k.Label+": the number can only have letters, digits, spaces, dots, dashes and slashes.")
		}
		if f.Expiry != "" {
			if _, err := time.Parse("2006-01-02", f.Expiry); err != nil {
				errs = append(errs, k.Label+": the expiry date isn't a valid date.")
			} else if f.Expiry < today {
				errs = append(errs, k.Label+" expired on "+f.Expiry+". Ask for a valid document.")
			}
		}
		if f.Upload == nil && f.OnFile.File == "" {
			errs = append(errs, k.Label+": upload a photo or scan (JPEG, PNG or PDF, up to 5 MB).")
		}
	}
	return docs, errs
}

// changed reports whether the form changes this document.
func (f AgentDocForm) changed() bool {
	return f.Upload != nil || (f.OnFile.File != "" && (f.Number != f.OnFile.Number || f.Expiry != f.OnFile.Expiry)) ||
		(f.OnFile.File == "" && (f.Number != "" || f.Expiry != ""))
}

// storeAgentDocs saves the uploads and returns the documents to keep,
// the files they replace (to delete once saved) and what changed.
func (s *Store) storeAgentDocs(cur Agent, forms []AgentDocForm) (docs []IDDoc, replaced []string, changed []string, err error) {
	var saved []string
	for _, f := range forms {
		d := f.OnFile
		if !f.changed() {
			if d.File != "" {
				docs = append(docs, d)
			}
			continue
		}
		if f.Upload != nil {
			nd, err := s.saveDocument(f.Upload)
			if err != nil {
				for _, n := range saved {
					s.removeDocument(n)
				}
				return nil, nil, nil, fmt.Errorf("%s: %w", agentDocLabel(f.Kind), err)
			}
			saved = append(saved, nd.File)
			if d.File != "" {
				replaced = append(replaced, d.File)
			}
			d.File, d.FileType, d.FileName, d.Uploaded = nd.File, nd.FileType, nd.FileName, nd.Uploaded
		}
		d.Type, d.Number, d.Expiry = f.Kind, f.Number, f.Expiry
		if d.File == "" {
			continue
		}
		docs = append(docs, d)
		changed = append(changed, agentDocLabel(f.Kind))
	}
	return docs, replaced, changed, nil
}

// agentDocTaken refuses a registration or tax number already used by
// another agent. Callers hold the lock.
func (s *Store) agentDocTaken(code string, docs []IDDoc) error {
	for _, d := range docs {
		if d.Number == "" || (d.Type != "registration" && d.Type != "tin") {
			continue
		}
		for _, o := range s.d.Agents {
			if o.Code == code {
				continue
			}
			if od, ok := o.Doc(d.Type); ok && normID(od.Number) == normID(d.Number) {
				return fmt.Errorf("%s %s is already registered to agent %s.", agentDocLabel(d.Type), d.Number, o.Code)
			}
		}
	}
	return nil
}

// --- Store ------------------------------------------------------------------

// SetAgentDocs replaces an agent's documents. A rejected agent goes back
// to waiting; a verified one is flagged for review.
func (s *Store) SetAgentDocs(code string, docs []IDDoc, by User) (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.findAgent(code)
	if a == nil {
		return Agent{}, ErrAgentNotFound
	}
	if err := s.agentDocTaken(code, docs); err != nil {
		return Agent{}, err
	}
	old := *a
	a.Docs, a.DocsBy, a.DocsAt = docs, by.Username, time.Now()
	switch {
	case a.Rejected():
		a.Verify, a.RejectReason = VerifyPending, ""
	case a.Verified():
		a.NeedsReview = true
	}
	if err := s.save(); err != nil {
		*a = old
		return Agent{}, err
	}
	return *a, nil
}

var errCantVerify = errors.New("You can't decide on this agent: it's not waiting, or you added it or changed its documents yourself.")

// VerifyAgent marks an agent verified (or its changed documents reviewed).
func (s *Store) VerifyAgent(code string, by User) (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.findAgent(code)
	if a == nil {
		return Agent{}, ErrAgentNotFound
	}
	if !by.MayVerify(*a) {
		return Agent{}, errCantVerify
	}
	if m := a.MissingDocs(); len(m) > 0 {
		return Agent{}, errors.New("Missing documents: " + strings.Join(m, ", ") + ". Upload them first.")
	}
	old := *a
	a.Verify, a.NeedsReview, a.RejectReason = "", false, ""
	a.VerifiedBy, a.VerifiedByName, a.VerifiedAt = by.Username, by.Name, time.Now()
	if err := s.save(); err != nil {
		*a = old
		return Agent{}, err
	}
	return *a, nil
}

// RejectAgent turns an agent down: its people can't sign in until new
// documents are uploaded and someone verifies it.
func (s *Store) RejectAgent(code string, by User, reason string) (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.findAgent(code)
	if a == nil {
		return Agent{}, ErrAgentNotFound
	}
	if !by.MayVerify(*a) {
		return Agent{}, errCantVerify
	}
	old := *a
	a.Verify, a.NeedsReview, a.RejectReason = VerifyRejected, false, reason
	a.VerifiedBy, a.VerifiedByName, a.VerifiedAt = by.Username, by.Name, time.Now()
	if err := s.save(); err != nil {
		*a = old
		return Agent{}, err
	}
	return *a, nil
}

// AgentsToVerify lists agents waiting for u to check, oldest first.
func (s *Store) AgentsToVerify(u User) []Agent {
	var out []Agent
	for _, a := range s.Agents() {
		if u.MayVerify(a) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DocsAt.Before(out[j].DocsAt) })
	return out
}

// --- Handlers ---------------------------------------------------------------

// agentDocument serves one of an agent's documents to admins and
// managers, and logs that it was opened.
func (a *app) agentDocument(w http.ResponseWriter, r *http.Request) {
	ag, ok := a.store.Agent(r.PathValue("code"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	d, ok := ag.Doc(r.PathValue("kind"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := a.store.readDocument(d.File)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.record(r, "agents", "document.viewed", "/agents/"+ag.Code, "Viewed the %s of agent %s", strings.ToLower(agentDocLabel(d.Type)), ag.Code)
	w.Header().Set("Content-Type", d.FileType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(d.FileName, `"`, "")))
	if d.FileType != "application/pdf" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	w.Write(data)
}

// updateAgentDocs replaces documents from the agent page.
func (a *app) updateAgentDocs(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	cur, ok := a.store.Agent(r.PathValue("code"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	show := func(status int, errs []string, msg string) {
		ag, _ := a.store.Agent(cur.Code)
		page(w, r, status, "Agent "+ag.Code, "agents", EditAgentPage(ag, agentFormOf(ag), a.agentUsers(ag.Code), errs, msg))
	}
	if err := parseUpload(w, r, len(agentDocKinds)); err != nil {
		show(http.StatusUnprocessableEntity, []string{"The upload is too big: each document can be at most 5 MB."}, "")
		return
	}
	forms, errs := readAgentDocs(r, cur, false)
	if len(errs) > 0 {
		show(http.StatusUnprocessableEntity, errs, "")
		return
	}
	docs, replaced, changed, err := a.store.storeAgentDocs(cur, forms)
	if err != nil {
		show(http.StatusUnprocessableEntity, []string{err.Error()}, "")
		return
	}
	if len(changed) == 0 {
		show(http.StatusOK, nil, "Nothing changed.")
		return
	}
	ag, err := a.store.SetAgentDocs(cur.Code, docs, me)
	if err != nil {
		for _, d := range docs {
			if _, had := cur.Doc(d.Type); !had || d.File != docFile(cur, d.Type) {
				a.store.removeDocument(d.File)
			}
		}
		show(http.StatusUnprocessableEntity, []string{err.Error()}, "")
		return
	}
	for _, f := range replaced {
		a.store.removeDocument(f)
	}
	a.record(r, "agents", "agent.documents", "/agents/"+cur.Code, "Updated documents of agent %s: %s", cur.Code, strings.Join(changed, ", "))
	msg := "Documents saved."
	switch {
	case ag.Pending() && cur.Rejected():
		msg = "Documents saved. The agent is waiting for verification again."
	case ag.NeedsReview:
		msg = "Documents saved. An admin or manager will review the change; the agent keeps working meanwhile."
	}
	show(http.StatusOK, nil, msg)
}

func docFile(a Agent, kind string) string {
	d, _ := a.Doc(kind)
	return d.File
}

// verifyAgent confirms (GET) and records (POST) verifying or rejecting.
func (a *app) verifyAgent(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	ag, ok := a.store.Agent(r.PathValue("code"))
	action := r.PathValue("action")
	if !ok || (action != "verify" && action != "reject") {
		http.NotFound(w, r)
		return
	}
	if !me.MayVerify(ag) {
		page(w, r, http.StatusForbidden, "Not allowed", "", Forbidden())
		return
	}
	ret := returnPath(r, "/agents/"+ag.Code)
	if r.Method == http.MethodGet {
		formPage(w, r, http.StatusOK, "Verify agent", "agents", VerifyAgentDialog(ag, action, "", nil, ret), VerifyAgentForm(ag, action, "", nil, ret))
		return
	}
	_ = r.ParseForm()
	reason := strings.Join(strings.Fields(r.FormValue("reason")), " ")
	fail := func(errs []string) {
		formPage(w, r, http.StatusUnprocessableEntity, "Verify agent", "agents", VerifyAgentDialog(ag, action, reason, errs, ret), VerifyAgentForm(ag, action, reason, errs, ret))
	}
	link := "/agents/" + ag.Code
	if action == "reject" {
		if reason == "" || len(reason) > 200 {
			fail([]string{"Say why (up to 200 characters), so the documents can be fixed."})
			return
		}
		if _, err := a.store.RejectAgent(ag.Code, me, reason); err != nil {
			fail([]string{err.Error()})
			return
		}
		a.record(r, "agents", "agent.rejected", link, "Rejected agent %s, %s: %s", ag.Code, ag.Name, reason)
		formDone(w, r, ret, "Agent "+ag.Code+" was rejected. Its people can't sign in until new documents are verified.")
		return
	}
	if _, err := a.store.VerifyAgent(ag.Code, me); err != nil {
		fail([]string{err.Error()})
		return
	}
	if ag.NeedsReview {
		a.record(r, "agents", "agent.reviewed", link, "Reviewed the changed documents of agent %s, %s", ag.Code, ag.Name)
		formDone(w, r, ret, "The new documents of agent "+ag.Code+" are reviewed.")
		return
	}
	a.record(r, "agents", "agent.verified", link, "Verified agent %s, %s (%s)", ag.Code, ag.Name, agentDocSummary(ag))
	formDone(w, r, ret, "Agent "+ag.Code+" is verified. Its owners and sellers can sign in now.")
}

// agentDocSummary is "registration 123, TIN 456" for the log.
func agentDocSummary(a Agent) string {
	var parts []string
	for _, d := range a.Docs {
		s := agentDocLabel(d.Type)
		if d.Number != "" {
			s += " " + d.Number
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "no documents"
	}
	return strings.Join(parts, "; ")
}

// parseUpload parses a multipart form with room for n documents.
func parseUpload(w http.ResponseWriter, r *http.Request, n int) error {
	limit := int64(n)*maxDocBytes + 256<<10
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	err := r.ParseMultipartForm(32 << 20)
	if errors.Is(err, http.ErrNotMultipart) {
		return r.ParseForm()
	}
	return err
}
