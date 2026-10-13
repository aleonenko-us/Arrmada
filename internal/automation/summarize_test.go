package automation

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// The interactive list's summary names the streaming service a WEB release came from.
func TestSummarizeNamesStreamingService(t *testing.T) {
	if got := summarize(parser.Parse("Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP")); got != "1080p · WEB-DL · AMZN" {
		t.Fatalf("summary = %q", got)
	}
	if got := summarize(parser.Parse("Movie.2020.1080p.BluRay.x264-GRP")); got != "1080p · BluRay" {
		t.Fatalf("summary = %q", got)
	}
}
