package movies

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The assertions for the rename layout matrix (rename_layout_test.go): each case takes
// away exactly what it wants, every file that leaves its place is one the preview listed
// (and the other way round), and — with ARRMADA_RENAME_LAYOUT_BASELINE naming the dump
// main made before the fix — nothing leaves that main left in place unless it's inside
// the movie's own folder. ARRMADA_RENAME_LAYOUT_TABLE=<file> writes the comparison.

func init() {
	renameLayoutPreview = func(t *testing.T, items []RenameItem) []string {
		t.Helper()
		var out []string
		for _, it := range items {
			out = append(out, it.Current)
			for _, x := range it.Extras {
				out = append(out, x.From)
			}
		}
		return out
	}
	renameLayoutCheck = func(t *testing.T, c renameCase, out renameOutcome, preview []string, items []RenameItem) {
		t.Helper()
		gone := out.Gone
		want := append([]string{}, c.gone...)
		sort.Strings(want)
		if strings.Join(gone, "\n") != strings.Join(want, "\n") {
			t.Errorf("gone:\n  %s\nwant:\n  %s", strings.Join(gone, "\n  "), strings.Join(want, "\n  "))
		}
		// Files: exactly the preview. Folders: only ones a listed file emptied.
		var files []string
		for _, p := range gone {
			if !goneDir(gone, p) {
				files = append(files, p)
			}
		}
		if strings.Join(files, "\n") != strings.Join(preview, "\n") {
			t.Errorf("the preview isn't the rename:\npreview:\n  %s\nmoved:\n  %s", strings.Join(preview, "\n  "), strings.Join(files, "\n  "))
		}
		for _, p := range gone {
			if goneDir(gone, p) && !layoutHasPrefix(preview, p+"/") {
				t.Errorf("removed folder %s, which held nothing the preview listed", p)
			}
		}
		// Arrivals: every file that arrived is one the preview named as a destination, and
		// nothing but the film's video and subtitles lands in a folder another film has a
		// record in (present or not).
		var tos []string
		for _, it := range items {
			tos = append(tos, filepath.ToSlash(it.Proposed))
			for _, x := range it.Extras {
				tos = append(tos, filepath.ToSlash(x.To))
			}
		}
		for _, a := range out.Arrived {
			if goneDir(out.Arrived, a) {
				continue
			}
			listed := false
			for _, to := range tos {
				if strings.HasSuffix(to, "/"+a) {
					listed = true
				}
			}
			if !listed {
				t.Errorf("%s arrived, which the preview didn't name", a)
			}
			if isAnyVideo(a) || subtitleLeftover[strings.ToLower(path.Ext(a))] {
				continue
			}
			for _, o := range c.others {
				if strings.HasPrefix(o.file, path.Dir(a)+"/") {
					t.Errorf("%s arrived in a folder %s %d has a file recorded in", a, o.title, o.year)
				}
			}
		}
		for _, it := range items {
			if it.Kept != "" {
				t.Logf("%s: other files stay: %s", it.Label, it.Kept)
			}
		}
	}
	renameLayoutReport = func(t *testing.T, cases []renameCase, outs map[string]renameOutcome) {
		t.Helper()
		basePath := os.Getenv("ARRMADA_RENAME_LAYOUT_BASELINE")
		if basePath == "" {
			return
		}
		b, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatal(err)
		}
		var base map[string]renameOutcome
		if err := json.Unmarshal(b, &base); err != nil {
			t.Fatal(err)
		}
		var table strings.Builder
		table.WriteString("| Case | main (cf05a0de) took away | This branch took away | Verdict | main: arrived | This branch: arrived |\n|---|---|---|---|---|---|\n")
		for _, c := range cases {
			ob, ok := base[c.name]
			if !ok {
				t.Errorf("%s: missing from main's dump", c.name)
				continue
			}
			old, now := ob.Gone, outs[c.name].Gone
			more, less := layoutMinus(now, old), layoutMinus(old, now)
			for _, p := range more {
				if !insideOwn(p, c.own) {
					t.Errorf("%s: moves %s, which main left in place and isn't inside the movie's own folder", c.name, p)
				}
			}
			verdict := "same"
			switch {
			case len(more) > 0 && len(less) > 0:
				verdict = fmt.Sprintf("more (own folder: %s); less: %s", strings.Join(more, ", "), strings.Join(less, ", "))
			case len(more) > 0:
				verdict = "more, all inside the movie's own folder: " + strings.Join(more, ", ")
			case len(less) > 0:
				verdict = "less: keeps " + strings.Join(less, ", ")
			}
			fmt.Fprintf(&table, "| %s | %s | %s | %s | %s | %s |\n", c.name, layoutShort(old), layoutShort(now), verdict, layoutShort(ob.Arrived), layoutShort(outs[c.name].Arrived))
		}
		t.Log("\n" + table.String())
		if out := os.Getenv("ARRMADA_RENAME_LAYOUT_TABLE"); out != "" {
			if err := os.WriteFile(out, []byte(table.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// goneDir reports whether p is a folder among gone: something else gone lies inside it.
func goneDir(gone []string, p string) bool { return layoutHasPrefix(gone, p+"/") }

func layoutHasPrefix(list []string, prefix string) bool {
	for _, x := range list {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}
