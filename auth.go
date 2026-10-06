package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "lotto_session"
	maxFailures   = 5
	lockoutWindow = 15 * time.Minute
)

type session struct {
	Username string
	Created  time.Time
	Expires  time.Time
}

// Sessions are kept in memory (keyed by a hash of the cookie token), so
// restarting the server signs everyone out.
type Sessions struct {
	mu       sync.Mutex
	byHash   map[string]session
	failures map[string][]time.Time // "ip|username" -> recent failed logins
}

func NewSessions() *Sessions {
	return &Sessions{byHash: map[string]session{}, failures: map[string][]time.Time{}}
}

func tokenHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func (s *Sessions) Create(username string) (string, time.Time) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	tok := hex.EncodeToString(b)
	now := time.Now()
	exp := now.Add(time.Duration(brand().SessionHours) * time.Hour) // set on the App setup page
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[tokenHash(tok)] = session{Username: username, Created: now, Expires: exp}
	for k, v := range s.byHash { // opportunistic cleanup
		if now.After(v.Expires) {
			delete(s.byHash, k)
		}
	}
	return tok, exp
}

func (s *Sessions) Get(tok string) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byHash[tokenHash(tok)]
	if !ok || time.Now().After(sess.Expires) {
		return session{}, false
	}
	return sess, true
}

func (s *Sessions) Delete(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byHash, tokenHash(tok))
}

// Locked reports whether too many recent failures came from this key.
func (s *Sessions) Locked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-lockoutWindow)
	recent := s.failures[key][:0]
	for _, t := range s.failures[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.failures[key] = recent
	return len(recent) >= maxFailures
}

func (s *Sessions) Fail(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[key] = append(s.failures[key], time.Now())
}

func (s *Sessions) Clear(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, key)
}

// --- request context ---

type ctxKey struct{}

type ctxVal struct {
	user    User
	agent   Agent // zero if the user has no agent
	pending int   // approvals waiting for this user, for the nav badge
}

func (a *app) withUser(ctx context.Context, u User) context.Context {
	ag, _ := a.store.Agent(u.AgentCode)
	return context.WithValue(ctx, ctxKey{}, ctxVal{user: u, agent: ag, pending: a.pendingCount(u)})
}

// pendingOf is the number of approvals waiting for the signed-in user.
func pendingOf(ctx context.Context) int {
	v, _ := ctx.Value(ctxKey{}).(ctxVal)
	return v.pending
}

// CurrentUser is used by handlers and templates.
func CurrentUser(ctx context.Context) (User, bool) {
	v, ok := ctx.Value(ctxKey{}).(ctxVal)
	return v.user, ok
}

// CurrentAgent is the signed-in user's agent, if any.
func CurrentAgent(ctx context.Context) Agent {
	v, _ := ctx.Value(ctxKey{}).(ctxVal)
	return v.agent
}

// loadUser returns the signed-in user for this request, if the session
// is valid, the account still active and the password unchanged since.
func (a *app) loadUser(r *http.Request) (User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return User{}, false
	}
	sess, ok := a.sessions.Get(c.Value)
	if !ok {
		return User{}, false
	}
	u, ok := a.store.User(sess.Username)
	if !ok || !u.CanSignIn() || sess.Created.Before(u.PasswordChangedAt) || !a.store.canWork(u) {
		return User{}, false
	}
	return u, true
}

// redirect works for both normal and htmx requests. htmx gets a full
// page load so the layout (nav, user menu) is rebuilt.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// sameOrigin rejects cross-site form posts (CSRF). SameSite=Lax cookies
// already cover modern browsers; this is a second check.
func sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host
	}
	return true
}

// PermNone lets in anyone who is signed in.
const PermNone Perm = -1

// requireLogin wraps a handler: the user must be signed in and, unless
// need is PermNone, their role must allow need.
func (a *app) requireLogin(need Perm, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		u, ok := a.loadUser(r)
		if !ok {
			if !a.store.HasUsers() {
				redirect(w, r, "/setup")
				return
			}
			next := r.URL.RequestURI()
			if r.Method != http.MethodGet {
				next = "/"
			}
			redirect(w, r, "/login?next="+url.QueryEscape(next))
			return
		}
		if need != PermNone && !u.Can(need) {
			r = r.WithContext(a.withUser(r.Context(), u))
			a.record(r, "security", "access.denied", "", "Was refused a page their role can't use: %s %s", r.Method, r.URL.Path)
			page(w, r, http.StatusForbidden, "Not allowed", "", Forbidden())
			return
		}
		h(w, r.WithContext(a.withUser(r.Context(), u)))
	}
}

func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientIP is the visitor's address. Behind a tunnel like ngrok every
// request comes from 127.0.0.1, so with -trust-proxy the address the
// proxy puts in X-Forwarded-For is used instead.
func (a *app) clientIP(r *http.Request) string {
	if a.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	return remoteHost(r)
}

// isHTTPS is true for direct TLS, or for an HTTPS tunnel/proxy we trust.
func (a *app) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (a.trustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

// isLocal reports whether the request comes straight from this computer,
// not through a tunnel or proxy (those add forwarding headers).
func isLocal(r *http.Request) bool {
	ip := net.ParseIP(remoteHost(r))
	return ip != nil && ip.IsLoopback() &&
		r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("Forwarded") == ""
}

func (a *app) startSession(w http.ResponseWriter, r *http.Request, username string) {
	tok, exp := a.sessions.Create(username)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.isHTTPS(r),
	})
}

