package arrimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

type memIndexers struct{ rows []indexer.Indexer }

func (m *memIndexers) List(context.Context) ([]indexer.Indexer, error) { return m.rows, nil }
func (m *memIndexers) Create(_ context.Context, i indexer.Indexer) (indexer.Indexer, error) {
	i.ID = int64(len(m.rows) + 1)
	m.rows = append(m.rows, i)
	return i, nil
}

type memClients struct{ rows []download.Client }

func (m *memClients) List(context.Context) ([]download.Client, error) { return m.rows, nil }
func (m *memClients) Create(_ context.Context, c download.Client) (download.Client, error) {
	c.ID = int64(len(m.rows) + 1)
	m.rows = append(m.rows, c)
	return c, nil
}

func TestSetupImportIsAdditiveIdempotentAndReadOnly(t *testing.T) {
	var writes atomic.Int32
	inner := liveArr(t, "radarr")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, err := New(Radarr, srv.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snap, err := Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Setup == nil || len(snap.Setup.Indexers) == 0 || len(snap.Setup.Clients) == 0 {
		t.Fatalf("setup not read: %+v err=%q", snap.Setup, snap.SetupErr)
	}
	ix, dc := &memIndexers{}, &memClients{}
	imp := &Importer{Indexers: ix, Clients: dc}
	opts := Options{ImportSetup: true, SkipTitles: true}

	pv, err := imp.Preview(ctx, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Setup == nil || Count(pv.Setup.Indexers, ActionAdd) == 0 {
		t.Fatalf("preview has nothing to add: %+v", pv.Setup)
	}
	if len(ix.rows) != 0 || len(dc.rows) != 0 {
		t.Fatal("preview wrote")
	}

	res, err := imp.Run(ctx, c, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	added := len(ix.rows) + len(dc.rows)
	if added == 0 || res.Setup == nil {
		t.Fatalf("nothing added: %+v", res.Setup)
	}
	for _, r := range ix.rows {
		if r.Enabled && r.APIKey == "" {
			t.Errorf("indexer %q enabled without the key the app never shared", r.Name)
		}
		if strings.Contains(r.APIKey, "*") {
			t.Errorf("masked key stored for %q", r.Name)
		}
	}
	for _, r := range dc.rows {
		if strings.Contains(r.Password, "*") {
			t.Errorf("masked password stored for %q", r.Name)
		}
	}
	// Second run adds nothing and everything it would add is reported as already here.
	if _, err := imp.Run(ctx, c, snap, opts, nil); err != nil {
		t.Fatal(err)
	}
	if len(ix.rows)+len(dc.rows) != added {
		t.Fatalf("second run added rows: %d -> %d", added, len(ix.rows)+len(dc.rows))
	}
	pv2, _ := imp.Preview(ctx, snap, opts, nil)
	if Count(pv2.Setup.Indexers, ActionAdd)+Count(pv2.Setup.Clients, ActionAdd) != 0 {
		t.Fatal("preview after import still wants to add")
	}
	if writes.Load() != 0 {
		t.Fatalf("%d non-GET requests reached the app", writes.Load())
	}
}

func TestAddressKeyIgnoresQueryAndDefaultPorts(t *testing.T) {
	if addressKey("https://Indexer.example/api/?apikey=x") != addressKey("https://indexer.example:443/api") {
		t.Fatal("addresses should compare equal")
	}
}

func TestPlanIndexerSkipsProwlarrAndBuiltIns(t *testing.T) {
	if it := planIndexer(ArrIndexer{Name: "X (Prowlarr)", Implementation: "Torznab"}, "movie"); it.Action != ActionSkip {
		t.Fatal("prowlarr-managed should skip")
	}
	if it := planIndexer(ArrIndexer{Name: "Y", Implementation: "Nyaa"}, "movie"); it.Action != ActionSkip || it.Reason == "" {
		t.Fatal("built-in should skip with a reason")
	}
}
