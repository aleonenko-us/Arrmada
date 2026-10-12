package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/mail"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// Send to Kindle: a person saves their Kindle's email address once, then taps Send on
// a book and the ebook is emailed there through the owner's outgoing mail (BOOK-23).
// Kindle owners can't use OPDS, and this is what Audiobookshelf's email-to-device did.
//
// Privacy, as for listening: nothing staff can see pairs a person with a book they sent.
// The send runs as a job with no book in its target, logs carry the outcome and SMTP code
// only, and the result ("Sent “Dune” to your Kindle") goes to the sender's own inbox.
// An email is only ever sent when its person presses the button.

// kindleDailyLimit caps sends per person per day: Amazon throttles busy senders, and a
// stuck button shouldn't email a Kindle fifty times.
const kindleDailyLimit = 20

// kindleSendTimeout bounds one send: a 50 MB attachment over a slow uplink.
const kindleSendTimeout = 3 * time.Minute

// kindleQuota counts each person's sends today, in memory (a restart forgets — the limit is
// a brake, not an accounting). The zero value is ready to use.
type kindleQuota struct {
	mu     sync.Mutex
	day    string
	counts map[int64]int
}

// take counts one send for userID, or reports false when today's limit is reached.
func (q *kindleQuota) take(userID int64, now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if day := now.Format("2006-01-02"); q.day != day || q.counts == nil {
		q.day, q.counts = day, map[int64]int{}
	}
	if q.counts[userID] >= kindleDailyLimit {
		return false
	}
	q.counts[userID]++
	return true
}

// refund gives back a send that never started.
func (q *kindleQuota) refund(userID int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.counts[userID] > 0 {
		q.counts[userID]--
	}
}

// kindleDomains are Amazon's Send-to-Kindle domains. Anything else is saved with a warning
// rather than refused: Amazon has changed them before.
var kindleDomains = []string{"kindle.com", "free.kindle.com", "kindle.cn"}

// kindleWarning is the gentle note for an address that isn't at a Kindle domain.
func kindleWarning(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if addr == "" || at < 0 {
		return ""
	}
	domain := strings.ToLower(addr[at+1:])
	for _, d := range kindleDomains {
		if domain == d {
			return ""
		}
	}
	return "That isn't a @kindle.com address. Your Kindle's address is on Amazon's “Manage Your Content and Devices” page, under Preferences → Personal Document Settings."
}

// parseKindleAddress accepts a bare address (no display name), at most 254 characters.
func parseKindleAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > 254 {
		return "", errors.New("that address is too long")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || !strings.EqualFold(a.Address, s) {
		return "", errors.New("enter just the address, like name_42@kindle.com")
	}
	return a.Address, nil
}

// kindleView is what the shelf's Kindle sheet needs: the person's address, and whether
// sending is possible and from which address (to add to Amazon's approved list).
type kindleView struct {
	KindleEmail    string `json:"kindle_email"`
	FromAddress    string `json:"from_address,omitempty"`
	SMTPConfigured bool   `json:"smtp_configured"`
	Warning        string `json:"warning,omitempty"`
}

func (a *api) kindleView(ctx context.Context, userID int64) (kindleView, error) {
	addr, err := a.deps.Auth.KindleEmail(ctx, userID)
	if err != nil && !errors.Is(err, auth.ErrNotFound) {
		return kindleView{}, err
	}
	v := kindleView{KindleEmail: addr, Warning: kindleWarning(addr)}
	if cfg, _ := a.mailConfig(ctx); cfg.Ready() {
		v.SMTPConfigured = true
		if from, err := mail.ParseAddress(cfg.From); err == nil {
			v.FromAddress = from.Address
		}
	}
	return v, nil
}

// kindleCaller is the signed-in person for the Kindle routes: requester rank or above
// (read-only accounts have no shelf), with the Books module on. It writes the refusal.
func (a *api) kindleCaller(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "sign in first")
		return nil, false
	}
	if !a.mayDownloadBook(r, books.Book{}) {
		a.writeError(w, http.StatusForbidden, "your account can't send books")
		return nil, false
	}
	if !a.booksEnabled(r.Context()) {
		a.writeError(w, http.StatusNotFound, "books are switched off")
		return nil, false
	}
	return u, true
}

// handleGetKindle returns the caller's Kindle address and whether sending works.
//
//	GET /api/v1/me/kindle
func (a *api) handleGetKindle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.kindleCaller(w, r)
	if !ok {
		return
	}
	v, err := a.kindleView(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read your Kindle address")
		return
	}
	a.writeJSON(w, http.StatusOK, v)
}

