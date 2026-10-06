package main

import (
	"net/http"
	"strings"
	"time"
)

// --- handlers: users ----------------------------------------------------------
//
// Who sees and changes which users is decided in roles.go: admins and
// managers work with everyone (managers not with admins), owners with
// their agent's sellers, and sellers ask for new sellers or changes at
// their own agent, which wait for approval (approvals.go).

type UserForm struct {
	Username  string
	Name      string
	Role      Role
	AgentCode string
	Active    bool
	ID        IDForm
}

type userFormOpts struct {
	me           User  // who is filling it in
	withPassword bool  // a new user: password and confirm
	cur          *User // the user being edited, nil when adding
	noID         bool  // first-run setup: no ID document asked
	upload       bool  // a document was uploaded with the form
}

// readUserForm validates the user fields for what o.me may do.
func (a *app) readUserForm(r *http.Request, o userFormOpts) (UserForm, []string) {
	f := UserForm{
		Username:  NormalizeUsername(r.FormValue("username")),
		Name:      strings.Join(strings.Fields(r.FormValue("name")), " "),
		Role:      Role(r.FormValue("role")),
		AgentCode: NormalizeAgentCode(r.FormValue("agent_code")),
		Active:    r.FormValue("active") == "on",
		ID:        readIDForm(r),
	}
	if o.cur != nil {
		f.Username = o.cur.Username
		f.ID.HasFile, f.ID.FileName = o.cur.ID.File != "", o.cur.ID.FileName
	}
	me := o.me
	// Owners only work with sellers at their own agent.
	if !me.PicksAgent() {
		f.AgentCode = me.AgentCode
	}
	if me.Role == RoleOwner {
		f.Role = RoleSeller
	}
	if f.Role == RoleManager {
		f.AgentCode = "" // managers work for all agents
	}

	var errs []string
	if err := ValidateUsername(f.Username); err != nil && o.cur == nil {
		errs = append(errs, err.Error())
	}
	if f.Name == "" || len(f.Name) > 60 {
		errs = append(errs, "Name is required (up to 60 characters).")
	}
	if !f.Role.Valid() {
		errs = append(errs, "Pick a role.")
	} else if !me.mayAssign(f.Role) && (o.cur == nil || o.cur.Role != f.Role) {
		errs = append(errs, "You can't give the "+f.Role.Label()+" role.")
	}
	currentAgent := ""
	if o.cur != nil {
		currentAgent = o.cur.AgentCode
	}
	if f.AgentCode == "" {
		if f.Role.NeedsAgent() {
			errs = append(errs, f.Role.Label()+"s must belong to an agent.")
		}
	} else if ag, ok := a.store.Agent(f.AgentCode); !ok {
		errs = append(errs, "Pick an existing agent.")
	} else if !ag.Active && f.AgentCode != currentAgent {
		errs = append(errs, "Agent "+ag.Code+" is disabled; pick an active one.")
	}
	if o.withPassword {
		pw := r.FormValue("password")
		if err := ValidatePassword(pw); err != nil {
			errs = append(errs, err.Error())
		} else if pw != r.FormValue("confirm") {
			errs = append(errs, "Passwords don't match.")
		}
	}
	if !o.noID {
		// New users always need an ID document. Older users added before
		// documents were asked for can be saved without one, until someone
		// starts filling it in.
		touched := f.ID.Type != "" || f.ID.Number != "" || f.ID.Expiry != "" || o.upload
		if o.cur == nil || touched || o.cur.ID.Type != "" {
			errs = append(errs, f.ID.check(!f.ID.HasFile && !o.upload)...)
		}
	}
	return f, errs
}

// UserQuery is the search and filters on the Users page.
type UserQuery struct {
	Search Search
	Role   string // "" or a role
	Agent  string // agent code, "-" for users without an agent, "" for all
	Status string // "", "active", "disabled", "pending" or "rejected"
}

func (q UserQuery) Filtered() bool {
	return !q.Search.Empty() || q.Role != "" || q.Agent != "" || q.Status != ""
}

// UserList is one page of the (searched) users.
type UserList struct {
	Items []User
	Pager Pager
	Q     UserQuery
}

func (a *app) userList(r *http.Request) UserList {
	me := userOf(r.Context())
	qs := r.URL.Query()
	q := UserQuery{
		Search: newSearch(qs.Get("q")),
		Role:   qs.Get("role"),
		Agent:  NormalizeAgentCode(qs.Get("agent")),
		Status: qs.Get("status"),
	}
	if !me.PicksAgent() {
		q.Agent = "" // owners only see their own agent
	}
	agentNames := map[string]string{}
	for _, ag := range a.store.Agents() {
		agentNames[ag.Code] = ag.Name
	}
	var out []User
	for _, u := range a.store.Users() {
		switch {
		case !me.SeesUser(u),
			q.Role != "" && string(u.Role) != q.Role,
			q.Agent == "-" && u.AgentCode != "",
			q.Agent != "" && q.Agent != "-" && u.AgentCode != q.Agent,
			q.Status == "active" && !u.CanSignIn(),
			q.Status == "disabled" && (u.Active || u.Approval != ""),
			q.Status == "pending" && !u.Pending() && u.Change == nil,
			q.Status == "rejected" && !u.Rejected(),
			!q.Search.Match(u.Name, u.Username, u.AgentCode, agentNames[u.AgentCode], u.ID.Number):
			continue
		}
		out = append(out, u)
	}
	pg := newPager(r, "/users", perPageUsers, len(out))
	return UserList{Items: pageOf(out, pg), Pager: pg, Q: q}
}

