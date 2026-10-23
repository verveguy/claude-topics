package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestWatchBinaryFiresOnceTheFileChangesAndSettles(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "topic")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	changed := watchBinary(bin, 10*time.Millisecond)

	time.Sleep(60 * time.Millisecond)
	if isDone(changed) {
		t.Fatal("fired with the binary untouched")
	}
	if err := os.WriteFile(bin, []byte("version two"), 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("did not fire after the binary was rewritten")
	}
}

func TestMatchSessionsFiltersByPatternAndSkipsTopics(t *testing.T) {
	live := []string{"zen", "fabrik", "fabrik daemon", "Dispatcher", "scratch"}
	isTopic := func(n string) bool { return n == "zen" || n == "Dispatcher" }
	got := matchSessions(live, []string{"fabrik*", "zen"}, isTopic)
	want := []string{"fabrik", "fabrik daemon"} // zen matches a pattern but is a topic
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sessions = %v, want %v", got, want)
	}
}

func TestExtraSessionPatternsIgnoresCommentsAndBlanks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ui-sessions"), []byte("# daemons\nfabrik*\n\n  orac-daemon  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := extraSessionPatterns(root)
	if want := []string{"fabrik*", "orac-daemon"}; !reflect.DeepEqual(got, want) {
		t.Errorf("patterns = %v, want %v", got, want)
	}
	if extraSessionPatterns(t.TempDir()) != nil {
		t.Error("a missing file should mean no patterns")
	}
}

