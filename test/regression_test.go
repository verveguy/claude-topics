package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each test here corresponds to a defect found on 2026-08-09. They are hermetic: no
// session is ever launched, so they run in milliseconds and cannot touch real topics.

// `topic forget` listed handoff docs with `ls`, which exits non-zero on an absent
// directory. Under pipefail + set -e that aborted the command substitution BEFORE
// anything was deleted, so forgetting a topic with no handoff docs exited 1, printed
// nothing, and did nothing.
func TestForgetWithoutHandoffDocs(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("plain", map[string]any{"name": "Plain", "dir": "/tmp", "sessionId": "abc"})

	r := e.run("forget", "Plain")
	if r.code != 0 {
		t.Fatalf("forget exited %d, want 0\n%s", r.code, r.out())
	}
	mustContain(t, r.out(), "Forgot", "forget should report what it did")
	if e.exists("plain", "topic.json") {
		t.Error("topic.json still present after forget")
	}
}

// `parent="$(cmd_whoami || true)"` aborted the script silently: die/exit inside a
// command substitution kills the subshell before any `|| true` inside it can run.
// The visible symptom was a command that exited non-zero with no output at all.
func TestFailuresAreNeverSilent(t *testing.T) {
	e := newEnv(t)
	// fork with no brief written: must explain itself rather than die mutely.
	r := e.run("fork", "Some New Topic")
	if r.code == 0 {
		t.Fatal("fork without a brief should fail")
	}
	if strings.TrimSpace(r.out()) == "" {
		t.Fatal("fork failed with NO output — the silent-abort regression")
	}
	mustContain(t, r.out(), "brief", "should name the missing brief")
}

// An unquoted ${parent:+forkedFrom "$parent"} in reg_write word-split a topic name
// like "Fantasy Economics" into two arguments, corrupting the key/value pairing.
func TestMultiWordNamesSurviveRegistryWrites(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("two-word-topic", map[string]any{
		"name": "Two Word Topic", "dir": "/tmp", "sessionId": "sid-1",
	})

	e.mustRun("rename", "Two Word Topic", "Another Long Name")

	got := e.readTopic("another-long-name")
	if got["name"] != "Another Long Name" {
		t.Errorf("name = %v, want %q", got["name"], "Another Long Name")
	}
	if got["renamedFrom"] != "Two Word Topic" {
		t.Errorf("renamedFrom = %v, want %q — key/value pairing was corrupted",
			got["renamedFrom"], "Two Word Topic")
	}
	if got["sessionId"] != "sid-1" {
		t.Errorf("sessionId = %v, want sid-1", got["sessionId"])
	}
}

// `move` checked liveness with is_up(), which only sees tmux. An adopted topic runs
// OUTSIDE tmux, so its transcript could be moved while the session still held it open.
func TestMoveRefusesLiveAdoptedSession(t *testing.T) {
	e := newEnv(t)
	const sid = "adopted-sid"
	// A session record for a pid that really is alive: our own.
	mkdirAll(t, filepath.Join(e.configDir, "sessions"))
	writeJSON(t, filepath.Join(e.configDir, "sessions", "self.json"), map[string]any{
		"pid": os.Getpid(), "sessionId": sid, "cwd": "/tmp", "name": "Adopted",
	})
	e.seedTopic("adopted", map[string]any{
		"name": "Adopted", "dir": "/tmp", "sessionId": sid,
		"state": "adopted", "adoptedPid": os.Getpid(),
	})
	dst := e.newProfile("dest")

	r := e.run("move", "Adopted", "--to", dst)
	if r.code == 0 {
		t.Fatal("move should refuse while the adopted session is alive")
	}
	mustContain(t, r.out(), "still running", "should say why it refused")
	if !e.exists("adopted", "topic.json") {
		t.Error("registry was moved despite the refusal")
	}
}

// Per-project approvals are recorded per profile and do not travel with a topic, so a
// moved topic could arrive somewhere it was not allowed to start.
func TestMoveChecksDestinationApprovals(t *testing.T) {
	e := newEnv(t)
	const workDir = "/tmp/some-project"
	writeJSON(t, filepath.Join(e.configDir, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
		"projects": map[string]any{workDir: map[string]any{
			"hasTrustDialogAccepted":              true,
			"hasClaudeMdExternalIncludesApproved": true,
			"allowedTools":                        []string{"Bash(rm:*)"},
		}},
	})
	e.seedTopic("needs-approval", map[string]any{
		"name": "Needs Approval", "dir": workDir, "sessionId": "sid-2",
	})
	dst := e.newProfile("dest")

	r := e.run("move", "Needs Approval", "--to", dst)
	if r.code == 0 {
		t.Fatal("move should refuse when the destination has not approved the directory")
	}
	mustContain(t, r.out(), "external CLAUDE.md imports", "should name the missing approval")

	// --carry-approvals copies the approvals, and ONLY the approvals: allowedTools is
	// a permission grant and must not be escalated into a profile that never made it.
	e.mustRun("move", "Needs Approval", "--to", dst, "--carry-approvals")

	var dstCfg map[string]any
	readJSON(t, filepath.Join(dst, ".claude.json"), &dstCfg)
	projects, _ := dstCfg["projects"].(map[string]any)
	entry, _ := projects[workDir].(map[string]any)
	if entry == nil {
		t.Fatal("destination has no project entry after --carry-approvals")
	}
	if entry["hasClaudeMdExternalIncludesApproved"] != true {
		t.Error("approval was not carried")
	}
	if _, leaked := entry["allowedTools"]; leaked {
		t.Error("allowedTools was carried — that is a silent permission escalation")
	}
}

