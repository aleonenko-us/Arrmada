package metadata

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"time"
)

// Age ratings for the browse grid's "Age rating" filter: the discovery region's own scale
// (US: G, PG, PG-13, R, NC-17; GB: U, PG, 12A, 15, 18 …), mildest first.

// DiscoverCertifications is the optional capability behind the filter.
type DiscoverCertifications interface {
	Certifications(ctx context.Context, media string) (Certifications, error)
}

// Certifications is one region's age ratings for movies or series.
type Certifications struct {
	Country string          `json:"country"`
	Items   []Certification `json:"certifications"`
}

// Certification is one age rating.
type Certification struct {
	Code    string `json:"code"`
	Meaning string `json:"meaning,omitempty"`
	Order   int    `json:"order"`
}

const certsTTL = 7 * 24 * time.Hour // TMDB's lists change about never

// Certifications lists the region's age ratings for media ("movie" or "series"), mildest
// first. A region TMDB has none for answers an empty list.
func (t *TMDB) Certifications(ctx context.Context, media string) (Certifications, error) {
	seg := "movie"
	if tvish(media) {
		seg = "tv"
	}
	country := t.regionOrDefault()
	all, err := swr(ctx, t.disk, "tmdb:certs:"+seg, certsTTL, func(ctx context.Context) (map[string][]Certification, error) {
		body, err := t.get(ctx, "/certification/"+seg+"/list", url.Values{})
		if err != nil {
			return nil, err
		}
		return parseCertifications(body)
	})
	if err != nil {
		return Certifications{}, err
	}
	items := append([]Certification(nil), all[country]...)
	if items == nil {
		items = []Certification{}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Order < items[j].Order })
	return Certifications{Country: country, Items: items}, nil
}

// parseCertifications reads /certification/{movie,tv}/list by country, keeping only codes
// the browse query would accept (certCode).
func parseCertifications(body []byte) (map[string][]Certification, error) {
	var payload struct {
		Certifications map[string][]struct {
			Certification string `json:"certification"`
			Meaning       string `json:"meaning"`
			Order         int    `json:"order"`
		} `json:"certifications"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := map[string][]Certification{}
	for country, list := range payload.Certifications {
		for _, c := range list {
			if !certCode.MatchString(c.Certification) {
				continue
			}
			out[country] = append(out[country], Certification{Code: c.Certification, Meaning: c.Meaning, Order: c.Order})
		}
	}
	return out, nil
}