func (a *app) usersPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, http.StatusOK, "Users", "users", UsersPage(a.userList(r), a.store.Agents()))
}

// newUserForm prefills "Add user"; ?agent=AV comes from an agent's page.
func (a *app) newUserForm(r *http.Request) UserForm {
	me := userOf(r.Context())
	f := UserForm{Role: RoleSeller, Active: true, AgentCode: NormalizeAgentCode(r.URL.Query().Get("agent"))}
	if !me.PicksAgent() {
		f.AgentCode = me.AgentCode
	}
	return f
}

func addUserTitle(me User) string {
	if me.NeedsApproval() {
		return "Add a seller"
	}
	return "Add a user"
}

// newUserPage serves the "Add a user" form (a dialog via htmx).
func (a *app) newUserPage(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	f := a.newUserForm(r)
	ret := returnPath(r, "/users")
	formPage(w, r, http.StatusOK, addUserTitle(me), "users",
		NewUserDialog(f, a.store.Agents(), nil, ret), NewUserForm(f, a.store.Agents(), nil, ret))
}

func (a *app) createUser(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	ret := "/users" // read from the form once it's parsed (below)
	fail := func(f UserForm, errs []string) {
		formPage(w, r, http.StatusUnprocessableEntity, addUserTitle(me), "users",
			NewUserDialog(f, a.store.Agents(), errs, ret), NewUserForm(f, a.store.Agents(), errs, ret))
	}
	if err := parseUserUpload(w, r); err != nil {
		fail(a.newUserForm(r), []string{errDocType.Error()})
		return
	}
	ret = returnPath(r, "/users")
	up := formDocument(r)
	f, errs := a.readUserForm(r, userFormOpts{me: me, withPassword: true, upload: up != nil})
	f.Active = true
	if len(errs) > 0 {
		fail(f, errs)
		return
	}
	doc, err := a.store.saveDocument(up)
	if err != nil {
		fail(f, []string{err.Error()})
		return
	}
	doc.Type, doc.Number, doc.Expiry = f.ID.Type, f.ID.Number, f.ID.Expiry
	u := User{Username: f.Username, Name: f.Name, Role: f.Role, AgentCode: f.AgentCode, Active: true, ID: doc,
		RequestedBy: me.Username, RequestedByName: me.Name, RequestedAt: time.Now()}
	if me.NeedsApproval() {
		u.Approval = ApprovalPending
	}
	if err := a.store.CreateUser(u, r.FormValue("password"), false); err != nil {
		a.store.removeDocument(doc.File)
		fail(f, []string{err.Error()})
		return
	}
	link := "/users/" + f.Username
	if u.Pending() {
		a.record(r, "users", "user.requested", link, "Asked to add seller %s (%s)%s with %s: waiting for approval", f.Name, f.Username, agentSuffix(f.AgentCode), doc.Summary())
		formDone(w, r, ret, f.Name+" was sent for approval. They can sign in once a manager or admin approves.")
		return
	}
	a.record(r, "users", "user.created", link, "Added %s %s (%s)%s with %s", strings.ToLower(f.Role.Label()), f.Name, f.Username, agentSuffix(f.AgentCode), doc.Summary())
	formDone(w, r, ret, f.Name+" ("+f.Username+") was added.")
}

// visibleUser loads the user in the path if the signed-in user may see them.
func (a *app) visibleUser(w http.ResponseWriter, r *http.Request) (User, bool) {
	t, ok := a.store.User(r.PathValue("username"))
	if !ok || !userOf(r.Context()).SeesUser(t) {
		http.NotFound(w, r)
		return User{}, false
	}
	return t, true
}

func formOf(u User) UserForm {
	return UserForm{Username: u.Username, Name: u.Name, Role: u.Role, AgentCode: u.AgentCode, Active: u.Active, ID: idForm(u.ID)}
}

func (a *app) editUserPage(w http.ResponseWriter, r *http.Request) {
	t, ok := a.visibleUser(w, r)
	if !ok {
		return
	}
	page(w, r, http.StatusOK, t.Name, "users", EditUserPage(t, formOf(t), a.store.Agents(), nil, ""))
}

