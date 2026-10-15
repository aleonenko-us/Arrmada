package quality

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// Shadow mode. Whichever ranking "quality_ranking" puts in effect decides every pick — v2,
// the default, or classic v1, the rollback — and the other runs alongside it on the same
// evaluations. Where the two would pick differently the service logs it and records it in
// ranking_shadow (migration 0273), one row per profile and pair of winners however often
// it recurs, and counts decisions and disagreements per day in ranking_shadow_days. Rows
// are kept as "classic picked X, v2 picked Y" whichever of the two decided, so the record
// reads the same before and after the default changed (GET /api/v1/quality/ranking-shadow,
// or ?format=csv).

// ShadowPick is the other ranking's choice on a decision: v2's while classic decides,
// classic's while v2 decides.
type ShadowPick struct {
	Mode      string  `json:"mode"`       // the ranking that ran in the shadow (RankingV1 or RankingV2)
	Winner    string  `json:"winner"`     // the release the shadow ranking would pick
	DecidedBy RankKey `json:"decided_by"` // what decided the shadow's pick (over its own runner-up)
	Differs   bool    `json:"differs"`    // the shadow picks a different release than the decision did
	// When it differs, whichever ranking decided: OverKey is the key v2's pick beats
	// classic's on, and V1Key the step classic ranking preferred its own pick on.
	OverKey RankKey `json:"over_key,omitempty"`
	V1Key   RankKey `json:"v1_key,omitempty"`
}

// shadowOf ranks a decision's eligible releases with the ranking that didn't decide it and
// says what that one would have picked. nil when nothing was eligible. The evaluations are
// shared — only the order differs — so this costs a sort, not a second evaluation.
func shadowOf(p Profile, d Decision) *ShadowPick {
	if d.Winner == nil || len(d.Eligible) == 0 {
		return nil
	}
	evs := append([]Evaluation(nil), d.Eligible...)
	sp := &ShadowPick{Mode: RankingV2}
	if d.Ranking == RankingV2 {
		sp.Mode = RankingV1
		sortV1(p, evs)
	} else {
		sortV2(p, evs)
	}
	sp.Winner, sp.DecidedBy = evs[0].Candidate.Name, decidedBy(evs)
	if evs[0].Candidate.Name != d.Winner.Candidate.Name {
		sp.Differs = true
		v1, v2 := d.Winner, &evs[0]
		if sp.Mode == RankingV1 {
			v1, v2 = &evs[0], d.Winner
		}
		_, sp.OverKey = compareV2(p, v2, v1)
		if k, _ := decidingFactor(p, *v1, *v2); k != "" {
			sp.V1Key = k
		} else {
			sp.V1Key = KeyTie
		}
	}
	return sp
}

// winners is the decision's pick and its shadow's, as classic's and v2's.
func (sp *ShadowPick) winners(d Decision) (v1, v2 string) {
	if sp.Mode == RankingV1 {
		return sp.Winner, d.Winner.Candidate.Name
	}
	return d.Winner.Candidate.Name, sp.Winner
}

// SetLogger gives the service a logger for shadow-mode disagreements.
func (s *Service) SetLogger(l *slog.Logger) { s.log = l }

// RankingMode is the ranking mode in effect ("quality_ranking": DefaultRanking, v2, unless
// "v1" was saved — the rollback).
func (s *Service) RankingMode(ctx context.Context) string {
	if s == nil || s.settings == nil { // nil-safe: a server built without quality reads the default
		return DefaultRanking
	}
	return ReadRanking(func(key, def string) string { return s.settings.Get(ctx, key, def) })
}

// shadowStats buffers the per-day decision counts in memory and writes them at most once
// a minute, so the shadow run adds no write to a decision that agrees.
type shadowStats struct {
	mu      sync.Mutex
	pending map[string][2]int // day → decisions, disagreements
	flushed time.Time
}

// shadowFlushEvery is how often buffered day counts reach the database.
const shadowFlushEvery = time.Minute