// --- handlers: login, logout, first-run setup, own password ---

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	if !a.store.HasUsers() {
		redirect(w, r, "/setup")
		return
	}
	if _, ok := a.loadUser(r); ok {
		redirect(w, r, "/")
		return
	}
	page(w, r, http.StatusOK, "Sign in", "", LoginPage("", safeNext(r.URL.Query().Get("next")), ""))
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	username := NormalizeUsername(r.FormValue("username"))
	next := safeNext(r.FormValue("next"))
	if next == "/" {
		next = "/sell" // after signing in, people usually want to sell
	}
	key := a.clientIP(r) + "|" + username
	who := User{} // don't put made-up usernames into the user filter
	if known, exists := a.store.User(username); exists {
		who = known
	}
	if a.sessions.Locked(key) {
		a.recordAs(r, who, "auth", "login.locked", "", "Sign-in refused: too many failed attempts for %q", username)
		page(w, r, http.StatusTooManyRequests, "Sign in", "", LoginPage(username, next, "Too many failed attempts. Try again in 15 minutes."))
		return
	}
	u, ok := a.store.Authenticate(username, r.FormValue("password"))
	if !ok {
		a.sessions.Fail(key)
		a.recordAs(r, who, "auth", "login.failed", "", "Failed sign-in as %q", username)
		if a.sessions.Locked(key) {
			a.recordAs(r, who, "auth", "login.locked", "", "Sign-in locked for 15 minutes after %d failed attempts as %q", maxFailures, username)
		}
		msg := "Wrong username or password, or the account (or its agent) is disabled."
		if who.Username != "" && (who.Pending() || who.Rejected()) && CheckPassword(who.PasswordHash, r.FormValue("password")) {
			msg = "Your account is waiting for a manager or admin to approve it."
			if who.Rejected() {
				msg = "Your account was not approved: " + who.RejectReason
			}
		}
		if who.Username != "" && who.CanSignIn() && who.Role.NeedsAgent() {
			if ag, ok := a.store.Agent(who.AgentCode); ok && ag.Active && !ag.Verified() && CheckPassword(who.PasswordHash, r.FormValue("password")) {
				msg = "Agent " + ag.Code + " is waiting for its documents to be verified. You can sign in once an admin or manager verifies it."
				if ag.Rejected() {
					msg = "Agent " + ag.Code + " was not verified: " + ag.RejectReason
				}
			}
		}
		page(w, r, http.StatusUnprocessableEntity, "Sign in", "", LoginPage(username, next, msg))
		return
	}
	a.sessions.Clear(key)
	a.startSession(w, r, u.Username)
	a.recordAs(r, u, "auth", "login", "", "Signed in")
	redirect(w, r, next)
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if sess, ok := a.sessions.Get(c.Value); ok {
			u, _ := a.store.User(sess.Username)
			a.recordAs(r, u, "auth", "logout", "", "Signed out")
		}
		a.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	redirect(w, r, "/login")
}

// First-run setup creates an admin, so it's only allowed from the
// computer running the app. Otherwise anyone with a shared link could
// claim the admin account before you do.
func (a *app) setupPage(w http.ResponseWriter, r *http.Request) {
	if a.store.HasUsers() {
		redirect(w, r, "/login")
		return
	}
	if !isLocal(r) {
		page(w, r, http.StatusForbidden, "Set up", "", SetupRemote())
		return
	}
	page(w, r, http.StatusOK, "Set up", "", SetupPage(UserForm{Role: RoleAdmin}, nil))
}

func (a *app) setup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return
	}
	if a.store.HasUsers() {
		redirect(w, r, "/login")
		return
	}
	if !isLocal(r) {
		page(w, r, http.StatusForbidden, "Set up", "", SetupRemote())
		return
	}
	_ = r.ParseForm()
	r.Form.Set("role", string(RoleAdmin)) // the first user is always an admin
	r.Form.Del("agent_code")              // the first admin has no agent yet
	f, errs := a.readUserForm(r, userFormOpts{me: User{Role: RoleAdmin}, withPassword: true, noID: true})
	if len(errs) == 0 {
		err := a.store.CreateUser(User{Username: f.Username, Name: f.Name, Role: RoleAdmin, Active: true}, r.FormValue("password"), true)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		page(w, r, http.StatusUnprocessableEntity, "Set up", "", SetupPage(f, errs))
		return
	}
	a.startSession(w, r, f.Username)
	a.recordAs(r, User{Username: f.Username, Name: f.Name}, "users", "setup.admin", "/users/"+f.Username, "Created the first admin account, %s (%s)", f.Name, f.Username)
	redirect(w, r, "/sell")
}

func (a *app) accountPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, http.StatusOK, "My account", "account", AccountPage(nil, false))
}

func (a *app) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	u, _ := CurrentUser(r.Context())
	_ = r.ParseForm()
	var errs []string
	if !CheckPassword(u.PasswordHash, r.FormValue("current")) {
		errs = append(errs, "Current password is wrong.")
	}
	pw := r.FormValue("password")
	if err := ValidatePassword(pw); err != nil {
		errs = append(errs, err.Error())
	} else if pw != r.FormValue("confirm") {
		errs = append(errs, "New passwords don't match.")
	}
	if len(errs) == 0 {
		if err := a.store.SetPassword(u.Username, pw); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		page(w, r, http.StatusUnprocessableEntity, "My account", "account", AccountPage(errs, false))
		return
	}
	a.record(r, "auth", "password.changed", "", "Changed their own password")
	// Changing the password ends all older sessions; start a fresh one.
	a.startSession(w, r, u.Username)
	page(w, r, http.StatusOK, "My account", "account", AccountPage(nil, true))
}
