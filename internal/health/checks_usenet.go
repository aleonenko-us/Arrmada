package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// Checks on a usenet client (SABnzbd). The client answering at all is DownloadClientsCheck's;
// these are what can be wrong with one that answers: nothing to download from, Arrmada's
// categories missing, folders Arrmada can't see, failure reasons it can't read — and a
// queue that isn't moving, which is almost always the provider. Each links to the client's
// card on Settings → Connections, where it's fixed.

// UsenetClient is one enabled usenet client and what it reports about itself. Err is set
// when that couldn't be read; the client check reports an unreachable client, so these say
// nothing about one.
type UsenetClient struct {
	ID    int64
	Name  string
	Facts download.UsenetFacts
	Err   error
}

func usenetFix(id int64, name string) *Fix {
	return ConnectionFix(fmt.Sprintf("download_client-%d", id), "Check "+name)
}

// UsenetClientsCheck reads each usenet client's facts every five minutes (each read is a
// few small API calls) and reports what keeps it from working.
func UsenetClientsCheck(list func(ctx context.Context) ([]UsenetClient, error)) Check {
	return Check{
		Key: "downloads.usenet", Name: "Usenet client", Category: CategoryDownloads,
		Interval: 5 * time.Minute, Timeout: 20 * time.Second,
		Run: func(ctx context.Context) []Finding {
			clients, err := list(ctx)
			if err != nil {
				return nil // a database hiccup isn't a client problem
			}
			var out []Finding
			for _, c := range clients {
				if c.Err == nil {
					out = append(out, usenetFindings(c)...)
				}
			}
			return out
		},
	}
}

func usenetFindings(c UsenetClient) []Finding {
	f := c.Facts
	key := fmt.Sprintf("downloads.usenet.%d.", c.ID)
	fix := usenetFix(c.ID, c.Name)
	var out []Finding
	add := func(what, level, msg string) {
		out = append(out, Finding{Key: key + what, Level: level, Message: msg, Fix: fix})
	}
	switch {
	case f.ServersConfigured == 0:
		add("servers", LevelError, fmt.Sprintf("%s has no news server, so nothing can download from Usenet. Add your Usenet provider in SABnzbd → Config → Servers.", c.Name))
	case f.ServersWorking == 0:
		add("servers", LevelError, fmt.Sprintf("None of %s's news servers is working%s, so nothing can download from Usenet. Check the provider's address, login and connections in SABnzbd → Config → Servers.", c.Name, firstServerError(f)))
	case f.ServersWorking < f.ServersConfigured:
		add("servers", LevelWarning, fmt.Sprintf("%d of %s's %d news servers report a problem%s. Check them in SABnzbd → Config → Servers.", f.ServersConfigured-f.ServersWorking, c.Name, f.ServersConfigured, firstServerError(f)))
	}
	if len(f.Missing) > 0 {
		cats := "categories"
		if len(f.Missing) == 1 {
			cats = "category"
		}
		add("categories", LevelWarning, fmt.Sprintf("%s is missing Arrmada's %s %s, so those downloads can't be sent to it. Use \"Add missing categories\" on its card.",
			c.Name, strings.Join(f.Missing, ", "), cats))
	}
	if f.CompleteFolder.Remote != "" && !f.CompleteFolder.Visible {
		add("complete", LevelWarning, fmt.Sprintf("Arrmada can't see %s's completed folder %s (it looked at %s), so finished downloads won't import. Mount it into Arrmada or add a path mapping on its card.",
			c.Name, f.CompleteFolder.Remote, f.CompleteFolder.Local))
	}
	for _, cat := range f.Categories {
		if cat.Folder != nil && !cat.Folder.Visible {
			add("category."+cat.Name, LevelWarning, fmt.Sprintf("Arrmada can't see the folder %s files %s downloads in (%s), so they won't import. Mount it into Arrmada or add a path mapping on its card.",
				c.Name, cat.Name, cat.Folder.Remote))
		}
	}
	if f.NotEnglish {
		add("language", LevelWarning, fmt.Sprintf("%s's language is %q. Its failure reasons are translated, so Arrmada can't tell a full disk from a bad release and treats every failure as a bad release. Set it to English in SABnzbd → Config → General.",
			c.Name, f.Language))
	}
	return out
}

