package main

import (
	"embed"
	"flag"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/a-h/templ"
)

//go:embed static
var staticFS embed.FS

type app struct {
	store      *Store
	sessions   *Sessions
	trustProxy bool   // behind ngrok or another reverse proxy
	publicURL  string // address buyers use, for QR codes (optional)
	activity   *ActivityLog
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("data", "lotto.json", "data file")
	trustProxy := flag.Bool("trust-proxy", false, "trust X-Forwarded-* headers (set when running behind ngrok or a reverse proxy)")
	publicURL := flag.String("public-url", "", "public address for QR-code links, e.g. https://my-lotto.ngrok-free.app (default: the address each request came in on)")
	flag.Parse()

	store, err := OpenStore(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	activity, err := OpenActivityLog(activityPath(*dbPath))
	if err != nil {
		log.Fatal(err)
	}
	a := &app{store: store, sessions: NewSessions(), trustProxy: *trustProxy, publicURL: *publicURL, activity: activity}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))

	// Public
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /setup", a.setupPage)
	mux.HandleFunc("GET /{$}", a.resultsPage) // lottery results, public
	mux.HandleFunc("POST /setup", a.setup)
	mux.HandleFunc("GET /r/{code}", a.publicTicket) // buyers' QR-code link, no sign-in
	mux.HandleFunc("GET /brand/logo", a.logo)

	// Signed-in users; need says which permission (roles.go) a page needs.
	need := func(p Perm, h http.HandlerFunc) http.HandlerFunc { return a.requireLogin(p, h) }
	mux.HandleFunc("GET /account", need(PermNone, a.accountPage))
	mux.HandleFunc("POST /account/password", need(PermNone, a.changeOwnPassword))

	// Selling: admins, owners, sellers
	mux.HandleFunc("GET /sell", need(PermSell, a.sellPage))
	mux.HandleFunc("POST /tickets", need(PermSell, a.createTicket))
	mux.HandleFunc("POST /tickets/preview", need(PermSell, a.previewTicket))
	mux.HandleFunc("POST /tickets/line", need(PermSell, a.addLine))
	// Sales: everyone, each seeing what their role allows
	mux.HandleFunc("GET /tickets", need(PermNone, a.listTickets))
	mux.HandleFunc("GET /tickets/{id}", need(PermNone, a.showTicket))

	// Users: everyone but what they can do depends on the role
	mux.HandleFunc("GET /users", need(PermUsers, a.usersPage))
	mux.HandleFunc("POST /users", need(PermUsers, a.createUser))
	mux.HandleFunc("GET /users/new", need(PermUsers, a.newUserPage))
	mux.HandleFunc("GET /users/{username}", need(PermUsers, a.editUserPage))
	mux.HandleFunc("POST /users/{username}", need(PermUsers, a.updateUser))
	mux.HandleFunc("POST /users/{username}/password", need(PermPassword, a.resetPassword))
	mux.HandleFunc("GET /users/{username}/document", need(PermNone, a.document))
	mux.HandleFunc("GET /approvals", need(PermUsers, a.approvalsPage))
	mux.HandleFunc("GET /users/{username}/{action}", need(PermApprove, a.decide))
	mux.HandleFunc("POST /users/{username}/{action}", need(PermApprove, a.decide))

	// Agents and prices: admins and managers
	mux.HandleFunc("GET /agents", need(PermAgents, a.agentsPage))
	mux.HandleFunc("POST /agents", need(PermAgents, a.createAgent))
	mux.HandleFunc("GET /agents/new", need(PermAgents, a.newAgentPage))
	mux.HandleFunc("GET /agents/{code}", need(PermAgents, a.editAgentPage))
	mux.HandleFunc("POST /agents/{code}", need(PermAgents, a.updateAgent))
	mux.HandleFunc("POST /agents/{code}/documents", need(PermAgents, a.updateAgentDocs))
	mux.HandleFunc("GET /agents/{code}/documents/{kind}", need(PermAgents, a.agentDocument))
	mux.HandleFunc("GET /agents/{code}/{action}", need(PermAgents, a.verifyAgent))
	mux.HandleFunc("POST /agents/{code}/{action}", need(PermAgents, a.verifyAgent))
	mux.HandleFunc("GET /settings", need(PermPrices, a.pricesPage))
	mux.HandleFunc("POST /settings", need(PermPrices, a.createPrices))
	mux.HandleFunc("GET /settings/new", need(PermPrices, a.newPricesPage))
	mux.HandleFunc("GET /settings/{id}/{action}", need(PermPrices, a.priceAction))
	mux.HandleFunc("POST /settings/{id}/{action}", need(PermPrices, a.priceAction))

	// Activity: admins, managers, owners (their agent's) — read only
	mux.HandleFunc("GET /activity", need(PermActivity, a.activityPage))
	mux.HandleFunc("GET /activity.csv", need(PermActivity, a.activityCSV))

	// Admins only
	mux.HandleFunc("GET /results/new", need(PermResults, a.newResultPage))
	mux.HandleFunc("POST /results", need(PermResults, a.saveResult))
	mux.HandleFunc("GET /results/{date}/{action}", need(PermResults, a.resultAction))
	mux.HandleFunc("POST /results/{date}/{action}", need(PermResults, a.resultAction))
	mux.HandleFunc("GET /setup/app", need(PermSetup, a.brandingPage))
	mux.HandleFunc("POST /setup/app", need(PermSetup, a.saveBranding))

	log.Printf("listening on http://localhost%s", *addr)
	if !store.HasUsers() {
		log.Printf("first run: open http://localhost%s/setup on this computer to create the admin", *addr)
	}
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// isPartial reports whether htmx asked for just the content area.
// History restores (back button after a cache miss) need the full page.
func isPartial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}

