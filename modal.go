package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf16"

	"github.com/a-h/templ"
)

// Forms that create things (users, agents) open in a dialog over the
// current page instead of sitting at the bottom of it:
//
//   - The "Add …" button fetches the form with htmx into #modal, and
//     static/modal.js opens it as a <dialog>.
//   - The form posts with htmx. Errors (422) re-render just the form inside
//     the dialog, keeping what was typed.
//   - On success the server answers with HX-Trigger (closes the dialog and
//     shows a confirmation) and HX-Location (reloads the page underneath,
//     or goes to the new agent's page).
//
// Without htmx (e.g. the form URL opened directly) the same form is shown
// as a normal page and success redirects.

// returnPath is where to go after saving: the page the dialog was opened
// from (htmx sends it as HX-Current-URL), limited to this app's own pages.
func returnPath(r *http.Request, fallback string) string {
	if v := r.FormValue("return"); v != "" {
		return safeLocal(v, fallback)
	}
	if cur := r.Header.Get("HX-Current-URL"); cur != "" {
		if u, err := url.Parse(cur); err == nil {
			p := u.Path
			if u.RawQuery != "" {
				p += "?" + u.RawQuery
			}
			return safeLocal(p, fallback)
		}
	}
	return fallback
}

func safeLocal(p, fallback string) string {
	if p = safeNext(p); p == "/" || strings.HasPrefix(p, "/login") || strings.Contains(p, "/new") {
		return fallback
	}
	return p
}

// formPage shows a create form: as a dialog for htmx, or as a full page
// when opened directly.
func formPage(w http.ResponseWriter, r *http.Request, status int, title, active string, dialog, form templ.Component) {
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "modal" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = dialog.Render(r.Context(), w)
		return
	}
	if r.Header.Get("HX-Request") == "true" && r.Method == http.MethodPost {
		// Re-render just the form inside the open dialog.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = form.Render(r.Context(), w)
		return
	}
	page(w, r, status, title, active, StandaloneForm(title, form))
}

// formDone finishes a successful create: close the dialog, show a short
// confirmation and load `to` in the main area.
func formDone(w http.ResponseWriter, r *http.Request, to, message string) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	w.Header().Set("HX-Trigger", asciiJSON(map[string]string{"lotaria:done": message}))
	w.Header().Set("HX-Location", asciiJSON(map[string]string{"path": to, "target": "#main"}))
	w.WriteHeader(http.StatusOK)
}

// newUserHref opens "Add a user" with the agent pre-selected when the
// Users list is filtered to one agent.
func newUserHref(agent string) string {
	if agent == "" || agent == "-" {
		return "/users/new"
	}
	return "/users/new?agent=" + url.QueryEscape(agent)
}

// asciiJSON encodes v as JSON with every non-ASCII letter written as
// \uXXXX. HTTP headers are read as Latin-1, so "Tomás" sent as raw UTF-8
// would arrive as "TomÃ¡s"; escaped, it arrives intact.
func asciiJSON(v any) string {
	b, _ := json.Marshal(v)
	var out strings.Builder
	for _, r := range string(b) {
		if r < 128 {
			out.WriteRune(r)
		} else if r > 0xFFFF {
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&out, "\\u%04x\\u%04x", r1, r2)
		} else {
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return out.String()
}