// handleSetKindle saves the caller's Kindle address ("" removes it). An address outside
// Amazon's Kindle domains is saved with a warning, not refused.
//
//	PUT /api/v1/me/kindle {kindle_email}
func (a *api) handleSetKindle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.kindleCaller(w, r)
	if !ok {
		return
	}
	var req struct {
		KindleEmail string `json:"kindle_email"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	addr := ""
	if strings.TrimSpace(req.KindleEmail) != "" {
		var err error
		if addr, err = parseKindleAddress(req.KindleEmail); err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := a.deps.Auth.SetKindleEmail(r.Context(), u.ID, addr); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save your Kindle address")
		return
	}
	v, err := a.kindleView(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read your Kindle address")
		return
	}
	a.writeJSON(w, http.StatusOK, v)
}

// handleSendToKindle emails a book's ebook to the caller's Kindle. Everything that can be
// checked up front is (address, mail setup, file, format, size, today's limit); the send
// itself runs in the background and its result arrives in the caller's inbox.
//
//	POST /api/v1/me/books/{id}/send-to-kindle → 202
func (a *api) handleSendToKindle(w http.ResponseWriter, r *http.Request) {
	u, ok := a.kindleCaller(w, r)
	if !ok {
		return
	}
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if a.deps.Books == nil {
		a.writeError(w, http.StatusNotFound, "no such book")
		return
	}
	b, err := a.deps.Books.Get(ctx, id)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "no such book")
		return
	}
	to, err := a.deps.Auth.KindleEmail(ctx, u.ID)
	if err != nil && !errors.Is(err, auth.ErrNotFound) {
		a.writeError(w, http.StatusInternalServerError, "could not read your Kindle address")
		return
	}
	if to == "" {
		a.writeError(w, http.StatusBadRequest, "add your Kindle's email address first")
		return
	}
	cfg, password := a.mailConfig(ctx)
	if !cfg.Ready() {
		a.writeError(w, http.StatusConflict, "sending to Kindle isn't set up on this server yet — ask whoever runs it to set up outgoing email")
		return
	}
	if b.Ebook == nil || b.Ebook.Path == "" {
		a.writeError(w, http.StatusNotFound, "this book has no ebook yet")
		return
	}
	path, err := books.EbookFileFor(b.Ebook.Path, books.KindleAccepts)
	if err != nil {
		a.deps.Log.Warn("send to kindle: the ebook file is missing")
		a.writeError(w, http.StatusNotFound, "the ebook file is missing on the server")
		return
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !books.KindleAccepts(ext) {
		a.writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("this ebook is %s — Kindle email takes EPUB or PDF", strings.ToUpper(strings.TrimPrefix(ext, "."))))
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		a.writeError(w, http.StatusNotFound, "the ebook file is missing on the server")
		return
	}
	if st.Size() > books.KindleMaxBytes {
		a.writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("this ebook is %d MB — Kindle email takes files up to %d MB", (st.Size()+(1<<20)-1)>>20, books.KindleMaxBytes>>20))
		return
	}
	if !a.kindle.take(u.ID, time.Now()) {
		a.writeError(w, http.StatusTooManyRequests, fmt.Sprintf("you've sent %d books to your Kindle today — try again tomorrow", kindleDailyLimit))
		return
	}
	send := kindleSend{userID: u.ID, to: to, cfg: cfg, password: password, title: b.Title,
		name: books.EbookDownloadName(b, ext), contentType: books.EbookContentType(ext), path: path, ref: "kindle:" + randomRef()}
	// The job's target is the send's own reference: staff see that a send ran, never
	// which book or whose Kindle.
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{
		Kind: "kindle.send", Target: send.ref, Timeout: kindleSendTimeout,
		Fn: func(ctx context.Context, _ *jobs.Progress) (any, error) {
			a.sendToKindle(ctx, send)
			return nil, nil
		},
	})
	if !ok {
		a.kindle.refund(u.ID)
		return
	}
	a.accepted(w, jobID, existing, map[string]any{"status": "sending"})
}

// kindleSend is one send, captured when it was accepted.
type kindleSend struct {
	userID      int64
	to          string
	cfg         mail.Config
	password    string
	title       string
	name        string
	contentType string
	path        string
	ref         string
}

// sendToKindle emails the file and tells the sender how it went, in their own inbox.
func (a *api) sendToKindle(ctx context.Context, s kindleSend) {
	err := mail.Send(ctx, s.cfg, s.password, mail.Message{
		To:      s.to,
		Subject: s.title,
		Body:    "Sent from Arrmada.",
		Attachment: &mail.Attachment{Name: s.name, ContentType: s.contentType,
			Open: func() (io.ReadCloser, error) { return os.Open(s.path) }},
	})
	n := requests.UserNotice{Ref: s.ref, Kind: "kindle-send", MediaType: "book",
		Title: "Sent to your Kindle", Body: fmt.Sprintf("Sent “%s” to your Kindle. It shows up there in a few minutes.", s.title)}
	if err != nil {
		a.deps.Log.Warn("send to kindle: failed", "code", mail.ReplyCode(err))
		n.Title = "Couldn't send to your Kindle"
		n.Body = fmt.Sprintf("Couldn't send “%s” to your Kindle: %s", s.title, shortReply(err.Error()))
	} else {
		a.deps.Log.Info("send to kindle: sent")
	}
	// The send may have used up its time; telling them gets its own short window.
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if a.deps.Requests != nil {
		if _, nerr := a.deps.Requests.NotifyUsers(nctx, []int64{s.userID}, n); nerr != nil {
			a.deps.Log.Warn("send to kindle: couldn't write the inbox message", "err", nerr)
		}
	}
	// Their bell refreshes now rather than at its next poll.
	if a.deps.Bus != nil {
		a.deps.Bus.Publish(eventbus.UserTopic(s.userID, "inbox.updated"), map[string]any{})
	}
}

// shortReply keeps a mail server's reply to one readable line.
func shortReply(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return s
}

// randomRef is a fresh, meaningless reference for one send.
func randomRef() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
