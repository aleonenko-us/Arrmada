package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/safego"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// Display titles (Settings → Library → "Show titles as", and each title's "Display
// title…" override). The API keeps `title` canonical — the TMDB English title scripts and
// personal API keys rely on — and adds `display_title` beside it; the web app shows that.

// displayPolicy is the "Show titles as" setting in effect now.
func (a *api) displayPolicy(ctx context.Context) displaytitle.Policy {
	if a.deps.Settings == nil {
		return displaytitle.ParsePolicy("", "", "")
	}
	return settings.DisplayTitlePolicy(func(key string) string { return a.deps.Settings.Get(ctx, key, "") })
}

// displaySettings are the setting's keys as GET /settings answers them.
func (a *api) displaySettings(ctx context.Context) map[string]any {
	p := a.displayPolicy(ctx)
	return map[string]any{
		// english (TMDB's title, the default), original or language.
		settings.KeyDisplayTitleMode: string(p.Mode),
		// The language mode's language (a canonical code), "" otherwise.
		settings.KeyDisplayTitleLanguage: a.deps.Settings.Get(ctx, settings.KeyDisplayTitleLanguage, ""),
		// Works originally in these languages show their original title in any mode.
		settings.KeyDisplayTitleOriginalIn: p.OriginalIn,
	}
}

// displayUpdate is a PUT /settings body's display-title keys, checked and ready to store.
type displayUpdate struct {
	mode, language, originalIn *string
}

// parseDisplayUpdate checks a PUT's display-title keys, or says what's wrong. A language
// mode needs a language: the one sent, else the one stored.
func (a *api) parseDisplayUpdate(ctx context.Context, req *settingsUpdate) (displayUpdate, string) {
	var u displayUpdate
	if req.DisplayTitleMode != nil {
		m := strings.ToLower(strings.TrimSpace(*req.DisplayTitleMode))
		switch displaytitle.Mode(m) {
		case displaytitle.ModeEnglish, displaytitle.ModeOriginal, displaytitle.ModeLanguage:
		default:
			return u, "display_title_mode must be english, original or language"
		}
		u.mode = &m
	}
	if req.DisplayTitleLanguage != nil {
		code := ""
		if raw := strings.TrimSpace(*req.DisplayTitleLanguage); raw != "" {
			l, ok := langs.Lookup(raw)
			if !ok {
				return u, "unknown language " + raw
			}
			code = l.Code
		}
		u.language = &code
	}
	if req.DisplayTitleOriginalIn != nil {
		list, msg := parseBookLanguagesUpdate(*req.DisplayTitleOriginalIn)
		if msg != "" {
			return u, msg
		}
		u.originalIn = &list
	}
	mode := a.deps.Settings.Get(ctx, settings.KeyDisplayTitleMode, "")
	if u.mode != nil {
		mode = *u.mode
	}
	lang := a.deps.Settings.Get(ctx, settings.KeyDisplayTitleLanguage, "")
	if u.language != nil {
		lang = *u.language
	}
	if displaytitle.Mode(mode) == displaytitle.ModeLanguage && lang == "" {
		return u, "pick the language to show titles in"
	}
	return u, ""
}

// saveDisplay stores a checked display-title update and reports whether the setting in
// effect changed. ok is false when a save failed.
func (a *api) saveDisplay(ctx context.Context, u displayUpdate) (changed, ok bool) {
	before := a.displayPolicy(ctx)
	for key, v := range map[string]*string{
		settings.KeyDisplayTitleMode: u.mode, settings.KeyDisplayTitleLanguage: u.language,
		settings.KeyDisplayTitleOriginalIn: u.originalIn,
	} {
		if v != nil && *v != a.deps.Settings.Get(ctx, key, "") {
			if err := a.deps.Settings.Set(ctx, key, *v); err != nil {
				return false, false
			}
		}
	}
	after := a.displayPolicy(ctx)
	return !samePolicy(before, after), true
}

// samePolicy reports whether two settings show every title the same way.
func samePolicy(a, b displaytitle.Policy) bool {
	return a.Mode == b.Mode && a.Language == b.Language && strings.Join(a.OriginalIn, ",") == strings.Join(b.OriginalIn, ",")
}

