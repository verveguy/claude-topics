package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func seedRegistry(t *testing.T, topics map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, dir := range topics {
		d := filepath.Join(root, slugify(name))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		m := map[string]any{"name": name}
		if dir != "" {
			m["dir"] = dir
		}
		if err := save(filepath.Join(d, "topic.json"), m); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBuildRowsGroupsByDirectory(t *testing.T) {
	root := seedRegistry(t, map[string]string{
		"zen":     "/home/me/dev/jetpack",
		"orac":    "/home/me/dev/jetpack",
		"Alpha":   "/home/me/dev/jetpack", // case-insensitive name order
		"Kiln":    "/home/me/dev/ainative",
		"Strayed": "",
	})
	state := func(name string) string {
		if name == "orac" {
			return "UP"
		}
		return "down"
	}
	got := buildRows(root, "/home/me", 40, state)
	want := []uiRow{
		{label: "(no directory)"},
		{"down", "Strayed"},
		{label: "~/dev/ainative"},
		{"down", "Kiln"},
		{label: "~/dev/jetpack"},
		{"down", "Alpha"},
		{"UP", "orac"},
		{"down", "zen"},
	}
	// "" sorts before any path, so the directory-less group leads.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%v\nwant\n%v", got, want)
	}
}

func TestBuildRowsEmptyRegistry(t *testing.T) {
	if rows := buildRows(t.TempDir(), "/home/me", 28, func(string) string { return "down" }); len(rows) != 0 {
		t.Errorf("expected no rows, got %v", rows)
	}
}

func TestShortDir(t *testing.T) {
	cases := []struct {
		dir, home string
		max       int
		want      string
	}{
		{"", "/h", 28, "(no directory)"},
		{"/h", "/h", 28, "~"},
		{"/h/dev/x", "/h", 28, "~/dev/x"},
		{"/hx/dev", "/h", 28, "/hx/dev"}, // a sibling of home is not under it
		{"/h/dev/autodesk/jetpack-project", "/h", 28, "…/autodesk/jetpack-project"},
		{"/h/dev/autodesk/responsibility-mapping-long", "/h", 20, "…bility-mapping-long"}, // no separator in the tail: keep it
		{"/elsewhere/p", "/h", 28, "/elsewhere/p"},
	}
	for _, c := range cases {
		if got := shortDir(c.dir, c.home, c.max); got != c.want {
			t.Errorf("shortDir(%q, %q, %d) = %q, want %q", c.dir, c.home, c.max, got, c.want)
		}
	}
}

func TestWithoutEnv(t *testing.T) {
	got := withoutEnv([]string{"A=1", "TMUX=/tmp/x,1,0", "TMUXED=keep", "B=2"}, "TMUX")
	want := []string{"A=1", "TMUXED=keep", "B=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
