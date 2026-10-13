package applog

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live mode polls with the last cursor it saw and gets only what came after — not the
// whole window again — and a cursor from before a restart starts over instead of
// waiting forever for numbers the new run hasn't reached.
func TestLogsAfterSeqReturnsOnlyNewer(t *testing.T) {
	ring := NewRing(100)
	log := slog.New(NewHandler(slog.NewTextHandler(discard{}, nil), ring))
	for i := 0; i < 5; i++ {
		log.Info("first batch", "i", i)
	}
	p := ring.Read(Filter{})
	if len(p.Entries) != 5 || p.LastSeq != 5 || p.Reset {
		t.Fatalf("first read = %d entries, last %d, reset %v", len(p.Entries), p.LastSeq, p.Reset)
	}
	log.Info("new one")
	log.Debug("below the filter") // not captured at info: the base handler drops it
	log.Warn("new two")

	p = ring.Read(Filter{AfterSeq: 5})
	if len(p.Entries) != 2 || p.Entries[0].Message != "new one" || p.Entries[1].Message != "new two" || p.LastSeq != 7 {
		t.Fatalf("incremental read = %+v (last %d)", p.Entries, p.LastSeq)
	}
	// Nothing new: an empty answer, and the cursor stays.
	if p = ring.Read(Filter{AfterSeq: 7}); len(p.Entries) != 0 || p.LastSeq != 7 {
		t.Errorf("idle read = %+v", p)
	}
	// The cursor moves past lines the filter hid, so they aren't rescanned every poll.
	if p = ring.Read(Filter{AfterSeq: 5, Min: slog.LevelWarn}); len(p.Entries) != 1 || p.LastSeq != 7 {
		t.Errorf("filtered incremental read = %+v", p)
	}
	// A cursor ahead of the ring is from an earlier run.
	if p = ring.Read(Filter{AfterSeq: 999, Limit: 3}); !p.Reset || len(p.Entries) != 3 {
		t.Errorf("stale cursor = %+v, want a reset with the newest 3", p)
	}

	// Once the ring has wrapped, the walk back still stops at the cursor.
	small := NewRing(100)
	for i := 0; i < 250; i++ {
		small.add(Entry{TimeMS: int64(i + 1), Level: "INFO", Message: "x"})
	}
	if p = small.Read(Filter{AfterSeq: 240}); len(p.Entries) != 10 || p.Entries[0].Seq != 241 || p.Entries[9].Seq != 250 {
		t.Errorf("after wrap = %d entries (%v…)", len(p.Entries), p.Entries)
	}
	// A cursor older than the ring holds returns everything held.
	if p = small.Read(Filter{AfterSeq: 10}); len(p.Entries) != 100 || p.Entries[0].Seq != 151 {
		t.Errorf("cursor older than the ring = %d entries", len(p.Entries))
	}
}

// Each service gets a logger tagged with its module in main; the tag becomes Entry.Module
// (not an attr), and the module filter keeps one module's lines.
func TestLogsModuleFilter(t *testing.T) {
	ring := NewRing(100)
	root := slog.New(NewHandler(slog.NewTextHandler(discard{}, nil), ring))
	root.With("module", "convert").Info("encode started", "file", "a.mkv")
	root.With("module", "indexer").Info("search", "q", "dune")
	root.Info("core line")
	root.With("module", "convert").With("job", 4).Warn("encode slow")

	got := ring.Read(Filter{Module: "convert"}).Entries
	if len(got) != 2 || got[0].Message != "encode started" || got[1].Message != "encode slow" {
		t.Fatalf("convert = %+v", got)
	}
	for _, e := range got {
		if e.Module != "convert" || strings.Contains(e.Attrs, "module=") {
			t.Errorf("entry %+v: module should be a field, not an attr", e)
		}
	}
	if !strings.Contains(got[1].Attrs, "job=4") {
		t.Errorf("later attrs lost: %q", got[1].Attrs)
	}
	if all := ring.Read(Filter{}).Entries; len(all) != 4 || all[2].Module != "" {
		t.Errorf("no module filter = %+v", all)
	}
}

// Sequence numbers belong to one run: they're not written to disk, and lines read back
// at startup are numbered afresh. Flush makes a copy taken right after a line include it.
func TestSeqNotPersistedAndFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "arrmada.log.jsonl")
	ring := NewRing(100)
	stop, err := ring.Persist(path)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(NewHandler(slog.NewTextHandler(discard{}, nil), ring))
	log.With("module", "books").Info("persist me")
	ring.Flush(5 * time.Second)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &e); err != nil {
		t.Fatalf("file %q: %v", raw, err)
	}
	if e.Message != "persist me" || e.Seq != 0 || e.Module != "books" || strings.Contains(string(raw), `"seq"`) {
		t.Errorf("on disk: %s", raw)
	}
	if files := ring.Files(); len(files) != 1 || files[0] != path {
		t.Errorf("Files = %v", files)
	}
	stop()

	again := NewRing(100)
	again.add(Entry{TimeMS: 1, Level: "INFO", Message: "already here"})
	again.Restore(path)
	got := again.Read(Filter{}).Entries
	if len(got) != 2 || got[1].Message != "persist me" || got[1].Seq != 2 || got[1].Module != "books" {
		t.Errorf("restored = %+v", got)
	}
	if NewRing(100).Files() != nil {
		t.Error("an unpersisted ring has no files")
	}
}
