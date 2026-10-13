package convert

import (
	"context"
	"strings"
	"testing"
)

// "Try these again" on a Problems group clears that kind and nothing else, in one step.
func TestClearSkipKindOnlyThatKind(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	for i := int64(1); i <= 300; i++ {
		s.skips.record(ctx, movieKey(i), SkipHardlinked, "seeding")
	}
	s.skips.record(ctx, movieKey(301), SkipNotSmaller, "only 5% smaller")
	s.skips.record(ctx, movieKey(302), SkipNoScratch, "needs 90 GB")

	if n := s.ClearSkipKind(ctx, SkipHardlinked); n != 300 {
		t.Fatalf("cleared %d, want the 300 seeding files", n)
	}
	list, err := s.skips.list(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, sk := range list {
		kinds[sk.Kind]++
	}
	if kinds[SkipHardlinked] != 0 || kinds[SkipNotSmaller] != 1 || kinds[SkipNoScratch] != 1 {
		t.Fatalf("left %v, want only the other two kinds", kinds)
	}
	if n := s.ClearSkipKind(ctx, "no_such_kind"); n != 0 {
		t.Errorf("an unknown kind cleared %d", n)
	}
}

// With files held back in Problems the status never says "everything's converted" plainly:
// it says how many are there.
func TestStatusDoneCountsProblems(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	set(t, s, map[string]string{keyAuto: "true"})
	indexMovie(t, s, 1, "Remux", film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
	indexMovie(t, s, 2, "BluRay", film("h264", 1920, 1080, 12000, aud("ac3", "eng", 6)))

	s.skips.record(ctx, movieKey(1), SkipNotSmaller, "only 5% smaller") // permanent
	if st := s.Status(ctx); st.State == "done" || st.Remaining != 1 || st.Problems != 1 {
		t.Fatalf("one left, one in Problems: %s %q remaining %d problems %d", st.State, st.Message, st.Remaining, st.Problems)
	}

	for i := 0; i < maxFailures; i++ {
		s.failures.recordFailure(ctx, movieKey(2), "encode failed")
	}
	st := s.Status(ctx)
	if st.State != "done" || st.Remaining != 0 || st.Problems != 2 {
		t.Fatalf("all held back: %s remaining %d problems %d", st.State, st.Remaining, st.Problems)
	}
	if strings.Contains(st.Message, "Everything's converted") || !strings.Contains(st.Message, "2 files") || !strings.Contains(st.Message, "Problems") {
		t.Fatalf("message %q should count the files in Problems", st.Message)
	}

	// Cleared, they're back in the queue; with nothing left at all it's plainly done.
	_ = s.ClearSkip(ctx, "")
	_ = s.ClearBlocklist(ctx, "")
	if st := s.Status(ctx); st.Problems != 0 || st.Remaining != 2 {
		t.Fatalf("after clearing: remaining %d problems %d", st.Remaining, st.Problems)
	}
	s2 := newTestService(t)
	set(t, s2, map[string]string{keyAuto: "true"})
	if st := s2.Status(ctx); st.State != "done" || !strings.HasPrefix(st.Message, "Everything's converted") {
		t.Fatalf("an empty library: %s %q", st.State, st.Message)
	}
}
