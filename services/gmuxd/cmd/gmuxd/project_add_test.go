package main

import (
	"path/filepath"
	"testing"

	"github.com/gmuxapp/gmux/services/gmuxd/internal/projects"
)

func TestProjectMatchingAllPathsMakesAddIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	items := []projects.Item{
		{Slug: "gmux", Match: []projects.MatchRule{{Path: "~/src/gmux"}, {Path: "~/src/gmux-docs"}, {Remote: "github.com/gmuxapp/gmux"}}},
	}
	for _, requested := range [][]projects.MatchRule{
		{{Path: "~/src/gmux"}},
		{{Path: filepath.Join(home, "src", "gmux")}},
	} {
		item, ok := projectMatchingAllPaths(items, requested)
		if !ok || item.Slug != "gmux" {
			t.Fatalf("match(%+v)=(%+v,%v)", requested, item, ok)
		}
	}
	if item, ok := projectMatchingAllPaths(items, []projects.MatchRule{{Path: "~/src/gmux"}, {Path: "~/src/gmux-docs"}}); !ok || item.Slug != "gmux" {
		t.Fatalf("all-path match=(%+v,%v)", item, ok)
	}
	for _, requested := range [][]projects.MatchRule{
		{{Path: "~/src/other"}},
		{{Path: "~/src/gmux"}, {Path: "~/src/missing"}},
		{{Path: "~/other-clone", Remote: "github.com/gmuxapp/gmux"}},
		{{Remote: "github.com/gmuxapp/gmux"}},
	} {
		if item, ok := projectMatchingAllPaths(items, requested); ok {
			t.Fatalf("unrelated path matched: request=%+v item=%+v", requested, item)
		}
	}
}
