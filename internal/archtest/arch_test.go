package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot is the module root, found from this file's location so the test works from
// any working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("can't locate the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// parseDir parses every non-test .go file directly in dir.
func parseDir(t *testing.T, dir string) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files[path] = f
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s — has the package moved?", dir)
	}
	return fset, files
}

// sourceDirs lists every directory under internal/ and cmd/ that holds Go files.
func sourceDirs(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if name := d.Name(); name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
					dirs = append(dirs, p)
					break
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	return dirs
}

// TestTransactionsGoThroughWithTx: multi-step writes use store.WithTx, never a hand-rolled
// BeginTx. WithTx retries a busy BEGIN and rolls back on an error or a panic; the
// boilerplate it replaced forgot one or the other often enough to be worth a rule. Only
// internal/store (which implements WithTx and runs the migrations) may call BeginTx.
func TestTransactionsGoThroughWithTx(t *testing.T) {
	root := repoRoot(t)
	allowed := filepath.Join(root, "internal", "store")
	for _, dir := range sourceDirs(t, root) {
		if dir == allowed {
			continue
		}
		fset, files := parseDir(t, dir)
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "BeginTx" || sel.Sel.Name == "Begin") {
					pos := fset.Position(call.Pos())
					t.Errorf("%s:%d: %s outside internal/store — use store.WithTx so the transaction is retried when busy and always rolled back",
						filepath.ToSlash(strings.TrimPrefix(pos.Filename, root+string(filepath.Separator))), pos.Line, sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// settingsSQL matches a statement that reads or writes the settings table; sqlVerb keeps
// prose such as "couldn't update settings" in a message from counting as SQL.
var (
	settingsSQL = regexp.MustCompile(`(?i)\b(FROM|INTO|JOIN)\s+settings\b|\bUPDATE\s+settings\s+SET\b`)
	sqlVerb     = regexp.MustCompile(`(?i)\b(SELECT|INSERT|UPDATE|DELETE|REPLACE)\b`)
)

// TestSettingsOnlyThroughService: settings are served from memory by settings.Service,
// which writes them through to the table. SQL that reads the table elsewhere can see a
// different value from everyone else, and SQL that writes it changes the database
// without changing what the app runs on. Only internal/settings may touch the table
// (migrations are .sql files, not Go, so they aren't walked).
func TestSettingsOnlyThroughService(t *testing.T) {
	root := repoRoot(t)
	allowed := filepath.Join(root, "internal", "settings")
	for _, dir := range sourceDirs(t, root) {
		if dir == allowed {
			continue
		}
		fset, files := parseDir(t, dir)
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !settingsSQL.MatchString(lit.Value) || !sqlVerb.MatchString(lit.Value) {
					return true
				}
				pos := fset.Position(lit.Pos())
				t.Errorf("%s:%d: SQL on the settings table outside internal/settings — use settings.Service (Get/Set/Lookup) so the in-memory copy stays the one truth",
					filepath.ToSlash(strings.TrimPrefix(pos.Filename, root+string(filepath.Separator))), pos.Line)
				return true
			})
		}
	}
}

// TestNoNakedGoroutines: HTTP handlers and main start background work through the run
// group (a.bg, grp.Go, grp.Loop), never a bare go statement. A bare one escapes both the
// panic recovery — one nil dereference in it restarts the whole app mid-encode — and the
// shutdown that cancels and waits for background work.
func TestNoNakedGoroutines(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"internal/httpapi", "cmd/arrmada"} {
		fset, files := parseDir(t, filepath.Join(root, filepath.FromSlash(rel)))
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				if g, ok := n.(*ast.GoStmt); ok {
					pos := fset.Position(g.Pos())
					t.Errorf("%s:%d: go statement in %s — use a.bg (handlers) or grp.Go/grp.Loop (main) so a panic is contained and shutdown can stop it",
						filepath.ToSlash(strings.TrimPrefix(pos.Filename, root+string(filepath.Separator))), pos.Line, rel)
				}
				return true
			})
		}
	}
}

// busReceiver matches the expression a Subscribe is called on when it is the event bus:
// bus, s.bus, c.bus, a.deps.Bus and the like. Other Subscribe methods (Web Push's) hang
// off differently named receivers.
var busReceiver = regexp.MustCompile(`(?i)(^|\.)bus$`)

