package main

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/tristenlammi/arrmada/internal/applog"
)

// The Logs page's module filter offers applog.Modules; every tag main hands out has to be
// on that list, or its lines can only be found by searching.
func TestModuleTagsAreListed(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	tags := regexp.MustCompile(`modLog\(log, "([a-z]+)"\)`).FindAllStringSubmatch(string(src), -1)
	if len(tags) == 0 {
		t.Fatal("no modLog calls found in main.go")
	}
	for _, m := range tags {
		if !slices.Contains(applog.Modules, m[1]) {
			t.Errorf("module %q is tagged in main.go but missing from applog.Modules", m[1])
		}
	}
}