// shadowRetention is how long the record keeps days and disagreements.
const shadowRetention = 180 * 24 * time.Hour

// recordShadow notes one automatic decision that ran in shadow mode. Only decisions with
// a choice to make (two or more eligible releases) count; a disagreement is written at
// once and logged the first time it is seen. Failures are logged, never returned: the
// record must not get in the way of the pick it describes.
func (s *Service) recordShadow(ctx context.Context, ref string, d Decision) {
	if d.Shadow == nil || len(d.Eligible) < 2 || s.shadow == nil || s.repo == nil || s.repo.db == nil {
		return
	}
	now := time.Now()
	day := now.UTC().Format("2006-01-02")
	s.shadow.mu.Lock()
	if s.shadow.pending == nil {
		s.shadow.pending = map[string][2]int{}
	}
	c := s.shadow.pending[day]
	c[0]++
	if d.Shadow.Differs {
		c[1]++
	}
	s.shadow.pending[day] = c
	flush := now.Sub(s.shadow.flushed) >= shadowFlushEvery
	s.shadow.mu.Unlock()

	wctx := context.WithoutCancel(ctx)
	if d.Shadow.Differs {
		v1, v2 := d.Shadow.winners(d)
		var seen int
		err := s.repo.db.QueryRowContext(wctx, `INSERT INTO ranking_shadow
			(profile, v1_winner, v1_key, v2_winner, v2_key, candidates, first_seen, last_seen, seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT(profile, v1_winner, v2_winner) DO UPDATE SET
				v1_key = excluded.v1_key, v2_key = excluded.v2_key, candidates = excluded.candidates,
				last_seen = excluded.last_seen, seen = seen + 1
			RETURNING seen`,
			ref, v1, string(d.Shadow.V1Key), v2, string(d.Shadow.OverKey), len(d.Eligible), now.Unix(), now.Unix()).Scan(&seen)
		switch {
		case err != nil:
			s.warn("quality: couldn't record a ranking disagreement", "err", err)
		case seen == 1 && s.log != nil && d.Shadow.Mode == RankingV1:
			s.log.Info(fmt.Sprintf("quality: classic ranking would pick %s instead of %s (v2 decided by %s)", v1, v2, d.Shadow.OverKey),
				"profile", ref, "v1_reason", string(d.Shadow.V1Key), "eligible", len(d.Eligible))
		case seen == 1 && s.log != nil:
			s.log.Info(fmt.Sprintf("quality: ranking v2 would pick %s instead of %s (decided by %s)", v2, v1, d.Shadow.OverKey),
				"profile", ref, "v1_reason", string(d.Shadow.V1Key), "eligible", len(d.Eligible))
		}
	}
	if flush {
		s.flushShadow(wctx)
	}
}

func (s *Service) warn(msg string, args ...any) {
	if s.log != nil {
		s.log.Warn(msg, args...)
	}
}

// flushShadow writes the buffered day counts and prunes what's past retention.
func (s *Service) flushShadow(ctx context.Context) {
	if s.shadow == nil || s.repo == nil || s.repo.db == nil {
		return
	}
	s.shadow.mu.Lock()
	pending := s.shadow.pending
	s.shadow.pending = nil
	s.shadow.flushed = time.Now()
	s.shadow.mu.Unlock()
	for day, c := range pending {
		if _, err := s.repo.db.ExecContext(ctx, `INSERT INTO ranking_shadow_days (day, decisions, disagreements)
			VALUES (?, ?, ?) ON CONFLICT(day) DO UPDATE SET
				decisions = decisions + excluded.decisions, disagreements = disagreements + excluded.disagreements`,
			day, c[0], c[1]); err != nil {
			s.warn("quality: couldn't record ranking v2 shadow counts", "err", err)
			return
		}
	}
	cut := time.Now().Add(-shadowRetention)
	_, _ = s.repo.db.ExecContext(ctx, `DELETE FROM ranking_shadow_days WHERE day < ?`, cut.UTC().Format("2006-01-02"))
	_, _ = s.repo.db.ExecContext(ctx, `DELETE FROM ranking_shadow WHERE last_seen < ?`, cut.Unix())
}

