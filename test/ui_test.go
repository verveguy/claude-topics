package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `topic ui --rows` is what the picker shows: topics grouped under their working
// directory, headings first. Hermetic — nothing here is a live session, so every
// topic reads as down.
func TestUIRowsGroupByDirectory(t *testing.T) {
	t.Setenv("NO_COLOR", "1") // plain marks, so the rows compare as text
	e := newEnv(t)
	e.seedTopic("zen", map[string]any{"name": "zen", "dir": "/work/jetpack"})
	e.seedTopic("orac", map[string]any{"name": "orac", "dir": "/work/jetpack"})
	e.seedTopic("kiln", map[string]any{"name": "Kiln", "dir": "/work/ainative"})

	got := e.mustRun("ui", "--rows").stdout
	want := "\t▾ /work/ainative\t/work/ainative\n" +
		"down\t▼ Kiln\tKiln\n" +
		"\t▾ /work/jetpack\t/work/jetpack\n" +
		"down\t▼ orac\torac\n" +
		"down\t▼ zen\tzen\n"
	if got != want {
		t.Errorf("rows =\n%q\nwant\n%q", got, want)
	}

	// A collapsed directory keeps its heading, with a count, and loses its topics.
	got = e.mustRun("ui", "--rows", "--collapse=/work/jetpack").stdout
	want = "\t▾ /work/ainative\t/work/ainative\n" +
		"down\t▼ Kiln\tKiln\n" +
		"\t▸ /work/jetpack (2)\t/work/jetpack\n"
	if got != want {
		t.Errorf("collapsed rows =\n%q\nwant\n%q", got, want)
	}
}

