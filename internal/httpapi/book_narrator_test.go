package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
)

// The narrator is typed on the Books page: alone on the metadata route (the rest of
// the book untouched, no "corrected by hand" event), with a full correction, or on an
// audiobook version.
func TestBookNarratorEdits(t *testing.T) {
	s := bookCardServer(t)
	ctx := context.Background()
	_, cookie := s.user(t, "manager@example.com", auth.RoleManager)
	b, err := books.NewRepo(s.st.DB()).Create(ctx, books.Book{OLKey: "hc:7", Title: "Dungeon Crawler Carl", Author: "Matt Dinniman"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/books/%d/metadata", b.ID)

	if rec := s.doBody("PUT", path, `{"narrator":"  Jeff  Hays "}`, cookie); rec.Code != http.StatusOK {
		t.Fatalf("narrator only: HTTP %d %s", rec.Code, rec.Body)
	}
	got, _ := s.deps.Books.Get(ctx, b.ID)
	if got.AudiobookNarrator != "Jeff Hays" || got.Title != "Dungeon Crawler Carl" || got.Author != "Matt Dinniman" {
		t.Fatalf("after a narrator-only edit: %+v", got)
	}
	if evs, _ := s.deps.Books.Events(ctx, b.ID, 10); len(evs) != 0 {
		t.Fatalf("a narrator edit logged %+v", evs)
	}
	if rec := s.doBody("PUT", path, `{"narrator":"`+strings.Repeat("x", 201)+`"}`, cookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("a 201-character narrator: HTTP %d", rec.Code)
	}
	if rec := s.doBody("PUT", path, `{"title":"Dungeon Crawler Carl","author":"Matt Dinniman","narrator":""}`, cookie); rec.Code != http.StatusOK {
		t.Fatalf("correction with narrator: HTTP %d %s", rec.Code, rec.Body)
	}
	if got, _ := s.deps.Books.Get(ctx, b.ID); got.AudiobookNarrator != "" {
		t.Fatalf("narrator not cleared: %q", got.AudiobookNarrator)
	}
	if rec := s.doBody("PUT", path, `{"overview":"no title"}`, cookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("a correction without title and author: HTTP %d", rec.Code)
	}

	v, err := s.deps.Books.AddAudioVersion(ctx, b.ID, "Full cast", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	rec := s.doBody("PUT", fmt.Sprintf("/api/v1/books/%d/audio-versions/%d", b.ID, v.ID), `{"narrator":"Soundbooth Theater"}`, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"narrator":"Soundbooth Theater"`) {
		t.Fatalf("version narrator: HTTP %d %s", rec.Code, rec.Body)
	}
	if got, _ := s.deps.Books.GetAudioVersion(ctx, b.ID, v.ID); got.Label != "Full cast" || got.Narrator != "Soundbooth Theater" {
		t.Fatalf("version after a narrator edit: %+v", got)
	}
}