// ShadowDay is one day's shadow-mode counts.
type ShadowDay struct {
	Day           string `json:"day"` // UTC, YYYY-MM-DD
	Decisions     int    `json:"decisions"`
	Disagreements int    `json:"disagreements"`
}

// ShadowRow is one recorded disagreement: under a profile, classic ranking's pick was
// V1Winner (preferring it on V1Key) and v2's was V2Winner (on V2Key) — one of them decided
// and the other ran in the shadow, and the row reads the same either way.
type ShadowRow struct {
	ID         int64     `json:"id"`
	Profile    string    `json:"profile"`
	V1Winner   string    `json:"v1_winner"`
	V1Key      string    `json:"v1_key"`
	V2Winner   string    `json:"v2_winner"`
	V2Key      string    `json:"v2_key"`
	Candidates int       `json:"candidates"` // eligible releases in the latest such decision
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Seen       int       `json:"seen"` // how many decisions came out this way
}

// ShadowReport is the shadow-mode record for review.
type ShadowReport struct {
	Mode          string      `json:"mode"` // the ranking mode in effect now
	Decisions     int         `json:"decisions"`
	Disagreements int         `json:"disagreements"`
	Days          []ShadowDay `json:"days"`
	Rows          []ShadowRow `json:"disagreement_rows"`
}

// ShadowReport reads the record: day counts (newest first) and the disagreements, most
// recently seen first, at most limit of them (0 = all).
func (s *Service) ShadowReport(ctx context.Context, limit int) (ShadowReport, error) {
	s.flushShadow(ctx)
	out := ShadowReport{Mode: s.RankingMode(ctx), Days: []ShadowDay{}, Rows: []ShadowRow{}}
	rows, err := s.repo.db.QueryContext(ctx, `SELECT day, decisions, disagreements FROM ranking_shadow_days ORDER BY day DESC`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d ShadowDay
		if err := rows.Scan(&d.Day, &d.Decisions, &d.Disagreements); err != nil {
			rows.Close()
			return out, err
		}
		out.Days = append(out.Days, d)
		out.Decisions += d.Decisions
		out.Disagreements += d.Disagreements
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	q := `SELECT id, profile, v1_winner, v1_key, v2_winner, v2_key, candidates, first_seen, last_seen, seen
		FROM ranking_shadow ORDER BY last_seen DESC, id DESC`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rs, err := s.repo.db.QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rs.Close()
	for rs.Next() {
		var r ShadowRow
		var first, last int64
		if err := rs.Scan(&r.ID, &r.Profile, &r.V1Winner, &r.V1Key, &r.V2Winner, &r.V2Key, &r.Candidates, &first, &last, &r.Seen); err != nil {
			return out, err
		}
		r.FirstSeen, r.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out.Rows = append(out.Rows, r)
	}
	return out, rs.Err()
}

// WriteShadowCSV writes every recorded disagreement as CSV, for review in a spreadsheet.
func (s *Service) WriteShadowCSV(ctx context.Context, w io.Writer) error {
	rep, err := s.ShadowReport(ctx, 0)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	// Classic and v2 columns whichever ranking decided: the row is the pair of picks.
	_ = cw.Write([]string{"profile", "classic_pick", "classic_preferred_on", "v2_pick", "v2_preferred_on",
		"eligible", "times_seen", "first_seen", "last_seen"})
	for _, r := range rep.Rows {
		_ = cw.Write([]string{r.Profile, r.V1Winner, r.V1Key, r.V2Winner, r.V2Key, strconv.Itoa(r.Candidates),
			strconv.Itoa(r.Seen), r.FirstSeen.Format(time.RFC3339), r.LastSeen.Format(time.RFC3339)})
	}
	cw.Flush()
	return cw.Error()
}