// firstServerError is the first news server's error, for the message: " (Provider: 502
// Authentication failed)", or "" when none said.
func firstServerError(f download.UsenetFacts) string {
	for _, s := range f.Servers {
		if !s.OK && s.Error != "" {
			msg := s.Error
			if r := []rune(msg); len(r) > 120 {
				msg = string(r[:117]) + "…"
			}
			return fmt.Sprintf(" (%s: %s)", s.Name, msg)
		}
	}
	return ""
}

// UsenetIdle is a usenet client that has had downloads waiting and fetched nothing since.
type UsenetIdle struct {
	ClientID   int64
	ClientName string
	Since      time.Time
	Jobs       int
}

// UsenetProgressCheck warns about a usenet client that has downloaded nothing for a while
// although downloads are waiting (automation.UsenetIdle decides when). Arrmada never
// replaces a usenet release for that — it's nearly always the provider or the account,
// which every release would hit — so this is what says it.
func UsenetProgressCheck(idle func(ctx context.Context) []UsenetIdle, now func() time.Time) Check {
	if now == nil {
		now = time.Now
	}
	return Check{
		Key: "downloads.usenet.progress", Name: "Usenet downloads moving", Category: CategoryDownloads,
		Run: func(ctx context.Context) []Finding {
			var out []Finding
			for _, u := range idle(ctx) {
				waiting := "a download is"
				if u.Jobs > 1 {
					waiting = fmt.Sprintf("%d downloads are", u.Jobs)
				}
				out = append(out, Finding{
					Key: fmt.Sprintf("downloads.usenet.%d.progress", u.ClientID), Level: LevelWarning, Fix: usenetFix(u.ClientID, u.ClientName),
					Message: fmt.Sprintf("%s hasn't downloaded anything for %s although %s waiting — check your Usenet provider (its servers, login and account) in SABnzbd → Config → Servers.",
						u.ClientName, idleSpan(now().Sub(u.Since)), waiting),
				})
			}
			return out
		},
	}
}

// idleSpan is "30 minutes", "2 hours".
func idleSpan(d time.Duration) string {
	if d < 2*time.Hour {
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	return fmt.Sprintf("%d hours", int(d/time.Hour))
}

// UsenetLeftovers is a usenet client whose finished downloads Arrmada keeps leaving in
// place after archiving them: Count in a row, the latest Name and why (Detail).
type UsenetLeftovers struct {
	ClientID   int64
	ClientName string
	Count      int
	Name       string
	Detail     string
}

// UsenetLeftoversCheck warns when finished usenet downloads keep being left behind: their
// folders are outside where Arrmada may delete (its downloads folder and SABnzbd's
// completed folder, as Arrmada sees them), so they pile up. One is left alone (a job moved
// by hand); it takes a repeat to report.
func UsenetLeftoversCheck(list func(ctx context.Context) []UsenetLeftovers) Check {
	return Check{
		Key: "downloads.usenet.leftovers", Name: "Usenet clean-up", Category: CategoryDownloads,
		Run: func(ctx context.Context) []Finding {
			var out []Finding
			for _, l := range list(ctx) {
				detail := l.Detail
				if r := []rune(detail); len(r) > 200 {
					detail = string(r[:197]) + "…"
				}
				out = append(out, Finding{
					Key: fmt.Sprintf("downloads.usenet.%d.leftovers", l.ClientID), Level: LevelWarning, Fix: usenetFix(l.ClientID, l.ClientName),
					Message: fmt.Sprintf("Arrmada imported %d downloads from %s but had to leave their folders in place, most recently %s (%s). Map SABnzbd's completed folder into Arrmada, inside its downloads folder or with a path mapping on the card, so it can tidy up after imports.",
						l.Count, l.ClientName, l.Name, detail),
				})
			}
			return out
		},
	}
}
