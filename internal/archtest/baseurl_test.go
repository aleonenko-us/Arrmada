package archtest

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBaseURLSetting: Arrmada is served at the root of its own hostname or port. The
// ARRMADA_BASE_URL sub-path option was removed (CFG-08) and FE-29 settled on keeping it
// removed rather than finishing it: a sub-path would have to reach ~500 hard-coded
// /api/v1 paths in the web app, the websocket, the service worker and manifest scope,
// OPDS absolute links and its OpenSearch template (which e-reader apps store), the Plex
// sign-in return URL, push and notification links — and anything missed is a blank page
// or a dead link for one feature only. So the name may appear only where a stale value is
// noticed and explained to someone upgrading: the startup warning, update.sh's .env
// note, and the README. Not in compose, the Dockerfile, install.sh, .env.example, an
// Unraid template, the web app, or as a server option.
func TestNoBaseURLSetting(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		"internal/config/config.go": true, // keeps a leftover value only to warn about it
		"cmd/arrmada/main.go":       true, // the startup warning
		"update.sh":                 true, // "ARRMADA_BASE_URL in .env is ignored"
		"README.md":                 true, // says sub-paths aren't supported
		"ROADMAP.md":                true,
	}
	skipDirs := map[string]bool{".git": true, ".claude": true, "node_modules": true, "dist": true, "docs": true, "testdata": true}
	text := map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".mjs": true, ".json": true, ".webmanifest": true,
		".yml": true, ".yaml": true, ".sh": true, ".xml": true, ".html": true, ".md": true, ".example": true, ".conf": true, ".env": true}
	ignored := gitIgnoredFiles(t, root)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		name := d.Name()
		if ignored[rel] {
			return nil // a local, git-ignored file (an audit report, a review page) isn't the app
		}
		if allowed[rel] || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") {
			return nil
		}
		if !text[filepath.Ext(name)] && !strings.HasPrefix(name, "Dockerfile") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "ARRMADA_BASE_URL") {
			t.Errorf("%s mentions ARRMADA_BASE_URL — sub-path hosting isn't supported; serve Arrmada at the root of its own hostname", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// gitIgnoredFiles lists the files git ignores under root, so a local file that never
// ships (an audit report at the repo root, say) can't fail the check. safe.directory lets
// it read a checkout owned by another user (a Docker test run). Without git it
// returns nothing and every file is checked.
func gitIgnoredFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	out, err := exec.Command("git", "-c", "safe.directory=*", "-C", root, "ls-files", "--others", "--ignored", "--exclude-standard").Output()
	if err != nil {
		return nil
	}
	files := map[string]bool{}
	for _, f := range strings.Split(string(out), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files[f] = true
		}
	}
	return files
}
