package main

// Roles and what each may do.
//
//	Admin    everything (the activity log can't be deleted by anyone)
//	Manager  no selling; manages users and agents, sees all sales,
//	         manages prices, approves new users; sees the activity log
//	Owner    owns an agent: sells, sees that agent's sales and activity,
//	         adds and changes its sellers, which a manager or admin must
//	         approve
//	Seller   sells and sees own sales; doesn't manage users

const (
	RoleAdmin   Role = "admin"
	RoleManager Role = "manager"
	RoleOwner   Role = "owner"
	RoleSeller  Role = "seller"
)

// Roles in order, for selects and filters.
var Roles = []Role{RoleAdmin, RoleManager, RoleOwner, RoleSeller}

func (r Role) Valid() bool {
	for _, x := range Roles {
		if r == x {
			return true
		}
	}
	return false
}

func (r Role) Label() string {
	switch r {
	case RoleAdmin:
		return "Admin"
	case RoleManager:
		return "Manager"
	case RoleOwner:
		return "Owner"
	case RoleSeller:
		return "Seller"
	}
	return string(r)
}

// Help is the one-line description shown in the role select.
func (r Role) Help() string {
	switch r {
	case RoleAdmin:
		return "Admin: everything"
	case RoleManager:
		return "Manager: users, agents, prices, all sales, approvals; doesn't sell"
	case RoleOwner:
		return "Owner: runs an agent; sells, adds its sellers (approved by a manager)"
	case RoleSeller:
		return "Seller: sells tickets, sees own sales"
	}
	return string(r)
}

// NeedsAgent is true for roles that work for one agent.
func (r Role) NeedsAgent() bool { return r == RoleOwner || r == RoleSeller }

// Perm is something a role may be allowed to do.
type Perm int

const (
	PermSell     Perm = iota // sell tickets
	PermSalesAll             // see every agent's sales
	PermUsers                // open the Users page (what's on it depends on the role)
	PermAgents               // manage agents
	PermPrices               // manage price lists
	PermApprove              // approve new users and changes
	PermActivity             // see the activity log (owners: their agent's)
	PermResults              // enter and publish results
	PermSetup                // App setup
	PermPassword             // reset other people's passwords
)

var perms = map[Role][]Perm{
	RoleAdmin:   {PermSell, PermSalesAll, PermUsers, PermAgents, PermPrices, PermApprove, PermActivity, PermResults, PermSetup, PermPassword},
	RoleManager: {PermSalesAll, PermUsers, PermAgents, PermPrices, PermApprove, PermActivity, PermPassword},
	RoleOwner:   {PermSell, PermUsers, PermActivity, PermPassword},
	RoleSeller:  {PermSell},
}

// Can reports whether the user's role allows p.
func (u User) Can(p Perm) bool {
	for _, x := range perms[u.Role] {
		if x == p {
			return true
		}
	}
	return false
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// AssignableRoles are the roles u may give to users they add or edit.
func (u User) AssignableRoles() []Role {
	switch u.Role {
	case RoleAdmin:
		return Roles
	case RoleManager:
		return []Role{RoleManager, RoleOwner, RoleSeller}
	case RoleOwner:
		return []Role{RoleSeller}
	}
	return nil
}

func (u User) mayAssign(r Role) bool {
	for _, x := range u.AssignableRoles() {
		if x == r {
			return true
		}
	}
	return false
}

// PicksAgent is true when u chooses the agent of users they add; owners
// and sellers always add users to their own agent.
func (u User) PicksAgent() bool { return u.Role == RoleAdmin || u.Role == RoleManager }

// SeesUser reports whether u may look at someone on the Users page.
func (u User) SeesUser(t User) bool {
	switch u.Role {
	case RoleAdmin, RoleManager:
		return true
	case RoleOwner:
		return t.AgentCode != "" && t.AgentCode == u.AgentCode
	}
	return false
}

// Manages reports whether u may change t (or, for sellers, ask to).
func (u User) Manages(t User) bool {
	if u.Username == t.Username && u.Role != RoleAdmin {
		return false // your own details are on My account
	}
	switch u.Role {
	case RoleAdmin:
		return true
	case RoleManager:
		return t.Role != RoleAdmin
	case RoleOwner:
		return t.Role == RoleSeller && t.AgentCode != "" && t.AgentCode == u.AgentCode
	}
	return false
}

// NeedsApproval is true when what u adds or changes waits for a manager
// or admin to approve it: owners adding or changing their sellers.
// manager or admin to approve it.
func (u User) NeedsApproval() bool { return u.Role == RoleOwner }

// Approves reports whether u may approve a request about t made by
// requester. Nobody approves their own request.
func (u User) Approves(t User, requester string) bool {
	if !u.Can(PermApprove) || u.Username == requester || u.Username == t.Username {
		return false
	}
	switch u.Role {
	case RoleAdmin:
		return true
	case RoleManager:
		return t.Role != RoleAdmin
	}
	return false
}

// SeesTicket reports whether u may open a ticket.
func (u User) SeesTicket(t Ticket) bool {
	switch {
	case u.Can(PermSalesAll):
		return true
	case u.Role == RoleOwner:
		return t.AgentCode == u.AgentCode && u.AgentCode != ""
	}
	return t.Seller == u.Username
}

// SeesDocument reports whether u may open t's ID document.
func (u User) SeesDocument(t User) bool {
	if u.Username == t.Username {
		return true
	}
	switch u.Role {
	case RoleAdmin:
		return true
	case RoleManager:
		return t.Role != RoleAdmin
	case RoleOwner:
		return t.AgentCode != "" && t.AgentCode == u.AgentCode
	}
	return false
}
