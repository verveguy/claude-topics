package main

import (
	"strings"
	"testing"
)

// The keeper's decision table: the whole point is that a husk (pane back at a shell)
// is restarted and a deliberate stop is respected. Pure, so no tmux needed.
func TestDaemonAction(t *testing.T) {
	cases := []struct {
		stopped bool
		state   string
		want    string
	}{
		{false, daemonRunning, "none"},
		{false, daemonHusk, "start"},
		{false, daemonMissing, "create-and-start"},
		{true, daemonRunning, "skip"},
		{true, daemonHusk, "skip"},
		{true, daemonMissing, "skip"},
	}
	for _, c := range cases {
		got := daemonAction(daemonSpec{Name: "d", Stopped: c.stopped}, c.state)
		if got != c.want {
			t.Errorf("stopped=%v state=%s: got %q, want %q", c.stopped, c.state, got, c.want)
		}
	}
}

func TestDaemonRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if ds, err := loadDaemons(dir); err != nil || ds != nil {
		t.Fatalf("missing registry should be empty, got %v, %v", ds, err)
	}
	in := []daemonSpec{
		{Name: "zeta", Dir: "/z", Command: "daemon-env fabrik --auto-upgrade"},
		{Name: "alpha", Dir: "/a", Command: "daemon-env pruefer", Stopped: true},
	}
	if err := saveDaemons(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := loadDaemons(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Name != "alpha" || out[1].Name != "zeta" {
		t.Fatalf("expected sorted round trip, got %+v", out)
	}
	if !out[0].Stopped || out[1].Stopped {
		t.Errorf("stopped flags not preserved: %+v", out)
	}
}

// An exit must be visible in the pane: four daemons once exited without logging why.
func TestStartLineReportsExit(t *testing.T) {
	l := startLine(daemonSpec{Name: "liminis-daemon", Command: "daemon-env fabrik --auto-upgrade"})
	if !strings.HasPrefix(l, "daemon-env fabrik --auto-upgrade; ") || !strings.Contains(l, "exited with status $?") {
		t.Errorf("startLine = %q", l)
	}
}