// page renders the whole document on normal loads, and only the
// title, nav and content on htmx requests.
func page(w http.ResponseWriter, r *http.Request, status int, title, active string, content templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "HX-Request")
	if r.Method == http.MethodGet && isPartial(r) && w.Header().Get("HX-Push-Url") == "" {
		if clean, changed := cleanURL(r); changed {
			w.Header().Set("HX-Push-Url", clean) // e.g. ?q=lucia instead of ?q=lucia&role=&agent=
		}
	}
	w.WriteHeader(status)
	var c templ.Component
	if isPartial(r) {
		c = Partial(title, active, content)
	} else {
		c = Layout(title, active, content)
	}
	if err := c.Render(r.Context(), w); err != nil {
		log.Printf("render: %v", err)
	}
}

// cleanURL drops empty search fields and "page=1" from the address, so a
// search form doesn't leave "?q=x&role=&agent=" in the address bar.
func cleanURL(r *http.Request) (string, bool) {
	q := r.URL.Query()
	changed := false
	for k, vs := range q {
		var keep []string
		for _, v := range vs {
			if v != "" {
				keep = append(keep, v)
			}
		}
		if len(keep) == 0 || (k == "page" && len(keep) == 1 && keep[0] == "1") {
			delete(q, k)
			changed = true
		} else {
			q[k] = keep
		}
	}
	if !changed {
		return "", false
	}
	if len(q) == 0 {
		return r.URL.Path, true
	}
	return r.URL.Path + "?" + q.Encode(), true
}

func today() string { return time.Now().Format("2006-01-02") }

func (a *app) showTicket(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	t, ok := a.store.Ticket(id)
	if u, _ := CurrentUser(r.Context()); ok && !u.SeesTicket(t) {
		ok = false // sellers see their own tickets, owners their agent's
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	page(w, r, http.StatusOK, "Ticket "+t.Serial, "sales", TicketPage(t, false, a.receiptShare(r, t)))
}

func (a *app) listTickets(w http.ResponseWriter, r *http.Request) {
	u, _ := CurrentUser(r.Context())
	q := r.URL.Query()
	f := TicketFilter{
		DrawDate:  q.Get("date"),
		Seller:    q.Get("seller"),
		AgentCode: NormalizeAgentCode(q.Get("agent")),
		Search:    newSearch(q.Get("q")),
	}
	var opts SalesOptions
	switch {
	case u.Can(PermSalesAll):
		opts.Agents = a.store.Agents()
		// A seller filter (e.g. from a user's page) shows as a removable chip.
		if f.Seller != "" {
			if s, ok := a.store.User(f.Seller); ok && (f.AgentCode == "" || s.AgentCode == f.AgentCode) {
				opts.SellerName = s.Name
			} else {
				f.Seller = "" // unknown, or not at the chosen agent
			}
		}
	case u.Role == RoleOwner:
		// Owners see their agent's sales, and can pick one of its sellers.
		f.AgentCode = u.AgentCode
		if f.Seller != "" {
			if s, ok := a.store.User(f.Seller); ok && s.AgentCode == u.AgentCode {
				opts.SellerName = s.Name
			} else {
				f.Seller = ""
			}
		}
	default:
		// Sellers only see their own sales.
		f = TicketFilter{DrawDate: f.DrawDate, Seller: u.Username, Search: f.Search}
	}
	tickets := a.store.Tickets(f)

	// Totals cover every matching ticket, not just the page on screen.
	sum := SalesSummary{ByGame: map[Game]Cents{}, Tickets: len(tickets)}
	byAgent := map[string]*AgentTotal{}
	for _, t := range tickets {
		sum.Total += t.Total()
		for _, l := range t.Lines {
			sum.ByGame[l.Game] += l.Amount()
		}
		at := byAgent[t.AgentCode]
		if at == nil {
			at = &AgentTotal{Code: t.AgentCode, Name: t.Agent}
			byAgent[t.AgentCode] = at
			sum.ByAgent = append(sum.ByAgent, at)
		}
		at.Tickets++
		at.Total += t.Total()
	}
	sort.Slice(sum.ByAgent, func(i, j int) bool { return sum.ByAgent[i].Total > sum.ByAgent[j].Total })

	pg := newPager(r, "/tickets", perPageTickets, len(tickets))
	page(w, r, http.StatusOK, "Sales", "sales", TicketsPage(f, opts, pageOf(tickets, pg), sum, pg))
}
