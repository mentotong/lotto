package main

import (
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Agent is a sales point (kiosk, shop, street seller) identified by a
// short code like "AV" printed on tickets. Users work for one agent.
type Agent struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Address   string    `json:"address"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`

	// Legal documents and their verification; see agentdocs.go.
	Owner          string    `json:"owner,omitempty"` // business owner's name
	Docs           []IDDoc   `json:"docs,omitempty"`  // IDDoc.Type is the document kind
	Verify         string    `json:"verify,omitempty"`
	NeedsReview    bool      `json:"needs_review,omitempty"` // documents changed after verification
	RejectReason   string    `json:"reject_reason,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedByName  string    `json:"created_by_name,omitempty"`
	DocsBy         string    `json:"docs_by,omitempty"` // who last changed the documents
	DocsAt         time.Time `json:"docs_at,omitzero"`
	VerifiedBy     string    `json:"verified_by,omitempty"`
	VerifiedByName string    `json:"verified_by_name,omitempty"`
	VerifiedAt     time.Time `json:"verified_at,omitzero"`
}

var agentCodeRe = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)

func NormalizeAgentCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

var (
	ErrAgentExists   = errors.New("That agent code is already used.")
	ErrAgentNotFound = errors.New("Agent not found.")
)

// --- store methods ---

func (s *Store) findAgent(code string) *Agent {
	for _, a := range s.d.Agents {
		if a.Code == code {
			return a
		}
	}
	return nil
}

func (s *Store) Agent(code string) (Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if a := s.findAgent(code); a != nil {
		return *a, true
	}
	return Agent{}, false
}

func (s *Store) Agents() []Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Agent, 0, len(s.d.Agents))
	for _, a := range s.d.Agents {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ActiveAgents is what user forms offer to pick from.
func (s *Store) ActiveAgents() []Agent {
	var out []Agent
	for _, a := range s.Agents() {
		if a.Active {
			out = append(out, a)
		}
	}
	return out
}

func (s *Store) CreateAgent(a Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.findAgent(a.Code) != nil {
		return ErrAgentExists
	}
	if err := s.agentDocTaken(a.Code, a.Docs); err != nil {
		return err
	}
	a.CreatedAt = time.Now()
	s.d.Agents = append(s.d.Agents, &a)
	return s.save()
}

// UpdateAgent changes everything except the code, which is printed on
// tickets already sold and so never changes.
func (s *Store) UpdateAgent(a Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.findAgent(a.Code)
	if cur == nil {
		return ErrAgentNotFound
	}
	cur.Name, cur.Owner, cur.Phone, cur.Address, cur.Active = a.Name, a.Owner, a.Phone, a.Address, a.Active
	return s.save()
}

// canWork reports whether a user's agent allows them to sign in: owners
// and sellers need an existing, active agent; managers have none; an
// admin's agent is optional but, if set, must also be active.
func (s *Store) canWork(u User) bool {
	if u.AgentCode == "" {
		return !u.Role.NeedsAgent()
	}
	a, ok := s.Agent(u.AgentCode)
	// Owners and sellers also need their agent verified (agentdocs.go).
	return ok && a.Active && (a.Verified() || !u.Role.NeedsAgent())
}

// --- handlers (admin only) ---

type AgentForm struct {
	Code    string
	Name    string
	Owner   string
	Phone   string
	Address string
	Active  bool
}

// AgentRow is one line of the agents list.
type AgentRow struct {
	Agent
	Users int
	Sales Cents
}

func agentFormOf(ag Agent) AgentForm {
	return AgentForm{Code: ag.Code, Name: ag.Name, Owner: ag.Owner, Phone: ag.Phone, Address: ag.Address, Active: ag.Active}
}

func readAgentForm(r *http.Request) (AgentForm, []string) {
	f := AgentForm{
		Code:    NormalizeAgentCode(r.FormValue("code")),
		Name:    strings.TrimSpace(r.FormValue("name")),
		Owner:   strings.Join(strings.Fields(r.FormValue("owner")), " "),
		Phone:   strings.TrimSpace(r.FormValue("phone")),
		Address: strings.TrimSpace(r.FormValue("address")),
		Active:  r.FormValue("active") == "on",
	}
	var errs []string
	if !agentCodeRe.MatchString(f.Code) {
		errs = append(errs, "Code must be 1–10 letters or digits, e.g. AV.")
	}
	if f.Name == "" || len(f.Name) > 80 {
		errs = append(errs, "Name is required (up to 80 characters).")
	}
	if f.Owner == "" || len(f.Owner) > 80 {
		errs = append(errs, "The owner's full name is required (up to 80 characters).")
	}
	if len(f.Phone) > 30 || len(f.Address) > 200 {
		errs = append(errs, "Phone or address is too long.")
	}
	return f, errs
}

// AgentQuery is the search, filter and sort on the Agents page.
type AgentQuery struct {
	Search Search
	Status string // "", "active", "disabled", "waiting" or "unverified"
	Sort   string // "" (code), "name" or "sales"
}

func (q AgentQuery) Filtered() bool { return !q.Search.Empty() || q.Status != "" }

// AgentList is one page of the (searched) agents.
type AgentList struct {
	Items  []AgentRow
	Pager  Pager
	Q      AgentQuery
	HasAny bool // there are agents at all (to tell "none yet" from "no match")
}

// agentRows adds user counts and sales to every agent in one pass over
// users and tickets.
func (a *app) agentRows() []AgentRow {
	users, sales := map[string]int{}, map[string]Cents{}
	for _, u := range a.store.Users() {
		users[u.AgentCode]++
	}
	for _, t := range a.store.Tickets(TicketFilter{}) {
		sales[t.AgentCode] += t.Total()
	}
	var rows []AgentRow
	for _, ag := range a.store.Agents() {
		rows = append(rows, AgentRow{Agent: ag, Users: users[ag.Code], Sales: sales[ag.Code]})
	}
	return rows
}

func (a *app) agentList(r *http.Request) AgentList {
	qs := r.URL.Query()
	q := AgentQuery{Search: newSearch(qs.Get("q")), Status: qs.Get("status"), Sort: qs.Get("sort")}
	all := a.agentRows()
	var out []AgentRow
	for _, row := range all {
		switch {
		case q.Status == "active" && !row.Active,
			q.Status == "disabled" && row.Active,
			q.Status == "waiting" && !row.Waiting(),
			q.Status == "unverified" && row.Verified(),
			!q.Search.Match(row.Code, row.Name, row.Owner, row.Phone, row.Address, docNumbers(row.Agent)):
			continue
		}
		out = append(out, row)
	}
	switch q.Sort {
	case "name":
		sort.SliceStable(out, func(i, j int) bool { return fold(out[i].Name) < fold(out[j].Name) })
	case "sales":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Sales > out[j].Sales })
	}
	pg := newPager(r, "/agents", perPageAgents, len(out))
	return AgentList{Items: pageOf(out, pg), Pager: pg, Q: q, HasAny: len(all) > 0}
}

func (a *app) agentsPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, http.StatusOK, "Agents", "agents", AgentsPage(a.agentList(r)))
}

// newAgentPage serves the "Add an agent" form (a dialog via htmx).
func (a *app) newAgentPage(w http.ResponseWriter, r *http.Request) {
	f := AgentForm{Active: true}
	formPage(w, r, http.StatusOK, "Add an agent", "agents", NewAgentDialog(f, nil, nil), NewAgentForm(f, nil, nil))
}

// createAgent adds an agent with its legal documents. It starts out
// waiting for another admin or manager to verify it.
func (a *app) createAgent(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	fail := func(f AgentForm, docs []AgentDocForm, errs []string) {
		formPage(w, r, http.StatusUnprocessableEntity, "Add an agent", "agents", NewAgentDialog(f, docs, errs), NewAgentForm(f, docs, errs))
	}
	if err := parseUpload(w, r, len(agentDocKinds)); err != nil {
		fail(AgentForm{Active: true}, nil, []string{"The upload is too big: each document can be at most 5 MB."})
		return
	}
	f, errs := readAgentForm(r)
	docForms, docErrs := readAgentDocs(r, Agent{}, true)
	errs = append(errs, docErrs...)
	if _, exists := a.store.Agent(f.Code); exists && f.Code != "" {
		errs = append(errs, ErrAgentExists.Error())
	}
	if len(errs) > 0 {
		fail(f, docForms, errs)
		return
	}
	docs, _, _, err := a.store.storeAgentDocs(Agent{}, docForms)
	if err != nil {
		fail(f, docForms, []string{err.Error()})
		return
	}
	now := time.Now()
	ag := Agent{Code: f.Code, Name: f.Name, Owner: f.Owner, Phone: f.Phone, Address: f.Address, Active: true,
		Docs: docs, Verify: VerifyPending, CreatedBy: me.Username, CreatedByName: me.Name, DocsBy: me.Username, DocsAt: now}
	if err := a.store.CreateAgent(ag); err != nil {
		for _, d := range docs {
			a.store.removeDocument(d.File)
		}
		fail(f, docForms, []string{err.Error()})
		return
	}
	a.record(r, "agents", "agent.created", "/agents/"+f.Code, "Added agent %s, %s (owner %s) with %s: waiting for verification", f.Code, f.Name, f.Owner, agentDocSummary(ag))
	formDone(w, r, "/agents/"+f.Code, "Agent "+f.Code+" was added and is waiting for verification by another admin or manager.")
}

func docNumbers(a Agent) string {
	var parts []string
	for _, d := range a.Docs {
		parts = append(parts, d.Number)
	}
	return strings.Join(parts, " ")
}

func (a *app) agentUsers(code string) []User {
	var out []User
	for _, u := range a.store.Users() {
		if u.AgentCode == code {
			out = append(out, u)
		}
	}
	return out
}

func (a *app) editAgentPage(w http.ResponseWriter, r *http.Request) {
	ag, ok := a.store.Agent(r.PathValue("code"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	page(w, r, http.StatusOK, "Agent "+ag.Code, "agents", EditAgentPage(ag, agentFormOf(ag), a.agentUsers(ag.Code), nil, ""))
}

func (a *app) updateAgent(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	before, ok := a.store.Agent(code)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	r.Form.Set("code", code) // the code can't be changed
	if r.FormValue("owner") == "" && before.Owner == "" {
		r.Form.Set("owner", "-") // agents from before owners were recorded
	}
	f, errs := readAgentForm(r)
	me, _ := CurrentUser(r.Context())
	if !f.Active && me.AgentCode == code {
		errs = append(errs, "You can't disable the agent your own account belongs to.")
	}
	if len(errs) == 0 {
		if f.Owner == "-" {
			f.Owner = ""
		}
		if err := a.store.UpdateAgent(Agent{Code: code, Name: f.Name, Owner: f.Owner, Phone: f.Phone, Address: f.Address, Active: f.Active}); err != nil {
			errs = append(errs, err.Error())
		}
	}
	status, msg := http.StatusOK, "Saved."
	if len(errs) > 0 {
		status, msg = http.StatusUnprocessableEntity, ""
	} else if !f.Active {
		msg = "Saved. This agent is disabled: its sellers can't sign in until you enable it again."
	}
	if len(errs) == 0 {
		a.record(r, "agents", "agent.updated", "/agents/"+code, "Edited agent %s: %s", code,
			changes("name", before.Name, f.Name, "owner", before.Owner, f.Owner, "phone", before.Phone, f.Phone, "address", before.Address, f.Address, "status", onOff(before.Active), onOff(f.Active)))
	}
	ag, _ := a.store.Agent(code)
	page(w, r, status, "Agent "+code, "agents", EditAgentPage(ag, f, a.agentUsers(code), errs, msg))
}
