package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/mail/mailtest"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// kindleFixture is a server with books, the inbox, outgoing mail pointed at a fake SMTP
// server, a requester, and three books: an EPUB, an AZW3 and one with no ebook.
type kindleFixture struct {
	s                       *routeServer
	logs                    *lockedBuf
	fake                    *mailtest.Server
	reader                  *auth.User
	cookie, admin           *http.Cookie
	epub, azw3, none, large books.Book
}

func newKindleFixture(t *testing.T) *kindleFixture {
	t.Helper()
	root := t.TempDir()
	s, logs := newMailServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Downloads = download.NewService(db, d.Log)
		d.Quality = quality.NewService(db)
		mv := movies.NewService(db, nil, nil, root, "", nil, d.Log)
		sr := series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, mv, sr, d.Books, nil, d.Quality, nil, "", d.Log)
	})
	f := &kindleFixture{s: s, logs: logs}
	f.reader, f.cookie = s.user(t, "reader", auth.RoleRequester)
	_, f.admin = s.user(t, "admin-one", auth.RoleAdmin)
	f.fake = startMailFake(t, mailtest.Options{User: "mailer", Pass: "app-pw"})
	saveSMTP(t, s, f.admin, f.fake, "mailer", "app-pw")

	ctx := context.Background()
	repo := books.NewRepo(s.st.DB())
	mk := func(key, title, file string, size int64) books.Book {
		b, err := repo.Create(ctx, books.Book{OLKey: key, Title: title, Author: "Frank Herbert"})
		if err != nil {
			t.Fatal(err)
		}
		if file != "" {
			p := filepath.Join(root, file)
			fh, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fh.WriteString("PK fake ebook bytes")
			if size > 0 {
				_ = fh.Truncate(size) // sparse: no real 50 MB on disk
			}
			_ = fh.Close()
			if err := repo.SetEdition(ctx, b.ID, books.KindEbook, p, strings.TrimPrefix(filepath.Ext(file), "."), 1, 1); err != nil {
				t.Fatal(err)
			}
		}
		return b
	}
	f.epub = mk("OL1W", "Dune", "dune.epub", 0)
	f.azw3 = mk("OL2W", "Dune Messiah", "messiah.azw3", 0)
	f.none = mk("OL3W", "Children of Dune", "", 0)
	f.large = mk("OL4W", "God Emperor of Dune", "emperor.pdf", books.KindleMaxBytes+1)
	return f
}

func (f *kindleFixture) setAddress(t *testing.T, addr string) *http.Response {
	t.Helper()
	rec := f.s.doJSON("PUT", "/api/v1/me/kindle", f.cookie, fmt.Sprintf(`{"kindle_email":%q}`, addr))
	return rec.Result()
}

func (f *kindleFixture) send(id int64) (int, string) {
	rec := f.s.do("POST", fmt.Sprintf("/api/v1/me/books/%d/send-to-kindle", id), f.cookie)
	return rec.Code, rec.Body.String()
}