// rebridge must drop bridge-session records so the next launch mints a fresh Remote
// Control identity, keep a backup, and leave the conversation itself untouched.
func TestRebridgeStripsBridgeAndBacksUp(t *testing.T) {
	e := newEnv(t)
	const sid = "bridge-sid"
	e.seedTopic("bridged", map[string]any{
		"name": "Bridged", "dir": "/tmp", "sessionId": sid,
	})
	path := e.seedTranscript("-tmp", sid, []string{
		`{"type":"custom-title","customTitle":"Bridged","sessionId":"` + sid + `"}`,
		`{"type":"bridge-session","bridgeSessionId":"cse_OLD1"}`,
		`{"type":"user","cwd":"/tmp","text":"important conversation"}`,
		`{"type":"bridge-session","bridgeSessionId":"cse_OLD2"}`,
	})

	r := e.mustRun("rebridge", "Bridged")
	mustContain(t, r.out(), "cse_OLD2", "should report the bridge it dropped")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "bridge-session") {
		t.Error("bridge-session records survived the strip")
	}
	if !strings.Contains(string(body), "important conversation") {
		t.Error("conversation content was lost — strip removed too much")
	}
	backups, _ := filepath.Glob(filepath.Join(e.topicsRoot, "bridged", "backups", "*.jsonl"))
	if len(backups) != 1 {
		t.Fatalf("expected exactly 1 backup, got %d", len(backups))
	}
	b, _ := os.ReadFile(backups[0])
	if !strings.Contains(string(b), "cse_OLD2") {
		t.Error("backup does not contain the original bridge records")
	}
}

// A rename must reach every layer, including lineage references in other topics.
func TestRenameUpdatesLineageAndTranscript(t *testing.T) {
	e := newEnv(t)
	const sid = "rename-sid"
	e.seedTopic("parent-topic", map[string]any{
		"name": "Parent Topic", "dir": "/tmp", "sessionId": sid,
		"forks": []string{"Child Topic"},
	})
	e.seedTopic("child-topic", map[string]any{
		"name": "Child Topic", "dir": "/tmp", "sessionId": "child-sid",
		"forkedFrom": "Parent Topic",
	})
	path := e.seedTranscript("-tmp", sid, []string{
		`{"type":"custom-title","customTitle":"Parent Topic","sessionId":"` + sid + `"}`,
		`{"type":"bridge-session","bridgeSessionId":"cse_X"}`,
	})

	e.mustRun("rename", "Parent Topic", "Renamed Parent")

	if e.exists("parent-topic", "topic.json") {
		t.Error("old registry directory still present")
	}
	if got := e.readTopic("renamed-parent")["name"]; got != "Renamed Parent" {
		t.Errorf("name = %v", got)
	}
	if got := e.readTopic("child-topic")["forkedFrom"]; got != "Renamed Parent" {
		t.Errorf("child forkedFrom = %v, want %q — lineage dangles", got, "Renamed Parent")
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `"customTitle": "Renamed Parent"`) &&
		!strings.Contains(string(body), `"customTitle":"Renamed Parent"`) {
		t.Error("transcript was not re-titled — adopt would not find it by the new name")
	}
	if strings.Contains(string(body), "bridge-session") {
		t.Error("rename must re-mint the bridge, or the UI keeps the old name")
	}
}

// prune finds topics that are down with no transcript left, and is a dry run by
// default — it must never delete without --yes.
func TestPruneIsDryRunByDefault(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("dead-one", map[string]any{
		"name": "Dead One", "dir": "/tmp", "sessionId": "no-such-transcript",
	})

	r := e.mustRun("prune")
	mustContain(t, r.out(), "Dead One", "should list the dead topic")
	if !e.exists("dead-one", "topic.json") {
		t.Fatal("prune deleted without --yes")
	}

	e.mustRun("prune", "--yes")
	if e.exists("dead-one", "topic.json") {
		t.Error("prune --yes did not remove the dead topic")
	}
}

// adopt gained --dry-run because identifying a session used to require adopting it.
func TestAdoptDryRunRegistersNothing(t *testing.T) {
	e := newEnv(t)
	const sid = "resumable-sid"
	e.seedTranscript("-tmp", sid, []string{
		`{"type":"custom-title","customTitle":"Resumable Thing","sessionId":"` + sid + `"}`,
		`{"type":"user","cwd":"/tmp"}`,
	})

	r := e.mustRun("adopt", "Resumable Thing", "--dry-run")
	mustContain(t, r.out(), "would adopt", "dry run should say what it would do")
	if e.exists("resumable-thing", "topic.json") {
		t.Error("--dry-run registered the topic anyway")
	}

	// And without --dry-run it really does adopt, by transcript title.
	e.mustRun("adopt", "Resumable Thing")
	if got := e.readTopic("resumable-thing")["sessionId"]; got != sid {
		t.Errorf("sessionId = %v, want %s", got, sid)
	}
}
