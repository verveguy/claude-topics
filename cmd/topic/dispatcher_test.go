package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Dispatcher used to default to $HOME, which made an unqualified search inside it
// mean "walk everything you own" — and on macOS every protected folder that walk
// reached raised a TCC prompt naming `topic`, because topic is the responsible process
// for the tmux tree. Found 2026-08-31, alongside the signing problem in signing.go.
//
// A unit test rather than a CLI test because the CLI path launches a real session: the
// choice of directory is the only part worth pinning, and it is a pure function.
func TestDispatcherDirIsNotHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot resolve home: %v", err)
	}
	t.Setenv("CLAUDE_DISPATCHER_DIR", "")

	configDir := t.TempDir()
	got := dispatcherDir(configDir)

	if got == home {
		t.Fatalf("dispatcherDir returned $HOME (%s) — a search there walks Photos, Documents and every other protected folder", got)
	}
	want := filepath.Join(configDir, "dispatcher")
	if got != want {
		t.Errorf("dispatcherDir = %q, want %q", got, want)
	}
	// It must exist: tmux new-session -c fails on a missing directory, and the
	// Dispatcher is started unattended by launchd where that failure is invisible.
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("dispatcherDir did not create %s: %v", got, err)
	}
}

// When <profile>/dispatcher cannot be created — a read-only volume, a CLAUDE_CONFIG_DIR
// that does not exist yet — the fallback must still not be $HOME. It used to be, which
// silently reintroduced the TCC bug above in exactly the unattended launchd context
// where nobody would notice. (Review feedback on #2.)
func TestDispatcherDirFallbackIsNotHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot resolve home: %v", err)
	}
	t.Setenv("CLAUDE_DISPATCHER_DIR", "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // keep the fallback inside the test's own temp dir

	// A regular file where the config dir should be makes MkdirAll fail.
	configDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(configDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got := dispatcherDir(configDir)
	if got == home {
		t.Fatalf("dispatcherDir fell back to $HOME (%s) when the profile dir was unusable", got)
	}
	if !strings.HasPrefix(got, tmp) {
		t.Errorf("dispatcherDir = %q, want a fallback under the temp dir %q", got, tmp)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("fallback %s was not created: %v", got, err)
	}
}

// An explicit choice wins, so anyone who wants the Dispatcher somewhere specific — a
// repo, a scratch directory — can say so without editing the registry.
func TestDispatcherDirHonoursOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv("CLAUDE_DISPATCHER_DIR", want)

	if got := dispatcherDir(t.TempDir()); got != want {
		t.Errorf("dispatcherDir = %q, want the CLAUDE_DISPATCHER_DIR override %q", got, want)
	}
}