// The layout must come up as two panes with the right one recorded, whatever the user's
// tmux.conf says. An early version addressed panes as "0.1" and silently built nothing
// under `base-index 1`, so this runs with exactly that config.
func TestUILayoutSurvivesBaseIndexOne(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	home := t.TempDir()
	conf := "set -g base-index 1\nsetw -g pane-base-index 1\n"
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := "topic-ui-test-" + filepath.Base(home)
	t.Setenv("HOME", home)
	t.Setenv("TOPIC_UI_SOCKET", sock)
	t.Setenv("TMUX", "") // as if launched from a bare terminal
	t.Cleanup(func() { _ = tmuxCommand("-L", sock, "kill-server").Run() })

	e := newEnv(t)
	e.mustRun("ui", "--no-attach")

	panes, err := tmuxCommand("-L", sock, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("listing panes: %v", err)
	}
	ids := strings.Fields(string(panes))
	if len(ids) != 2 {
		t.Fatalf("want 2 panes, got %v", ids)
	}
	right, err := tmuxCommand("-L", sock, "show", "-gv", "@right-pane").Output()
	if err != nil {
		t.Fatalf("@right-pane not set: %v", err)
	}
	if r := strings.TrimSpace(string(right)); r != ids[1] {
		t.Errorf("@right-pane = %q, want the second pane %q", r, ids[1])
	}

	// A second run must reuse the layout, not stack another one.
	e.mustRun("ui", "--no-attach")
	again, _ := tmuxCommand("-L", sock, "list-panes", "-F", "#{pane_id}").Output()
	if got := strings.Fields(string(again)); len(got) != 2 {
		t.Errorf("second run changed the layout: %v", got)
	}

	// A client narrower than the window was built for must not squeeze the list: tmux
	// shrinks the first pane first, and a 54-column list once became 4.
	harness := sock + "-client"
	t.Cleanup(func() { _ = tmuxCommand("-L", harness, "kill-server").Run() })
	if err := tmuxCommand("-L", harness, "new-session", "-d", "-x", "120", "-y", "40",
		"env -u TMUX tmux -L "+sock+" attach -t "+sock).Run(); err != nil {
		t.Fatalf("attaching a client: %v", err)
	}
	var width string
	for i := 0; i < 50; i++ {
		out, _ := tmuxCommand("-L", sock, "list-panes", "-F", "#{pane_width}").Output()
		if f := strings.Fields(string(out)); len(f) == 2 {
			width = f[0]
			if width != "54" { // the build-time width: wait for the client's resize to land
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if w, _ := strconv.Atoi(width); w < 30 {
		t.Errorf("list pane is %q columns wide after a 120-column client attached; want >= 30", width)
	}

	// --quit closes the UI's server and nothing else.
	e.mustRun("ui", "--quit")
	if err := tmuxCommand("-L", sock, "has-session").Run(); err == nil {
		t.Error("the UI is still running after --quit")
	}
}

// Each plain `topic ui` is its own instance, so two terminal windows do not share one UI
// (and its folded groups). --list names them, --quit needs an id when there are several,
// and --quit --all closes every one. Runs on a private TMUX_TMPDIR, so the developer's own
// UI is neither listed nor touched.
func TestUIInstancesAreSeparate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("instances are found through tmux's socket directory")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Short on purpose: a unix socket path is capped near 104 bytes, and t.TempDir() is not.
	tmp, err := os.MkdirTemp("/tmp", "tui")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMUX_TMPDIR", tmp)
	t.Setenv("TMUX", "")
	t.Setenv("TOPIC_UI_SOCKET", "")
	e := newEnv(t)
	t.Cleanup(func() { e.run("ui", "--quit", "--all") })

	e.mustRun("ui", "--no-attach")
	e.mustRun("ui", "--no-attach")
	ids := strings.Fields(e.mustRun("ui", "--list").stdout)
	// each line is "<id>\tdetached"
	var names []string
	for i := 0; i+1 < len(ids); i += 2 {
		names = append(names, ids[i])
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("want two distinct instances, got %q", ids)
	}

	if r := e.run("ui", "--quit"); r.code == 0 {
		t.Errorf("--quit with two instances running should ask which, got success: %s", r.stdout)
	}
	e.mustRun("ui", "--quit", names[0])
	if got := strings.Fields(e.mustRun("ui", "--list").stdout); len(got) != 2 || got[0] != names[1] {
		t.Errorf("after quitting %s the list = %q, want only %s", names[0], got, names[1])
	}
	e.mustRun("ui", "--quit", "--all")
	if out := e.mustRun("ui", "--list").stdout; !strings.Contains(out, "not running") {
		t.Errorf("after --quit --all: %q", out)
	}
}

// Enter on a heading runs `topic ui --activate`, which folds the group in the instance's own
// state and prints the fzf action that redraws the list in place. fzf is never restarted for
// a choice, so the fold has to live where every helper and a hot-reloaded picker can see it:
// the instance's tmux server.
func TestUIActivateFoldsAHeadingInPlace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("instances are found through tmux's socket directory")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmp, err := os.MkdirTemp("/tmp", "tui") // short: unix socket paths are capped near 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMUX_TMPDIR", tmp)
	t.Setenv("TMUX", "")
	t.Setenv("TOPIC_UI_SOCKET", "")
	t.Setenv("NO_COLOR", "1")
	e := newEnv(t)
	e.seedTopic("zen", map[string]any{"name": "zen", "dir": "/work/jetpack"})
	e.seedTopic("orac", map[string]any{"name": "orac", "dir": "/work/jetpack"})
	e.seedTopic("kiln", map[string]any{"name": "Kiln", "dir": "/work/ainative"})
	t.Cleanup(func() { e.run("ui", "--quit", "--all") })

	e.mustRun("ui", "--no-attach")
	id := strings.Fields(e.mustRun("ui", "--list").stdout)[0]
	t.Setenv("TOPIC_UI_SOCKET", "topics-ui-"+id) // what the picker and fzf's helpers inherit

	// The second heading (row index 2, zero-based) is /work/jetpack.
	action := strings.TrimSpace(e.mustRun("ui", "--activate", "", "/work/jetpack", "2").stdout)
	if !strings.HasPrefix(action, "reload-sync(") || !strings.HasSuffix(action, "+clear-query+pos(3)") {
		t.Errorf("action = %q, want a reload-sync that ends +clear-query+pos(3)", action)
	}
	want := "\t▾ /work/ainative\t/work/ainative\n" +
		"down\t▼ Kiln\tKiln\n" +
		"\t▸ /work/jetpack (2)\t/work/jetpack\n"
	if got := e.mustRun("ui", "--rows").stdout; got != want {
		t.Errorf("after folding, rows =\n%q\nwant\n%q", got, want)
	}

	// The same choice again unfolds it.
	e.mustRun("ui", "--activate", "", "/work/jetpack", "2")
	if got := e.mustRun("ui", "--rows").stdout; strings.Contains(got, "▸") || !strings.Contains(got, "orac") {
		t.Errorf("after unfolding, rows =\n%q", got)
	}
}