func (a *app) updateUser(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	cur, ok := a.visibleUser(w, r)
	if !ok {
		return
	}
	if !me.Manages(cur) || (me.NeedsApproval() && (cur.Pending() || cur.Rejected())) {
		page(w, r, http.StatusForbidden, "Not allowed", "", Forbidden())
		return
	}
	show := func(status int, f UserForm, errs []string, msg string) {
		t, _ := a.store.User(cur.Username)
		page(w, r, status, t.Name, "users", EditUserPage(t, f, a.store.Agents(), errs, msg))
	}
	if err := parseUserUpload(w, r); err != nil {
		show(http.StatusUnprocessableEntity, formOf(cur), []string{errDocType.Error()}, "")
		return
	}
	up := formDocument(r)
	if me.NeedsApproval() {
		// Sellers can't change roles or agents; keep them.
		r.Form.Set("role", string(cur.Role))
		r.Form.Set("agent_code", cur.AgentCode)
	}
	f, errs := a.readUserForm(r, userFormOpts{me: me, cur: &cur, upload: up != nil})
	if cur.Username == me.Username && (!f.Active || f.Role != RoleAdmin) {
		errs = append(errs, "You can't disable your own account or remove your own admin role.")
	}
	if cur.Username == me.Username && f.AgentCode != "" && f.AgentCode != cur.AgentCode {
		if ag, ok := a.store.Agent(f.AgentCode); ok && !ag.Active {
			errs = append(errs, "You can't move yourself to a disabled agent.")
		}
	}
	if len(errs) > 0 {
		show(http.StatusUnprocessableEntity, f, errs, "")
		return
	}
	doc, err := a.store.saveDocument(up)
	if err != nil {
		show(http.StatusUnprocessableEntity, f, []string{err.Error()}, "")
		return
	}
	var newID *IDDoc
	if f.ID.Type != "" {
		d := doc
		d.Type, d.Number, d.Expiry = f.ID.Type, f.ID.Number, f.ID.Expiry
		newID = &d
	}
	link := "/users/" + cur.Username

	if me.NeedsApproval() {
		c := UserChange{Name: f.Name, Active: f.Active, By: me.Username, ByName: me.Name, At: time.Now()}
		if newID != nil {
			c.ID = *newID
		} else {
			c.ID = cur.ID
		}
		what := c.Describe(cur)
		if what == "no changes" && c.ID.File == "" {
			a.store.removeDocument(doc.File)
			show(http.StatusOK, f, nil, "Nothing changed.")
			return
		}
		old, err := a.store.RequestChange(cur.Username, c)
		if err != nil {
			a.store.removeDocument(doc.File)
			show(http.StatusUnprocessableEntity, f, []string{err.Error()}, "")
			return
		}
		a.store.removeDocument(old)
		a.record(r, "users", "change.requested", link, "Asked to change %s (%s): %s. Waiting for approval", cur.Name, cur.Username, what)
		t, _ := a.store.User(cur.Username)
		show(http.StatusOK, formOf(t), nil, "Sent for approval. The changes apply once a manager or admin approves them.")
		return
	}

	old, err := a.store.UpdateUser(cur.Username, UserUpdate{Name: f.Name, Role: f.Role, AgentCode: f.AgentCode, Active: f.Active, ID: newID})
	if err != nil {
		a.store.removeDocument(doc.File)
		show(http.StatusUnprocessableEntity, f, []string{err.Error()}, "")
		return
	}
	a.store.removeDocument(old)
	idBefore, idAfter := "", ""
	if newID != nil && (newID.Type != cur.ID.Type || normID(newID.Number) != normID(cur.ID.Number) || newID.Expiry != cur.ID.Expiry) {
		idBefore, idAfter = cur.ID.Summary(), newID.Summary()
	}
	what := changes("name", cur.Name, f.Name, "role", string(cur.Role), string(f.Role), "agent", cur.AgentCode, f.AgentCode, "status", onOff(cur.Active), onOff(f.Active), "ID", idBefore, idAfter)
	if doc.File != "" {
		what = strings.TrimPrefix(what+"; new document photo", "no changes; ")
	}
	a.record(r, "users", "user.updated", link, "Edited user %s: %s", cur.Username, what)
	t, _ := a.store.User(cur.Username)
	show(http.StatusOK, formOf(t), nil, "Saved.")
}

func (a *app) resetPassword(w http.ResponseWriter, r *http.Request) {
	me := userOf(r.Context())
	u, ok := a.visibleUser(w, r)
	if !ok {
		return
	}
	if !me.Can(PermPassword) || (!me.Manages(u) && u.Username != me.Username) {
		page(w, r, http.StatusForbidden, "Not allowed", "", Forbidden())
		return
	}
	_ = r.ParseForm()
	pw := r.FormValue("password")
	var errs []string
	if err := ValidatePassword(pw); err != nil {
		errs = append(errs, err.Error())
	} else if pw != r.FormValue("confirm") {
		errs = append(errs, "Passwords don't match.")
	}
	if len(errs) == 0 {
		if err := a.store.SetPassword(u.Username, pw); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		page(w, r, http.StatusUnprocessableEntity, u.Name, "users", EditUserPage(u, formOf(u), a.store.Agents(), errs, ""))
		return
	}
	a.record(r, "users", "password.reset", "/users/"+u.Username, "Reset the password of %s (%s)", u.Name, u.Username)
	if me.Username == u.Username {
		a.startSession(w, r, u.Username)
	}
	page(w, r, http.StatusOK, u.Name, "users", EditUserPage(u, formOf(u), a.store.Agents(), nil, "Password reset. "+u.Name+" is signed out everywhere and must use the new password."))
}
