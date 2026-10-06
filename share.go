package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	qrcode "github.com/skip2/go-qrcode"
)

// ReceiptShare is what the receipt page needs to show and share a ticket:
// the public check link and its QR code.
type ReceiptShare struct {
	Link      string
	QR        templ.Component
	LocalOnly bool // the link points at this computer, so buyers can't open it
}

// publicBase is the address buyers use to reach this app: -public-url if
// set, otherwise the address this request came in on.
func (a *app) publicBase(r *http.Request) string {
	if a.publicURL != "" {
		return strings.TrimRight(a.publicURL, "/")
	}
	scheme := "http"
	if a.isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func isLocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func (a *app) receiptShare(r *http.Request, t Ticket) ReceiptShare {
	base := a.publicBase(r)
	link := base + "/r/" + t.Code
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	return ReceiptShare{Link: link, QR: qrSVG(link), LocalOnly: isLocalHost(host)}
}

// qrSVG draws a QR code as an SVG made of one path, so it stays sharp on
// screen, on paper and when copied into the share image.
func qrSVG(content string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		q, err := qrcode.New(content, qrcode.Medium)
		if err != nil {
			return err
		}
		q.DisableBorder = true
		bits := q.Bitmap()
		n := len(bits)
		var b strings.Builder
		fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-2 -2 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR code to check this ticket"><rect x="-2" y="-2" width="%d" height="%d" fill="#fff"/><path fill="#17203B" d="`, n+4, n+4, n+4, n+4)
		for y, row := range bits {
			for x, dark := range row {
				if dark {
					fmt.Fprintf(&b, "M%d %dh1v1h-1z", x, y)
				}
			}
		}
		b.WriteString(`"/></svg>`)
		_, err = io.WriteString(w, b.String())
		return err
	})
}

// publicTicket is the page a buyer reaches by scanning the QR code. It
// needs no sign-in and shows only that one ticket.
func (a *app) publicTicket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Referrer-Policy", "no-referrer")
	t, ok := a.store.TicketByCode(strings.ToLower(r.PathValue("code")))
	if u, signedIn := a.loadUser(r); signedIn {
		r = r.WithContext(a.withUser(r.Context(), u))
	}
	if !ok {
		page(w, r, http.StatusNotFound, "Ticket not found", "", PublicTicketMissing())
		return
	}
	page(w, r, http.StatusOK, "Ticket "+t.Serial, "", PublicTicketPage(t, a.receiptShare(r, t)))
}