// displayPolicyChanged puts every title's shown title right for the new setting at once
// from what's stored — a show's original title is kept already, for one — then reads the
// names still missing through the throttled titles backfill, which updates each title as
// it goes. A follow-up of the save, like titleLanguagesChanged.
func (a *api) displayPolicyChanged() {
	run := func(ctx context.Context) {
		if a.deps.Movies != nil {
			if _, err := a.deps.Movies.SyncDisplayTitles(ctx); err != nil && ctx.Err() == nil {
				a.deps.Log.Warn("movies: couldn't update the titles shown", "err", err)
			}
		}
		if a.deps.Series != nil {
			if _, err := a.deps.Series.SyncDisplayTitles(ctx); err != nil && ctx.Err() == nil {
				a.deps.Log.Warn("series: couldn't update the titles shown", "err", err)
			}
		}
		// Only after the pass above: a name the backfill stores meanwhile could otherwise
		// be overwritten by a title the pass worked out before it.
		if ctx.Err() == nil {
			a.titleLanguagesChanged()
		}
	}
	if g := a.deps.RunGroup; g != nil {
		g.Go("titles: display titles", run)
		return
	}
	safego.Go(a.deps.Log, "titles: display titles", func() { run(a.runCtx()) })
}

// titleOverrideBody is PUT /movies/{id}/display-title and /series/{id}/display-title: the
// title to show, or "" to follow the setting again.
type titleOverrideBody struct {
	Title string `json:"title"`
}

// handleSetMovieDisplayTitle sets or clears a film's display-title override and answers
// the film.
func (a *api) handleSetMovieDisplayTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var body titleOverrideBody
	if !a.decodeJSON(w, r, &body) {
		return
	}
	m, err := a.deps.Movies.SetTitleOverride(r.Context(), id, body.Title)
	switch {
	case errors.Is(err, movies.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "movie not found")
	case errors.Is(err, movies.ErrBadOverride):
		a.writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not save the display title")
	default:
		a.publishLibraryTitle("movie", m.ID)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"id": m.ID, "title": m.Title, "display_title": m.DisplayTitle, "title_override": m.TitleOverride,
		})
	}
}

// handleSetSeriesDisplayTitle is handleSetMovieDisplayTitle for a show.
func (a *api) handleSetSeriesDisplayTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var body titleOverrideBody
	if !a.decodeJSON(w, r, &body) {
		return
	}
	sr, err := a.deps.Series.SetTitleOverride(r.Context(), id, body.Title)
	switch {
	case errors.Is(err, series.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "series not found")
	case errors.Is(err, series.ErrBadOverride):
		a.writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not save the display title")
	default:
		a.publishLibraryTitle("series", sr.ID)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"id": sr.ID, "title": sr.Title, "display_title": sr.DisplayTitle, "title_override": sr.TitleOverride,
		})
	}
}

// publishLibraryTitle tells open pages a title's shown name changed ("movie.refreshed" /
// "series.refreshed", staff topics), so the grid and its page reload it.
func (a *api) publishLibraryTitle(kind string, id int64) {
	if a.deps.Bus != nil {
		a.deps.Bus.Publish(kind+".refreshed", map[string]any{"id": id})
	}
}

// handleMovieTitleChoices lists the names a film may be shown by, for the picker. It may
// ask TMDB (through the titles cache) — it runs when the picker opens, never on a page
// load.
func (a *api) handleMovieTitleChoices(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	choices, err := a.deps.Movies.TitleChoices(r.Context(), id)
	switch {
	case errors.Is(err, movies.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "movie not found")
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not list the titles")
	default:
		a.writeJSON(w, http.StatusOK, map[string]any{"choices": choices})
	}
}

// handleSeriesTitleChoices is handleMovieTitleChoices for a show.
func (a *api) handleSeriesTitleChoices(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	choices, err := a.deps.Series.TitleChoices(r.Context(), id)
	switch {
	case errors.Is(err, series.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "series not found")
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not list the titles")
	default:
		a.writeJSON(w, http.StatusOK, map[string]any{"choices": choices})
	}
}
