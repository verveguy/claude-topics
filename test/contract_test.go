package test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Contract coverage: the behaviour each command promises, independent of the bugs we
// happened to hit. These pin the CLI down before commands are migrated to Go.

// A move must carry the whole topic: registry, docs, and EVERY transcript it can still
// resume — the current session and the retired ones in `history`. Leaving history
// behind silently breaks the emergency path back to a pre-handoff session.
func TestMoveCarriesHistoryTranscripts(t *testing.T) {
	e := newEnv(t)
	const cur, old = "cur-sid", "old-sid"
	e.seedTopic("mover", map[string]any{
		"name": "Mover", "dir": "/tmp", "sessionId": cur, "generation": "2",
		"history": []any{map[string]any{"sessionId": old, "handoffDoc": "/x/h.md"}},
	})
	e.seedTranscript("-tmp", cur, []string{
		`{"type":"custom-title","customTitle":"Mover","sessionId":"` + cur + `"}`,
		`{"type":"bridge-session","bridgeSessionId":"cse_MOVE"}`,
	})
	e.seedTranscript("-tmp", old, []string{`{"type":"user","cwd":"/tmp"}`})
	mkdirAll(t, filepath.Join(e.topicsRoot, "mover", "handoffs"))
	os.WriteFile(filepath.Join(e.topicsRoot, "mover", "handoffs", "h.md"), []byte("doc"), 0o600)

	dst := e.newProfile("dest")
	e.mustRun("move", "Mover", "--to", dst)

	for _, sid := range []string{cur, old} {
		if _, err := os.Stat(filepath.Join(dst, "projects", "-tmp", sid+".jsonl")); err != nil {
			t.Errorf("transcript %s did not move: %v", sid, err)
		}
		if _, err := os.Stat(filepath.Join(e.configDir, "projects", "-tmp", sid+".jsonl")); err == nil {
			t.Errorf("transcript %s left behind in the source profile", sid)
		}
	}
	if e.exists("mover", "topic.json") {
		t.Error("source registry entry survived the move")
	}
	if _, err := os.Stat(filepath.Join(dst, "topics", "mover", "handoffs", "h.md")); err != nil {
		t.Error("handoff doc did not move")
	}
	var moved map[string]any
	readJSON(t, filepath.Join(dst, "topics", "mover", "topic.json"), &moved)
	if moved["movedFrom"] != e.configDir {
		t.Errorf("movedFrom = %v, want %s", moved["movedFrom"], e.configDir)
	}
	// Moving re-registers Remote Control, or the topic keeps advertising its old
	// profile's account under a hostname-derived name.
	b, _ := os.ReadFile(filepath.Join(dst, "projects", "-tmp", cur+".jsonl"))
	if strings.Contains(string(b), "bridge-session") {
		t.Error("bridge was not re-minted by move")
	}
}

func TestMoveCopyLeavesTheOriginal(t *testing.T) {
	e := newEnv(t)
	const sid = "copy-sid"
	e.seedTopic("copier", map[string]any{"name": "Copier", "dir": "/tmp", "sessionId": sid})
	e.seedTranscript("-tmp", sid, []string{`{"type":"user","cwd":"/tmp"}`})
	dst := e.newProfile("dest")

	r := e.mustRun("move", "Copier", "--to", dst, "--copy")
	mustContain(t, r.out(), "still in", "should warn that both copies now exist")

	if !e.exists("copier", "topic.json") {
		t.Error("--copy removed the original")
	}
	if _, err := os.Stat(filepath.Join(dst, "topics", "copier", "topic.json")); err != nil {
		t.Error("--copy did not create the destination")
	}
}

func TestMoveKeepBridgePreservesRemoteControl(t *testing.T) {
	e := newEnv(t)
	const sid = "keep-sid"
	e.seedTopic("keeper", map[string]any{"name": "Keeper", "dir": "/tmp", "sessionId": sid})
	e.seedTranscript("-tmp", sid, []string{
		`{"type":"bridge-session","bridgeSessionId":"cse_KEEP"}`,
	})
	dst := e.newProfile("dest")

	e.mustRun("move", "Keeper", "--to", dst, "--keep-bridge")

	b, _ := os.ReadFile(filepath.Join(dst, "projects", "-tmp", sid+".jsonl"))
	if !strings.Contains(string(b), "cse_KEEP") {
		t.Error("--keep-bridge dropped the bridge anyway")
	}
}

func TestMoveDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.seedTopic(slug("Dry Run"), map[string]any{"name": "Dry Run", "dir": "/tmp", "sessionId": "d-sid"})
	dst := e.newProfile("dest")

	r := e.mustRun("move", "Dry Run", "--to", dst, "--dry-run")
	mustContain(t, r.out(), "Dry run", "should say it changed nothing")
	if !e.exists(slug("Dry Run"), "topic.json") {
		t.Error("--dry-run moved the topic")
	}
	if _, err := os.Stat(filepath.Join(dst, "topics", slug("Dry Run"))); err == nil {
		t.Error("--dry-run created the destination")
	}
}

// Adoption by pid: the live-session record carries the authoritative mapping, and the
// pid is stored so `up` can refuse while the original process is alive.
func TestAdoptByPidRecordsThePid(t *testing.T) {
	e := newEnv(t)
	mkdirAll(t, filepath.Join(e.configDir, "sessions"))
	writeJSON(t, filepath.Join(e.configDir, "sessions", "a.json"), map[string]any{
		"pid": os.Getpid(), "sessionId": "live-sid", "cwd": "/tmp/work", "name": "Live One",
	})

	e.mustRun("adopt", "Renamed Topic", "--pid", strconv.Itoa(os.Getpid()))

	got := e.readTopic("renamed-topic")
	if got["sessionId"] != "live-sid" {
		t.Errorf("sessionId = %v", got["sessionId"])
	}
	if got["state"] != "adopted" {
		t.Errorf("state = %v, want adopted", got["state"])
	}
	if got["adoptedPid"] != strconv.Itoa(os.Getpid()) {
		t.Errorf("adoptedPid = %v, want %d", got["adoptedPid"], os.Getpid())
	}
	if got["dir"] != "/tmp/work" {
		t.Errorf("dir = %v, want /tmp/work (from the session record)", got["dir"])
	}
}

// An explicit --pid that matches nothing must be an error, not a silent fallback to
// some other session.
func TestAdoptRejectsUnknownPid(t *testing.T) {
	e := newEnv(t)
	mkdirAll(t, filepath.Join(e.configDir, "sessions"))
	writeJSON(t, filepath.Join(e.configDir, "sessions", "a.json"), map[string]any{
		"pid": os.Getpid(), "sessionId": "live-sid", "cwd": "/tmp", "name": "Live One",
	})

	r := e.run("adopt", "Whatever", "--pid", "999999")
	if r.code == 0 {
		t.Fatal("adopt should reject a pid with no live session")
	}
	mustContain(t, r.out(), "no live session with pid", "should say the pid is unknown")
}

// With several live sessions and no name match, adopt must list candidates rather
// than guess.
func TestAdoptAmbiguousListsCandidates(t *testing.T) {
	e := newEnv(t)
	mkdirAll(t, filepath.Join(e.configDir, "sessions"))
	for i, name := range []string{"One", "Two"} {
		writeJSON(t, filepath.Join(e.configDir, "sessions", strconv.Itoa(i)+".json"), map[string]any{
			"pid": os.Getpid(), "sessionId": "sid-" + name, "cwd": "/tmp", "name": name,
		})
	}

	r := e.run("adopt", "No Such Session")
	if r.code == 0 {
		t.Fatal("adopt should not guess between candidates")
	}
	mustContain(t, r.out(), "--pid", "should tell the user how to disambiguate")
}

// --session-id takes a UUID the caller already knows and derives the directory from
// the transcript rather than $PWD.
func TestAdoptBySessionIdDerivesDirFromTranscript(t *testing.T) {
	e := newEnv(t)
	const sid = "known-sid"
	e.seedTranscript("-tmp-elsewhere", sid, []string{
		`{"type":"user","cwd":"/tmp/elsewhere"}`,
	})

	e.mustRun("adopt", "By UUID", "--session-id", sid)

	got := e.readTopic("by-uuid")
	if got["dir"] != "/tmp/elsewhere" {
		t.Errorf("dir = %v, want /tmp/elsewhere (from the transcript's own cwd)", got["dir"])
	}
	if got["state"] != "down" {
		t.Errorf("state = %v, want down — nothing is running, so nothing to claim", got["state"])
	}
}

