package tautulli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// History must ask for one row per session: Tautulli's default grouping merges a user's
// consecutive sittings into one row that can't be matched against live-recorded plays.
func TestHistoryAsksForUngroupedRows(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("grouping") != "0" {
			t.Errorf("get_history without grouping=0: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":1,"data":[
			{"user_id":7,"user":"amy","title":"Dune","media_type":"movie","rating_key":"100","started":1000,"stopped":4000,"duration":2900}]}}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	var got []Row
	total := 0
	if err := c.History(context.Background(), func(p Page) error { got, total = append(got, p.Rows...), p.Total; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RatingKey != "100" || got[0].DurationSec != 2900 || total != 1 {
		t.Fatalf("rows = %+v, total %d", got, total)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("made %d requests, want 2", len(queries))
	}
}

// Tautulli sends ids as numbers in some versions and as strings in others: a play's
// rating key (and the show's, and the user id) read the same either way.
func TestHistoryReadsNumericAndStringIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":3,"data":[
			{"user_id":7,"user":"amy","title":"Dune","media_type":"movie","rating_key":100,"started":1000,"stopped":4000,"duration":2900},
			{"user_id":"7","user":"amy","title":"Pilot","media_type":"episode","rating_key":"8812","grandparent_rating_key":"8800","started":"5000","stopped":"8000","duration":"2900"},
			{"user_id":7,"user":"amy","title":"Heat","media_type":"movie","rating_key":null,"started":9000,"stopped":12000,"duration":2900}]}}}`))
	}))
	defer srv.Close()
	var got []Row
	if err := New(srv.URL, "k").History(context.Background(), func(p Page) error { got = append(got, p.Rows...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %+v", got)
	}
	if got[0].RatingKey != "100" || got[0].UserID != 7 {
		t.Errorf("numeric ids: %+v", got[0])
	}
	if got[1].RatingKey != "8812" || got[1].GrandparentRatingKey != "8800" || got[1].UserID != 7 || got[1].Started != 5000 || got[1].DurationSec != 2900 {
		t.Errorf("string ids: %+v", got[1])
	}
	if got[2].RatingKey != "" {
		t.Errorf("a missing key reads as empty, got %q", got[2].RatingKey)
	}
}

// An episode's show poster and rating key come through, so imported episodes group with
// live ones by Plex identity.
func TestHistoryMapsGrandparentFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"result":"success","data":{"recordsFiltered":1,"data":[
			{"user_id":7,"user":"amy","title":"Pilot","grandparent_title":"Severance","media_type":"episode","rating_key":"8812",
			 "grandparent_rating_key":8800,"grandparent_thumb":"/library/metadata/8800/thumb/1","started":1000,"stopped":4000,"duration":2900}]}}}`))
	}))
	defer srv.Close()
	var got []Row
	if err := New(srv.URL, "k").History(context.Background(), func(p Page) error { got = append(got, p.Rows...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GrandparentRatingKey != "8800" || got[0].GrandparentThumb != "/library/metadata/8800/thumb/1" {
		t.Fatalf("rows = %+v", got)
	}
}