func TestAddSessionPattern(t *testing.T) {
	root := filepath.Join(t.TempDir(), "topics") // not created yet
	if added, err := addSessionPattern(root, " fabrik* "); err != nil || !added {
		t.Fatalf("first add = %v, %v", added, err)
	}
	if added, err := addSessionPattern(root, "fabrik*"); err != nil || added {
		t.Errorf("a repeat should be a no-op, got %v, %v", added, err)
	}
	// A file without a trailing newline must not have the next pattern glued onto it.
	if err := os.WriteFile(filepath.Join(root, "ui-sessions"), []byte("fabrik*"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := addSessionPattern(root, "orac-daemon"); err != nil {
		t.Fatal(err)
	}
	if got, want := extraSessionPatterns(root), []string{"fabrik*", "orac-daemon"}; !reflect.DeepEqual(got, want) {
		t.Errorf("patterns = %v, want %v", got, want)
	}
	if _, err := addSessionPattern(root, "[bad"); err == nil {
		t.Error("a malformed glob should be refused")
	}
	if _, err := addSessionPattern(root, "  "); err == nil {
		t.Error("an empty pattern should be refused")
	}
}

func TestRemoveSessionPattern(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ui-sessions"), []byte("# daemons\nfabrik*\norac-daemon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := removeSessionPattern(root, "fabrik*"); err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if removed, _ := removeSessionPattern(root, "fabrik*"); removed {
		t.Error("removing twice should report not listed")
	}
	data, _ := os.ReadFile(filepath.Join(root, "ui-sessions"))
	if want := "# daemons\norac-daemon\n"; string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
	if removed, _ := removeSessionPattern(root, "# daemons"); removed {
		t.Error("a comment is not a pattern")
	}
	if removed, _ := removeSessionPattern(t.TempDir(), "x"); removed {
		t.Error("no file means nothing to remove")
	}
}

func TestBuildRowsAppendsOtherSessionsLast(t *testing.T) {
	root := seedRegistry(t, map[string]string{"a": "/home/me/x"})
	down := func(string) string { return "down" }
	got := buildRows(root, "/home/me", 40, nil, []string{"fabrik"}, down)
	want := []uiRow{
		{label: "~/x", dir: "/home/me/x"},
		{state: "down", label: "a"},
		{label: otherSessionsDir, dir: otherSessionsDir},
		{state: "SESSION", label: "fabrik"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%v\nwant\n%v", got, want)
	}
	got = buildRows(root, "/home/me", 40, map[string]bool{otherSessionsDir: true}, []string{"fabrik"}, down)
	if last := got[len(got)-1]; last.hidden != 1 || last.dir != otherSessionsDir {
		t.Errorf("collapsed heading = %+v", last)
	}
}

// The sweep must never unlink a live server's socket, nor one that is still starting: only
// a file that is past the grace period and refuses connections is abandoned.
func TestRemoveUISocketOnlyRemovesAbandonedSockets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux sockets live under a unix uid directory")
	}
	tmp, err := os.MkdirTemp("/tmp", "tus") // short: unix socket paths are capped near 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMUX_TMPDIR", tmp)
	if err := os.MkdirAll(uiSocketDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	listen := func(name string) net.Listener {
		l, err := net.Listen("unix", filepath.Join(uiSocketDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	age := func(name string) {
		old := time.Now().Add(-time.Minute)
		if err := os.Chtimes(filepath.Join(uiSocketDir(), name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(name string) bool {
		_, err := os.Lstat(filepath.Join(uiSocketDir(), name))
		return err == nil
	}

	live := listen("topics-ui-live")
	defer live.Close()
	age("topics-ui-live")

	fresh := listen("topics-ui-fresh") // nothing listens once closed, but it is brand new
	fresh.(*net.UnixListener).SetUnlinkOnClose(false)
	fresh.Close()

	dead := listen("topics-ui-dead")
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	dead.Close()
	age("topics-ui-dead")

	for _, n := range []string{"topics-ui-live", "topics-ui-fresh", "topics-ui-dead"} {
		removeUISocket(n)
	}
	if !exists("topics-ui-live") {
		t.Error("removed the socket of a server that is answering")
	}
	if !exists("topics-ui-fresh") {
		t.Error("removed a socket inside the grace period: a starting server would be orphaned")
	}
	if exists("topics-ui-dead") {
		t.Error("kept an old socket that refuses connections")
	}
}

func TestWatchBinaryMissingFileNeverFires(t *testing.T) {
	changed := watchBinary(filepath.Join(t.TempDir(), "nope"), 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if isDone(changed) {
		t.Fatal("fired for a file that does not exist")
	}
}

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
	got := buildRows(root, "/home/me", 40, nil, nil, state)
	want := []uiRow{
		{label: "(no directory)", dir: ""},
		{state: "down", label: "Strayed"},
		{label: "~/dev/ainative", dir: "/home/me/dev/ainative"},
		{state: "down", label: "Kiln"},
		{label: "~/dev/jetpack", dir: "/home/me/dev/jetpack"},
		{state: "down", label: "Alpha"},
		{state: "UP", label: "orac"},
		{state: "down", label: "zen"},
	}
	// "" sorts before any path, so the directory-less group leads.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%v\nwant\n%v", got, want)
	}
}

func TestBuildRowsCollapsedKeepsHeading(t *testing.T) {
	root := seedRegistry(t, map[string]string{"a": "/home/me/x", "b": "/home/me/x", "c": "/home/me/y"})
	down := func(string) string { return "down" }
	got := buildRows(root, "/home/me", 40, map[string]bool{"/home/me/x": true}, nil, down)
	want := []uiRow{
		{label: "~/x", dir: "/home/me/x", hidden: 2},
		{label: "~/y", dir: "/home/me/y"},
		{state: "down", label: "c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%v\nwant\n%v", got, want)
	}
}

func TestBuildRowsEmptyRegistry(t *testing.T) {
	if rows := buildRows(t.TempDir(), "/home/me", 28, nil, nil, func(string) string { return "down" }); len(rows) != 0 {
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

func TestEscapeTmuxFormat(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"#(rm -rf /)": "##(rm -rf /)",
		"#{pane_id}":  "##{pane_id}",
		"a # b ## c":  "a ## b #### c",
	}
	for in, want := range cases {
		if got := escapeTmuxFormat(in); got != want {
			t.Errorf("escapeTmuxFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLastLine(t *testing.T) {
	boom := errors.New("exit status 1")
	cases := []struct {
		out  string
		want string
	}{
		{"", "exit status 1"},
		{"  \n\n", "exit status 1"},
		{"starting\nno such topic: \"x\"\n", `no such topic: "x"`},
		{"only", "only"},
	}
	for _, c := range cases {
		if got := lastLine(c.out, boom); got != c.want {
			t.Errorf("lastLine(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
