package archtest

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAudiotagWritesOnlyInStaging (MUS-33): tags are only ever written into a staging
// copy, never into a library file or a download that may still be seeding. So only the
// staging package (internal/music/stage) may call audiotag.Write or build a Tagger
// (audiotag.New), whose Write it could call; everything else may only read tags or build
// Tags values. Tests may do what they like.
func TestAudiotagWritesOnlyInStaging(t *testing.T) {
	root := repoRoot(t)
	const pkg = "github.com/tristenlammi/arrmada/internal/audiotag"
	allowed := []string{"internal/audiotag/", "internal/music/stage/"}
	for _, dir := range sourceDirs(t, root) {
		rel := filepath.ToSlash(strings.TrimPrefix(dir, root+string(filepath.Separator))) + "/"
		skip := false
		for _, a := range allowed {
			if strings.HasPrefix(rel, a) {
				skip = true
			}
		}
		if skip {
			continue
		}
		fset, files := parseDir(t, dir)
		for _, f := range files {
			name := ""
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) != pkg {
					continue
				}
				name = "audiotag"
				if imp.Name != nil {
					name = imp.Name.Name
				}
			}
			if name == "" {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && (sel.Sel.Name == "Write" || sel.Sel.Name == "New") {
					t.Errorf("%s: %s.%s — tags are written only in a staging copy, through internal/music/stage",
						fset.Position(sel.Pos()), name, sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// TestThirdPartyNoticesShip: the music import links internal/audiotag, and with it the
// LGPL-2.1 TagLib (for Ogg), so THIRD_PARTY_NOTICES.md must ship in the image: the
// Dockerfile copies it, and .dockerignore (which drops *.md) lets it through.
func TestThirdPartyNoticesShip(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "THIRD_PARTY_NOTICES.md")); err != nil {
		t.Fatal(err)
	}
	docker, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(docker), "COPY THIRD_PARTY_NOTICES.md LICENSE /usr/share/doc/arrmada/") {
		t.Error("the Dockerfile doesn't copy THIRD_PARTY_NOTICES.md (and LICENSE) into the image")
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ignore), "!THIRD_PARTY_NOTICES.md") {
		t.Error(".dockerignore drops *.md without letting THIRD_PARTY_NOTICES.md through")
	}
}