// BOOK-24: a requester saves their Kindle address and sends an EPUB; the fake mail server
// gets it as an attachment, the inbox says it was sent, and no log line names the book or
// pairs the person with it.
func TestSendToKindleDeliversAndTellsTheInbox(t *testing.T) {
	f := newKindleFixture(t)
	if code, body := f.send(f.epub.ID); code != http.StatusBadRequest {
		t.Fatalf("no Kindle address yet: HTTP %d %s, want 400", code, body)
	}
	if res := f.setAddress(t, "reader_42@kindle.com"); res.StatusCode != http.StatusOK {
		t.Fatalf("save address: HTTP %d", res.StatusCode)
	}
	rec := f.s.do("GET", "/api/v1/me/kindle", f.cookie)
	var v kindleView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.KindleEmail != "reader_42@kindle.com" || !v.SMTPConfigured || v.FromAddress != "books@example.com" || v.Warning != "" {
		t.Errorf("kindle view = %+v", v)
	}

	code, body := f.send(f.epub.ID)
	if code != http.StatusAccepted {
		t.Fatalf("send: HTTP %d %s, want 202", code, body)
	}
	deadline := time.Now().Add(10 * time.Second)
	var inbox []requests.UserNotification
	for time.Now().Before(deadline) {
		inbox, _ = f.s.deps.Requests.Inbox(context.Background(), f.reader.ID)
		if len(inbox) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(inbox) != 1 || !strings.Contains(inbox[0].Body, "Sent “Dune” to your Kindle") || !strings.HasPrefix(inbox[0].Ref, "kindle:") {
		t.Fatalf("inbox = %+v, want one 'Sent “Dune”' message", inbox)
	}
	msgs := f.fake.Messages()
	if len(msgs) != 1 || msgs[0].To[0] != "reader_42@kindle.com" || !strings.Contains(msgs[0].Data, `filename="Dune - Frank Herbert.epub"`) || !strings.Contains(msgs[0].Data, "application/epub+zip") {
		t.Fatalf("fake server got %+v", msgs)
	}

	logs := f.logs.String()
	for _, leak := range []string{"Dune", fmt.Sprintf("book=%d", f.epub.ID), "reader_42", "/me/books/1/"} {
		if strings.Contains(logs, leak) {
			t.Errorf("a log line carries %q:\n%s", leak, logs)
		}
	}
}

// Staff send to their own Kindle from the book page: the same signed-in routes work for a
// manager and an admin, each result lands in the sender's own inbox, and the book page's
// ebook says whether Kindle email takes it.
func TestSendToKindleFromStaffAccounts(t *testing.T) {
	f := newKindleFixture(t)
	for _, role := range []auth.Role{auth.RoleManager, auth.RoleAdmin} {
		u, c := f.s.user(t, "staff-"+string(role), role)
		if rec := f.s.doJSON("PUT", "/api/v1/me/kindle", c, `{"kindle_email":"staff_`+string(role)+`@kindle.com"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s saves an address: HTTP %d %s", role, rec.Code, rec.Body)
		}
		if rec := f.s.do("POST", fmt.Sprintf("/api/v1/me/books/%d/send-to-kindle", f.epub.ID), c); rec.Code != http.StatusAccepted {
			t.Fatalf("%s sends: HTTP %d %s", role, rec.Code, rec.Body)
		}
		var inbox []requests.UserNotification
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && len(inbox) == 0; time.Sleep(20 * time.Millisecond) {
			inbox, _ = f.s.deps.Requests.Inbox(context.Background(), u.ID)
		}
		if len(inbox) != 1 || !strings.Contains(inbox[0].Body, "Sent “Dune” to your Kindle") || !strings.HasPrefix(inbox[0].Ref, "kindle:") {
			t.Fatalf("%s inbox = %+v", role, inbox)
		}
		if strings.Contains(inbox[0].Ref, fmt.Sprint(f.epub.ID)+":") || strings.Contains(inbox[0].Ref, "Dune") {
			t.Errorf("the reference names the book: %q", inbox[0].Ref)
		}
	}
	if n := len(f.fake.Messages()); n != 2 {
		t.Errorf("%d emails went out, want one per staff send", n)
	}

	for book, want := range map[int64]bool{f.epub.ID: true, f.azw3.ID: false} {
		rec := f.s.do("GET", fmt.Sprintf("/api/v1/books/%d", book), f.admin)
		var b books.Book
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &b) != nil || b.Ebook == nil {
			t.Fatalf("book %d: HTTP %d %s", book, rec.Code, rec.Body)
		}
		if b.Ebook.Kindle != want {
			t.Errorf("book %d (%s): ebook.kindle = %v, want %v", book, b.Ebook.Format, b.Ebook.Kindle, want)
		}
	}
}

// A failed send says so in the inbox, with the mail server's reply.
func TestSendToKindleFailureReachesTheInbox(t *testing.T) {
	f := newKindleFixture(t)
	// The fake refuses the recipient.
	f.fake.Close()
	refusing := startMailFake(t, mailtest.Options{User: "mailer", Pass: "app-pw", RejectRcpt: "reader_42@kindle.com"})
	saveSMTP(t, f.s, f.admin, refusing, "mailer", "app-pw")
	f.setAddress(t, "reader_42@kindle.com")
	if code, body := f.send(f.epub.ID); code != http.StatusAccepted {
		t.Fatalf("send: HTTP %d %s", code, body)
	}
	deadline := time.Now().Add(10 * time.Second)
	var inbox []requests.UserNotification
	for time.Now().Before(deadline) && len(inbox) == 0 {
		inbox, _ = f.s.deps.Requests.Inbox(context.Background(), f.reader.ID)
		time.Sleep(20 * time.Millisecond)
	}
	if len(inbox) != 1 || !strings.Contains(inbox[0].Body, "Couldn't send “Dune”") || !strings.Contains(inbox[0].Body, "550") {
		t.Fatalf("inbox = %+v, want the failure with the 550 reply", inbox)
	}
	if strings.Contains(inbox[0].Body, refusing.Host) {
		t.Errorf("the requester's inbox names the mail server: %q", inbox[0].Body)
	}
}

// Every refusal the button can meet, each with its own status.
func TestSendToKindleRefusals(t *testing.T) {
	f := newKindleFixture(t)
	f.setAddress(t, "reader_42@kindle.com")
	cases := []struct {
		name string
		id   int64
		want int
		says string
	}{
		{"AZW3 isn't taken by Kindle email", f.azw3.ID, http.StatusUnprocessableEntity, "AZW3"},
		{"no ebook", f.none.ID, http.StatusNotFound, "no ebook"},
		{"too large", f.large.ID, http.StatusRequestEntityTooLarge, "50 MB"},
		{"no such book", 9999, http.StatusNotFound, ""},
	}
	for _, c := range cases {
		if code, body := f.send(c.id); code != c.want || !strings.Contains(body, c.says) {
			t.Errorf("%s: HTTP %d %s, want %d mentioning %q", c.name, code, body, c.want, c.says)
		}
	}

	// A read-only account can't send (or keep an address).
	_, ro := f.s.user(t, "viewer", auth.RoleReadonly)
	if rec := f.s.do("POST", fmt.Sprintf("/api/v1/me/books/%d/send-to-kindle", f.epub.ID), ro); rec.Code != http.StatusForbidden {
		t.Errorf("read-only send: HTTP %d, want 403", rec.Code)
	}
	if rec := f.s.doJSON("PUT", "/api/v1/me/kindle", ro, `{"kindle_email":"x@kindle.com"}`); rec.Code != http.StatusForbidden {
		t.Errorf("read-only address: HTTP %d, want 403", rec.Code)
	}

	// No outgoing email: 409.
	if rec := f.s.do("DELETE", "/api/v1/settings/smtp", f.admin); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if code, body := f.send(f.epub.ID); code != http.StatusConflict {
		t.Errorf("no SMTP: HTTP %d %s, want 409", code, body)
	}

	// Books switched off: 404 everywhere.
	if err := f.s.deps.Settings.SetBool(context.Background(), settings.KeyModuleBooks, false); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.send(f.epub.ID); code != http.StatusNotFound {
		t.Errorf("books off: send HTTP %d, want 404", code)
	}
	if rec := f.s.do("GET", "/api/v1/me/kindle", f.cookie); rec.Code != http.StatusNotFound {
		t.Errorf("books off: GET HTTP %d, want 404", rec.Code)
	}
	if n := len(f.fake.Messages()); n != 0 {
		t.Errorf("%d emails went out from refused sends", n)
	}
}

// The 21st send in a day is refused with a friendly 429.
func TestSendToKindleDailyLimit(t *testing.T) {
	f := newKindleFixture(t)
	f.setAddress(t, "reader_42@kindle.com")
	now := time.Now()
	for i := 0; i < kindleDailyLimit; i++ {
		if code, body := f.send(f.epub.ID); code != http.StatusAccepted {
			t.Fatalf("send %d: HTTP %d %s", i+1, code, body)
		}
	}
	code, body := f.send(f.epub.ID)
	if code != http.StatusTooManyRequests || !strings.Contains(body, "tomorrow") {
		t.Errorf("21st send: HTTP %d %s, want 429", code, body)
	}
	// Let the background sends finish before the server goes away.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if inbox, _ := f.s.deps.Requests.Inbox(context.Background(), f.reader.ID); len(inbox) >= kindleDailyLimit {
			break
		}
	}
	if n := len(f.fake.Messages()); n != kindleDailyLimit {
		t.Errorf("%d emails sent, want %d", n, kindleDailyLimit)
	}
	// Tomorrow it's open again, and the limit is per person.
	var q kindleQuota
	for i := 0; i < kindleDailyLimit; i++ {
		q.take(1, now)
	}
	if q.take(1, now) || !q.take(2, now) || !q.take(1, now.Add(24*time.Hour)) {
		t.Error("the limit must be per person and per day")
	}
}

// Addresses: a bare address only; another domain is saved with a warning; "" removes it.
func TestKindleAddressValidation(t *testing.T) {
	f := newKindleFixture(t)
	for _, bad := range []string{"not an address", "Reader <reader@kindle.com>", "reader@kindle", "a@b.com\r\nBcc: x@y.com"} {
		if res := f.setAddress(t, bad); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: HTTP %d, want 400", bad, res.StatusCode)
		}
	}
	rec := f.s.doJSON("PUT", "/api/v1/me/kindle", f.cookie, `{"kindle_email":"reader@gmail.com"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"warning"`) {
		t.Errorf("non-Kindle domain: HTTP %d %s, want saved with a warning", rec.Code, rec.Body)
	}
	if res := f.setAddress(t, ""); res.StatusCode != http.StatusOK {
		t.Errorf("clear: HTTP %d", res.StatusCode)
	}
	if addr, _ := f.s.auth.KindleEmail(context.Background(), f.reader.ID); addr != "" {
		t.Errorf("after clearing, address = %q", addr)
	}
}
