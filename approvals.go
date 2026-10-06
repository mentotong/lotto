package main

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// UserChange is a seller's change to another seller at their agent,
// waiting for a manager or admin to approve it.
type UserChange struct {
	Name   string    `json:"name"`
	Active bool      `json:"active"`
	ID     IDDoc     `json:"id_doc"`
	By     string    `json:"by"`
	ByName string    `json:"by_name"`
	At     time.Time `json:"at"`
}

// Describe lists what the change would change, e.g. `name "A" → "B"`.
func (c UserChange) Describe(u User) string {
	var docBefore, docAfter string
	if c.ID.Type != u.ID.Type || normID(c.ID.Number) != normID(u.ID.Number) || c.ID.Expiry != u.ID.Expiry {
		docBefore, docAfter = u.ID.Summary(), c.ID.Summary()
	}
	s := changes("name", u.Name, c.Name, "status", onOff(u.Active), onOff(c.Active), "ID", docBefore, docAfter)
	if c.ID.File != "" && c.ID.File != u.ID.File {
		s = strings.TrimPrefix(s+"; new document photo", "no changes; ")
	}
	return s
}

var (
	ErrNothingPending = errors.New("There's nothing waiting for approval for this user.")
)

// RequestChange stores a seller's change for approval, replacing any
// earlier one. It returns the earlier change's new file, if any, for the
// caller to delete.
func (s *Store) RequestChange(username string, c UserChange) (oldFile string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUser(username)
	if u == nil {
		return "", ErrUserNotFound
	}
	if err := s.idTaken(c.ID, username); err != nil {
		return "", err
	}
	if c.ID.File == "" { // same document photo
		c.ID.File, c.ID.FileType, c.ID.FileName, c.ID.Uploaded = u.ID.File, u.ID.FileType, u.ID.FileName, u.ID.Uploaded
	}
	if u.Change != nil && u.Change.ID.File != u.ID.File {
		oldFile = u.Change.ID.File
	}
	old := u.Change
	u.Change = &c
	if err := s.save(); err != nil {
		u.Change = old
		return "", err
	}
	return oldFile, nil
}

// Decision is what Approve and Reject did, for the activity log.
type Decision struct {
	User     User
	Kind     string // "user" (a new user) or "change"
	Describe string
}

// Approve approves a new user or applies a pending change. It returns the
// document file the change replaced, for the caller to delete.
func (s *Store) Approve(username string, by User) (Decision, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUser(username)
	if u == nil {
		return Decision{}, "", ErrUserNotFound
	}
	now := time.Now()
	switch {
	case u.Pending():
		if !by.Approves(*u, u.RequestedBy) {
			return Decision{}, "", errNotYours
		}
		old := *u
		u.Approval, u.DecidedBy, u.DecidedAt = "", by.Username, now
		if err := s.save(); err != nil {
			*u = old
			return Decision{}, "", err
		}
		return Decision{User: *u, Kind: "user"}, "", nil
	case u.Change != nil:
		if !by.Approves(*u, u.Change.By) {
			return Decision{}, "", errNotYours
		}
		old := *u
		c := *u.Change
		d := Decision{Kind: "change", Describe: c.Describe(*u)}
		var oldFile string
		if c.ID.File != old.ID.File {
			oldFile = old.ID.File
		}
		u.LastChange = &ChangeResult{By: c.By, Describe: d.Describe, Approved: true, At: now}
		u.Name, u.Active, u.ID, u.Change = c.Name, c.Active, c.ID, nil
		u.DecidedBy, u.DecidedAt = by.Username, now
		if err := s.save(); err != nil {
			*u = old
			return Decision{}, "", err
		}
		d.User = *u
		return d, oldFile, nil
	}
	return Decision{}, "", ErrNothingPending
}

var errNotYours = errors.New("You can't approve this: it's for another agent, or you asked for it yourself.")

// Reject turns down a new user (who then can't sign in) or drops a
// pending change. It returns a document file nobody needs any more.
func (s *Store) Reject(username string, by User, reason string) (Decision, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUser(username)
	if u == nil {
		return Decision{}, "", ErrUserNotFound
	}
	now := time.Now()
	switch {
	case u.Pending():
		if !by.Approves(*u, u.RequestedBy) {
			return Decision{}, "", errNotYours
		}
		old := *u
		u.Approval, u.DecidedBy, u.DecidedAt, u.RejectReason = ApprovalRejected, by.Username, now, reason
		if err := s.save(); err != nil {
			*u = old
			return Decision{}, "", err
		}
		return Decision{User: *u, Kind: "user"}, "", nil
	case u.Change != nil:
		if !by.Approves(*u, u.Change.By) {
			return Decision{}, "", errNotYours
		}
		old := *u
		d := Decision{User: *u, Kind: "change", Describe: u.Change.Describe(*u)}
		var dropFile string
		if u.Change.ID.File != u.ID.File {
			dropFile = u.Change.ID.File
		}
		u.LastChange = &ChangeResult{By: u.Change.By, Describe: d.Describe, Reason: reason, At: now}
		u.Change = nil
		if err := s.save(); err != nil {
			*u = old
			return Decision{}, "", err
		}
		return d, dropFile, nil
	}
	return Decision{}, "", ErrNothingPending
}