// TestBusSubscribeOnlyInUIAndAlerts: the event bus drops events when a subscriber is busy
// and forgets them on a restart, so it may only feed the UI (internal/realtime) and admin
// alerts (internal/notify). Work that has to happen after an event — reindexing, a
// requester's "ready" message, cache refreshes — goes through a direct call or an
// internal/outbox row instead.
func TestBusSubscribeOnlyInUIAndAlerts(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		filepath.Join(root, "internal", "realtime"): true,
		filepath.Join(root, "internal", "notify"):   true,
		filepath.Join(root, "internal", "eventbus"): true,
	}
	for _, dir := range sourceDirs(t, root) {
		if allowed[dir] {
			continue
		}
		fset, files := parseDir(t, dir)
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Subscribe" {
					return true
				}
				// The bus by name, or by its call shape: topics are passed as string
				// literals, which no other Subscribe in the tree takes first.
				topicArg := false
				if len(call.Args) > 0 {
					lit, isLit := call.Args[0].(*ast.BasicLit)
					topicArg = isLit && lit.Kind == token.STRING
				}
				if !busReceiver.MatchString(exprString(sel.X)) && !topicArg {
					return true
				}
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d: event bus Subscribe outside internal/realtime and internal/notify — the bus drops events; use an internal/outbox consumer or a direct call for work that must happen",
					filepath.ToSlash(strings.TrimPrefix(pos.Filename, root+string(filepath.Separator))), pos.Line)
				return true
			})
		}
	}
}

// exprString renders an identifier or selector chain ("a.deps.Bus"); anything else is "".
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if x := exprString(v.X); x != "" {
			return x + "." + v.Sel.Name
		}
		return v.Sel.Name
	}
	return ""
}

// TestNoBackgroundContextInHandlers: HTTP handlers hand work to the job runner (a.submit),
// which runs it on the app's run context, so shutdown cancels it. A context.Background()
// in a handler is work nothing can stop or see. The allowlist is the one fallback that
// must remain: runCtx, for when no run group is wired (tests, tools).
func TestNoBackgroundContextInHandlers(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]int{"background.go": 1}
	fset, files := parseDir(t, filepath.Join(root, "internal", "httpapi"))
	seen := map[string]int{}
	for path, f := range files {
		base := filepath.Base(path)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Background" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "context" {
				return true
			}
			seen[base]++
			if seen[base] > allowed[base] {
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d: context.Background() in a handler — use a.submit (or a.runCtx for work that must outlive the request) so shutdown can stop it",
					filepath.ToSlash(strings.TrimPrefix(pos.Filename, root+string(filepath.Separator))), pos.Line)
			}
			return true
		})
	}
}

// TestAudiotagNotLinkedYet: nothing imports internal/audiotag until the music import's
// tagging step (Phase 2) does, deliberately. Until then the binary links neither its
// LGPL-2.1 TagLib backend nor anything else from it, and stays MIT/Apache, as
// THIRD_PARTY_NOTICES.md says. Wiring it in means updating that file and this test.
func TestAudiotagNotLinkedYet(t *testing.T) {
	root := repoRoot(t)
	const pkg = `"github.com/tristenlammi/arrmada/internal/audiotag"`
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != root && (name == "node_modules" || name == "web" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		if strings.HasPrefix(rel, "internal/audiotag/") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if perr != nil {
			return nil
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == pkg {
				t.Errorf("%s imports internal/audiotag — it would link LGPL code into the binary; update THIRD_PARTY_NOTICES.md (and ship it) first", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOneMusicBrainzClient: only main builds a MusicBrainz client. Every client shares the
// one rate limiter, but a client built elsewhere would carry its own config and logger and
// invite a way round the shared cache. Tests may build their own.
func TestOneMusicBrainzClient(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != root && (name == "node_modules" || name == "web" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		if strings.HasPrefix(rel, "cmd/arrmada/") || rel == "internal/metadata/musicbrainz.go" {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if name == "NewMusicBrainz" {
				t.Errorf("%s:%d: NewMusicBrainz outside cmd/arrmada — take the one client main builds", rel, fset.Position(call.Pos()).Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestToolsThroughRunner: the media packages start ffmpeg, ffprobe and the other helper
// tools through internal/toolrun, never exec.Command or exec.CommandContext. A bare one
// runs at full CPU and disk priority next to someone's Plex stream, can't be paused with the
// rest of a conversion, and leaves anything it spawned running when it is cancelled.
// exec.LookPath (finding a tool) is fine.
func TestToolsThroughRunner(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"internal/convert", "internal/subtitles", "internal/mediainfo", "internal/extract", "internal/kepub", "internal/audioserver"} {
		fset, files := parseDir(t, filepath.Join(root, filepath.FromSlash(rel)))
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "exec" {
					return true
				}
				pos := fset.Position(sel.Pos())
				t.Errorf("%s:%d: exec.%s in %s — start tools with toolrun.Command (or s.tool in convert) so they are niced, pausable and killed as a group",
					filepath.ToSlash(strings.TrimPrefix(pos.Filename, root+string(filepath.Separator))), pos.Line, sel.Sel.Name, rel)
				return true
			})
		}
	}
}