func TestPrunePurgeRemovesDocuments(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("gone", map[string]any{"name": "Gone", "dir": "/tmp", "sessionId": "nope"})
	mkdirAll(t, filepath.Join(e.topicsRoot, "gone", "handoffs"))
	os.WriteFile(filepath.Join(e.topicsRoot, "gone", "handoffs", "h.md"), []byte("x"), 0o600)

	e.mustRun("prune", "--yes", "--purge")

	if e.exists("gone") {
		t.Error("--purge left the topic directory behind")
	}
}

// forget keeps handoff docs by default: they are often the only surviving record of
// what a topic was about.
func TestForgetKeepsHandoffDocsByDefault(t *testing.T) {
	e := newEnv(t)
	e.seedTopic(slug("Keep Docs"), map[string]any{"name": "Keep Docs", "dir": "/tmp", "sessionId": "s"})
	mkdirAll(t, filepath.Join(e.topicsRoot, slug("Keep Docs"), "handoffs"))
	os.WriteFile(filepath.Join(e.topicsRoot, slug("Keep Docs"), "handoffs", "h.md"), []byte("x"), 0o600)

	e.mustRun("forget", "Keep Docs")

	if e.exists(slug("Keep Docs"), "topic.json") {
		t.Error("forget did not remove the registry entry")
	}
	if !e.exists(slug("Keep Docs"), "handoffs", "h.md") {
		t.Error("forget removed handoff docs without --purge")
	}
}

func TestPathHelpersCreateTheirDirectories(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ cmd, dir string }{
		{"handoff-path", "handoffs"},
		{"fork-path", "briefs"},
	} {
		r := e.mustRun(tc.cmd, "Brand New Topic")
		got := strings.TrimSpace(r.stdout)
		if !strings.Contains(got, filepath.Join("brand-new-topic", tc.dir)) {
			t.Errorf("%s printed %q, want a path under %s/", tc.cmd, got, tc.dir)
		}
		if _, err := os.Stat(filepath.Dir(got)); err != nil {
			t.Errorf("%s did not create its directory: %v", tc.cmd, err)
		}
	}
}

func TestRenameRefusesAnExistingName(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("first", map[string]any{"name": "First", "dir": "/tmp", "sessionId": "a"})
	e.seedTopic("second", map[string]any{"name": "Second", "dir": "/tmp", "sessionId": "b"})

	r := e.run("rename", "First", "Second")
	if r.code == 0 {
		t.Fatal("rename should refuse to collide with an existing topic")
	}
	if !e.exists("first", "topic.json") {
		t.Error("the source topic was destroyed by a refused rename")
	}
}

func TestRenameDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("stable", map[string]any{"name": "Stable", "dir": "/tmp", "sessionId": "a"})

	e.mustRun("rename", "Stable", "Changed", "--dry-run")

	if !e.exists("stable", "topic.json") {
		t.Error("--dry-run renamed the topic")
	}
	if e.exists("changed", "topic.json") {
		t.Error("--dry-run created the new name")
	}
}

func TestDownAllWithNothingRunning(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("idle", map[string]any{"name": "Idle", "dir": "/tmp", "sessionId": "a"})

	r := e.mustRun("down-all")
	mustContain(t, r.out(), "nothing up", "should say there was nothing to do")
}

// whoami maps the tmux session back to a registered topic. Run against a sandboxed
// registry it must fail cleanly rather than guess or crash.
func TestWhoamiFailsCleanlyOutsideAKnownTopic(t *testing.T) {
	e := newEnv(t)
	r := e.run("whoami")
	if r.code == 0 {
		t.Skip("running inside a topic registered in this sandbox — nothing to assert")
	}
	if strings.TrimSpace(r.out()) == "" {
		t.Error("whoami failed silently")
	}
}

// profiles reads the real home directory by design, so assert only its shape.
func TestProfilesReportsTheActiveProfile(t *testing.T) {
	e := newEnv(t)
	r := e.mustRun("profiles")
	mustContain(t, r.out(), "* = active", "should explain its own marker")
}