// WaitingFor lists users with a new account or a change that u may
// approve, oldest request first.
func (s *Store) WaitingFor(u User) []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []User
	for _, t := range s.d.Users {
		if (t.Pending() && u.Approves(*t, t.RequestedBy)) || (t.Change != nil && u.Approves(*t, t.Change.By)) {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return requestedAt(out[i]).Before(requestedAt(out[j])) })
	return out
}

// RequestedBy lists the requests u made that are still waiting, plus the
// ones decided in the last 30 days.
func (s *Store) RequestsBy(u User) []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	since := time.Now().AddDate(0, 0, -30)
	var out []User
	for _, t := range s.d.Users {
		mine := (t.RequestedBy == u.Username && (t.Pending() || t.DecidedAt.After(since))) ||
			(t.Change != nil && t.Change.By == u.Username) ||
			(t.LastChange != nil && t.LastChange.By == u.Username && t.LastChange.At.After(since))
		if mine {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return requestedAt(out[i]).After(requestedAt(out[j])) })
	return out
}

func requestedAt(u User) time.Time {
	if u.Change != nil {
		return u.Change.At
	}
	if u.LastChange != nil && u.LastChange.At.After(u.RequestedAt) {
		return u.LastChange.At
	}
	return u.RequestedAt
}

// --- Handlers -------------------------------------------------------------

func (a *app) approvalsPage(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	var v ApprovalsView
	if me.Can(PermApprove) {
		v.Waiting = a.store.WaitingFor(me)
	}
	v.Mine = a.store.RequestsBy(me)
	if me.Can(PermAgents) {
		v.Agents = a.store.AgentsToVerify(me)
	}
	page(w, r, http.StatusOK, approvalsTitle(me), "approvals", ApprovalsPage(v))
}

// ApprovalsView is the Approvals page: what the user may approve, and
// what they asked for.
type ApprovalsView struct {
	Waiting []User
	Mine    []User
	Agents  []Agent // agents waiting for this admin or manager to verify
}

func approvalsTitle(u User) string {
	if u.Can(PermApprove) {
		return "Approvals"
	}
	return "My requests"
}

// decide approves (GET shows the confirmation, POST does it) or rejects
// a new user or a change.
func (a *app) decide(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	t, ok := a.store.User(r.PathValue("username"))
	action := r.PathValue("action")
	if !ok || (action != "approve" && action != "reject") {
		http.NotFound(w, r)
		return
	}
	requester := t.RequestedBy
	if !t.Pending() && t.Change != nil {
		requester = t.Change.By
	}
	if (!t.Pending() && t.Change == nil) || !me.Approves(t, requester) {
		page(w, r, http.StatusForbidden, "Not allowed", "", Forbidden())
		return
	}
	ret := returnPath(r, "/approvals")
	if r.Method == http.MethodGet {
		formPage(w, r, http.StatusOK, "Approve", "approvals", DecideDialog(t, action, "", nil, ret), DecideForm(t, action, "", nil, ret))
		return
	}
	_ = r.ParseForm()
	reason := strings.Join(strings.Fields(r.FormValue("reason")), " ")
	if action == "reject" && (reason == "" || len(reason) > 200) {
		errs := []string{"Say why (up to 200 characters), so the person who asked knows what to fix."}
		formPage(w, r, http.StatusUnprocessableEntity, "Reject", "approvals", DecideDialog(t, action, reason, errs, ret), DecideForm(t, action, reason, errs, ret))
		return
	}
	var (
		d    Decision
		file string
		err  error
	)
	if action == "approve" {
		d, file, err = a.store.Approve(t.Username, me)
	} else {
		d, file, err = a.store.Reject(t.Username, me, reason)
	}
	if err != nil {
		formPage(w, r, http.StatusUnprocessableEntity, "Approve", "approvals", DecideDialog(t, action, reason, []string{err.Error()}, ret), DecideForm(t, action, reason, []string{err.Error()}, ret))
		return
	}
	a.store.removeDocument(file)
	link := "/users/" + t.Username
	var msg string
	switch {
	case action == "approve" && d.Kind == "user":
		a.record(r, "users", "user.approved", link, "Approved new %s %s (%s)%s, asked for by %s", strings.ToLower(t.Role.Label()), t.Name, t.Username, agentSuffix(t.AgentCode), t.RequestedByName)
		msg = t.Name + " is approved and can sign in now."
	case action == "approve":
		a.record(r, "users", "change.approved", link, "Approved changes to %s (%s), asked for by %s: %s", t.Name, t.Username, t.Change.ByName, d.Describe)
		msg = "Changes to " + d.User.Name + " are approved and saved."
	case d.Kind == "user":
		a.record(r, "users", "user.rejected", link, "Rejected new %s %s (%s), asked for by %s: %s", strings.ToLower(t.Role.Label()), t.Name, t.Username, t.RequestedByName, reason)
		msg = t.Name + " was rejected and can't sign in."
	default:
		a.record(r, "users", "change.rejected", link, "Rejected changes to %s (%s), asked for by %s: %s. Reason: %s", t.Name, t.Username, t.Change.ByName, d.Describe, reason)
		msg = "The changes to " + t.Name + " were rejected."
	}
	formDone(w, r, ret, msg)
}

// pendingCount is the number on the Approvals tab.
func (a *app) pendingCount(u User) int {
	n := 0
	if u.Can(PermApprove) {
		n += len(a.store.WaitingFor(u))
	}
	if u.Can(PermAgents) {
		n += len(a.store.AgentsToVerify(u))
	}
	return n
}
