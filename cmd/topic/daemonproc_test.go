package main

import (
	"testing"
	"time"
)

// fakeProcs stands in for ps and kill: pid -> name and start time.
type fakeProcs struct {
	procs    map[int][2]string
	children map[int][]int
	panes    map[string][]int
	killed   []int
}

func (f *fakeProcs) install(t *testing.T) {
	t.Helper()
	ol, ot, op, oc := lookupProc, terminatePID, panePIDs, childPIDs
	t.Cleanup(func() { lookupProc, terminatePID, panePIDs, childPIDs = ol, ot, op, oc })
	lookupProc = func(pid int) (string, string, bool) {
		p, ok := f.procs[pid]
		return p[0], p[1], ok
	}
	terminatePID = func(pid int) error {
		f.killed = append(f.killed, pid)
		delete(f.procs, pid) // exits at once
		return nil
	}
	panePIDs = func(s string) []int { return f.panes[s] }
	childPIDs = func(pid int) []int { return f.children[pid] }
}

func TestDaemonProcessesWalksThroughShells(t *testing.T) {
	f := &fakeProcs{
		procs: map[int][2]string{
			10: {"-zsh", "t0"},          // the pane's login shell
			11: {"/bin/zsh", "t1"},      // zsh -lic wrapper
			12: {"./fabrik", "t2"},      // the daemon
			13: {"fabrik-worker", "t3"}, // its child: not a separate daemon process
		},
		children: map[int][]int{10: {11}, 11: {12}, 12: {13}},
		panes:    map[string][]int{"fab": {10}},
	}
	f.install(t)
	got := daemonProcesses("fab")
	if len(got) != 1 || got[0] != (procEntry{12, "t2"}) {
		t.Fatalf("daemonProcesses = %+v, want just the daemon", got)
	}
}

// The 2026-11-22 case: kill-server left the daemon running, reparented away from any
// pane. Only the very process the keeper saw before counts: a pid since reused by
// something else does not.
func TestOrphansAreRememberedProcessesOutsideThePane(t *testing.T) {
	f := &fakeProcs{procs: map[int][2]string{
		20: {"fabrik", "Sun 10:20"}, // the orphan
		21: {"vim", "Sun 11:00"},    // pid reused since
		22: {"fabrik", "Sun 12:00"}, // in the pane now
	}}
	f.install(t)
	remembered := []procEntry{{20, "Sun 10:20"}, {21, "Sun 09:00"}, {22, "Sun 12:00"}, {23, "gone"}}
	got := orphans(remembered, []procEntry{{22, "Sun 12:00"}})
	if len(got) != 1 || got[0].PID != 20 {
		t.Fatalf("orphans = %+v, want only pid 20", got)
	}
	left, _ := stopProcesses(got, time.Second)
	if len(left) != 0 || len(f.killed) != 1 || f.killed[0] != 20 {
		t.Fatalf("left %+v, killed %v", left, f.killed)
	}
}

func TestStopProcessesReportsStragglers(t *testing.T) {
	f := &fakeProcs{procs: map[int][2]string{30: {"stubborn", "t"}}}
	f.install(t)
	terminatePID = func(int) error { return nil } // ignores SIGTERM
	left, _ := stopProcesses([]procEntry{{30, "t"}}, 300*time.Millisecond)
	if len(left) != 1 || left[0].PID != 30 {
		t.Fatalf("left = %+v, want the straggler", left)
	}
}

func TestRememberDaemonRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rememberDaemon(dir, "fab", []procEntry{{12, "t2"}})
	rememberDaemon(dir, "pru", []procEntry{{14, "t4"}})
	rememberDaemon(dir, "fab", nil) // nothing seen: keep what was remembered
	m := loadDaemonPIDs(dir)
	if len(m["fab"]) != 1 || m["fab"][0].PID != 12 || m["pru"][0].PID != 14 {
		t.Fatalf("remembered = %+v", m)
	}
}
