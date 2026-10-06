package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Role string

type User struct {
	Username     string    `json:"username"`
	Name         string    `json:"name"`
	Role         Role      `json:"role"`
	AgentCode    string    `json:"agent_code"` // the agent (sales point) this user works for
	PasswordHash string    `json:"password_hash"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	// PasswordChangedAt lets us drop sessions created before a reset.
	PasswordChangedAt time.Time `json:"password_changed_at"`

	ID IDDoc `json:"id_doc,omitzero"` // national ID document, see identity.go

	// Approval: "" (approved), "pending" or "rejected". Users added by a
	// seller wait for an owner, manager or admin; see approvals.go.
	Approval        string      `json:"approval,omitempty"`
	RequestedBy     string      `json:"requested_by,omitempty"`
	RequestedByName string      `json:"requested_by_name,omitempty"`
	RequestedAt     time.Time   `json:"requested_at,omitzero"`
	DecidedBy       string      `json:"decided_by,omitempty"`
	DecidedAt       time.Time   `json:"decided_at,omitzero"`
	RejectReason    string      `json:"reject_reason,omitempty"`
	Change          *UserChange `json:"change,omitempty"` // a seller's change waiting for approval
	// LastChange is how the latest change request ended, so the seller
	// who asked can see it on their Requests page.
	LastChange *ChangeResult `json:"last_change,omitempty"`
}

type ChangeResult struct {
	By       string    `json:"by"` // who asked
	Describe string    `json:"describe"`
	Approved bool      `json:"approved"`
	Reason   string    `json:"reason,omitempty"`
	At       time.Time `json:"at"`
}

const (
	ApprovalPending  = "pending"
	ApprovalRejected = "rejected"
)

func (u User) Pending() bool  { return u.Approval == ApprovalPending }
func (u User) Rejected() bool { return u.Approval == ApprovalRejected }

// CanSignIn is false while the account waits for approval, was rejected
// or is disabled.
func (u User) CanSignIn() bool { return u.Active && u.Approval == "" }

const MinPasswordLen = 8

// --- password hashing (PBKDF2-SHA256, Go standard library) ---

const pbkdf2Iter = 600_000

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iter, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked when a username doesn't exist so a login attempt
// takes the same time either way.
var dummyHash, _ = HashPassword("not-a-real-password")

// --- validation ---

var usernameRe = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func ValidateUsername(s string) error {
	if !usernameRe.MatchString(s) {
		return errors.New("Username must be 3–32 characters: lowercase letters, digits, dot, dash or underscore.")
	}
	if s == "new" {
		return errors.New("That username is reserved. Pick another.")
	}
	return nil
}

func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLen {
		return fmt.Errorf("Password must be at least %d characters.", MinPasswordLen)
	}
	return nil
}

// --- store methods ---

var (
	ErrUserExists   = errors.New("That username is already taken.")
	ErrUserNotFound = errors.New("User not found.")
	ErrLastAdmin    = errors.New("There must be at least one active admin.")
)

func (s *Store) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.d.Users) > 0
}

func (s *Store) findUser(username string) *User {
	for _, u := range s.d.Users {
		if u.Username == username {
			return u
		}
	}
	return nil
}

func (s *Store) User(username string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u := s.findUser(username); u != nil {
		return *u, true
	}
	return User{}, false
}

func (s *Store) Users() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.d.Users))
	for _, u := range s.d.Users {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (s *Store) activeAdmins() int {
	n := 0
	for _, u := range s.d.Users {
		if u.Active && u.Role == RoleAdmin {
			n++
		}
	}
	return n
}

// CreateUser adds a user. onlyIfEmpty is used by first-run setup so two
// people racing on /setup can't both become admin.
func (s *Store) CreateUser(u User, password string, onlyIfEmpty bool) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if onlyIfEmpty && len(s.d.Users) > 0 {
		return errors.New("Setup is already done.")
	}
	if s.findUser(u.Username) != nil {
		return ErrUserExists
	}
	if err := s.idTaken(u.ID, u.Username); err != nil {
		return err
	}
	now := time.Now()
	u.PasswordHash, u.CreatedAt, u.PasswordChangedAt = hash, now, now
	s.d.Users = append(s.d.Users, &u)
	return s.save()
}

// UserUpdate is an edit to a user. ID replaces the ID document when set.
type UserUpdate struct {
	Name      string
	Role      Role
	AgentCode string
	Active    bool
	ID        *IDDoc
}

// UpdateUser applies an edit, refusing changes that would leave no
// active admin. It returns the replaced document file, if any, for the
// caller to delete.
func (s *Store) UpdateUser(username string, up UserUpdate) (oldFile string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUser(username)
	if u == nil {
		return "", ErrUserNotFound
	}
	if up.ID != nil {
		if err := s.idTaken(*up.ID, username); err != nil {
			return "", err
		}
	}
	old := *u
	u.Name, u.Role, u.AgentCode, u.Active = up.Name, up.Role, up.AgentCode, up.Active
	if up.ID != nil {
		if up.ID.File != "" && up.ID.File != old.ID.File {
			oldFile = old.ID.File
		}
		if up.ID.File == "" { // details changed, same file
			up.ID.File, up.ID.FileType, up.ID.FileName = old.ID.File, old.ID.FileType, old.ID.FileName
		}
		u.ID = *up.ID
	}
	if s.activeAdmins() == 0 {
		*u = old
		return "", ErrLastAdmin
	}
	if err := s.save(); err != nil {
		*u = old
		return "", err
	}
	return oldFile, nil
}

func (s *Store) SetPassword(username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUser(username)
	if u == nil {
		return ErrUserNotFound
	}
	u.PasswordHash = hash
	u.PasswordChangedAt = time.Now()
	return s.save()
}

// Authenticate returns the user if the credentials are right and the
// account is active.
func (s *Store) Authenticate(username, password string) (User, bool) {
	u, ok := s.User(username)
	if !ok {
		CheckPassword(dummyHash, password)
		return User{}, false
	}
	if !CheckPassword(u.PasswordHash, password) || !u.CanSignIn() || !s.canWork(u) {
		return User{}, false
	}
	return u, true
}
