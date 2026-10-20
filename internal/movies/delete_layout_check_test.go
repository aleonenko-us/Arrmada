package movies

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The assertions for the layout matrix (delete_layout_test.go): each case removes exactly
// what it wants, the delete dialog's preview lists exactly what the delete removed, and —
// with ARRMADA_LAYOUT_BASELINE naming v0.3.2's dump — nothing goes that v0.3.2 kept unless
// it's inside the movie's own folder. ARRMADA_LAYOUT_TABLE=<file> writes the comparison.

// lastLeftovers are the leftovers the last preview listed, which the delete sends back.
var lastLeftovers []string

func init() {
	layoutDelete = func(s *Service, id int64) error {
		return s.DeleteWith(context.Background(), id, DeleteOptions{Files: true, Leftovers: lastLeftovers})
	}
	layoutPreview = func(t *testing.T, s *Service, id int64) []string {
		t.Helper()
		plan, err := s.DeletePlan(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		// What the dialog shows is built from the same plan: each folder it says goes is
		// one the plan removes, and each file it names is one the plan moves.
		listed := map[string]bool{}
		names := map[string]bool{}
		for _, p := range plan.paths {
			listed[p] = true
			names[filepath.Base(p)] = true
		}
		for _, f := range plan.Folders {
			if f.Removed != listed[f.Path] {
				t.Errorf("preview: folder %s removed=%v but the plan says %v", f.Name, f.Removed, listed[f.Path])
			}
		}
		for _, v := range plan.Versions {
			if !names[v.FileName] {
				t.Errorf("preview names %s, which the plan doesn't move", v.FileName)
			}
		}
		for _, x := range plan.Extras {
			if !names[path.Base(x.Name)] {
				t.Errorf("preview names %s, which the plan doesn't move", x.Name)
			}
		}
		lastLeftovers = nil
		for _, x := range plan.Extras {
			lastLeftovers = append(lastLeftovers, x.Path)
		}
		return plan.paths
	}
	layoutCheck = func(t *testing.T, c layoutCase, removed, preview []string, deleteErr error) {
		t.Helper()
		if deleteErr != nil {
			t.Fatalf("delete failed: %v", deleteErr)
		}
		want := append([]string{}, c.want...)
		sort.Strings(want)
		if want == nil {
			want = []string{}
		}
		if strings.Join(removed, "\n") != strings.Join(want, "\n") {
			t.Errorf("removed:\n  %s\nwant:\n  %s", strings.Join(removed, "\n  "), strings.Join(want, "\n  "))
		}
		if c.action != "" {
			return // only "delete the movie" has a preview
		}
		if strings.HasPrefix(c.bin, "refuse:") || len(c.late) > 0 || c.noList {
			// A refused leftover stays: the delete did the preview minus it, never more.
			for _, p := range removed {
				if !layoutHas(preview, p) {
					t.Errorf("removed %s, which the preview didn't list", p)
				}
			}
			return
		}
		if strings.Join(removed, "\n") != strings.Join(preview, "\n") {
			t.Errorf("the preview isn't the delete:\npreview:\n  %s\nremoved:\n  %s", strings.Join(preview, "\n  "), strings.Join(removed, "\n  "))
		}
	}
	layoutReport = func(t *testing.T, cases []layoutCase, removed map[string][]string) {
		t.Helper()
		basePath := os.Getenv("ARRMADA_LAYOUT_BASELINE")
		if basePath == "" {
			return
		}
		b, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatal(err)
		}
		var base map[string][]string
		if err := json.Unmarshal(b, &base); err != nil {
			t.Fatal(err)
		}
		var table strings.Builder
		table.WriteString("| Case | v0.3.2 removed | This branch removed | Verdict |\n|---|---|---|---|\n")
		for _, c := range cases {
			old, ok := base[c.name]
			if !ok {
				t.Errorf("%s: missing from the v0.3.2 dump", c.name)
				continue
			}
			now := removed[c.name]
			more, less := layoutMinus(now, old), layoutMinus(old, now)
			for _, p := range more {
				if !insideOwn(p, c.own) {
					t.Errorf("%s: removes %s, which v0.3.2 kept and isn't inside the movie's own folder", c.name, p)
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
			fmt.Fprintf(&table, "| %s | %s | %s | %s |\n", c.name, layoutShort(old), layoutShort(now), verdict)
		}
		t.Log("\n" + table.String())
		if out := os.Getenv("ARRMADA_LAYOUT_TABLE"); out != "" {
			if err := os.WriteFile(out, []byte(table.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// insideOwn reports whether p is one of the case's own folders ("a|b") or inside one.
func insideOwn(p, own string) bool {
	if own == "" {
		return false
	}
	for _, o := range strings.Split(own, "|") {
		if p == o || strings.HasPrefix(p, o+"/") {
			return true
		}
	}
	return false
}

func layoutMinus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !layoutHas(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func layoutHas(list []string, x string) bool {
	for _, y := range list {
		if y == x {
			return true
		}
	}
	return false
}

func layoutShort(paths []string) string {
	if len(paths) == 0 {
		return "nothing"
	}
	return fmt.Sprintf("%d: %s", len(paths), strings.Join(paths, ", "))
}
