// Whether this binary can hold a macOS permission decision.
//
// The problem, in full, because it is not guessable from the symptom:
//
// launchd starts `topic ensure-dispatcher` at login, before any terminal is open. So
// `topic` is what creates the tmux server, and that makes it the RESPONSIBLE PROCESS
// for everything underneath: every Claude session, every MCP server, every hook, every
// `find` a session runs. Those are unsigned interpreters (node, sh, find), so macOS
// attributes their TCC requests to topic's binary. The dialog then says
//
//	"topic" would like to access data from other apps.
//
// and topic is not the one asking. Before a reboot you never see this, because your
// terminal emulator started the tmux server and already held those grants.
//
// The second half is why clicking the dialog does nothing. The Go linker signs ad-hoc
// with Identifier=a.out — not a unique identity, since every ad-hoc Go binary on the
// machine claims it — and TCC will not persist a decision against it. The stored
// answer stays "unknown" (authValue=1) and the dialog returns forever, whether the
// user clicked Allow or Don't Allow.
//
// scripts/build.sh fixes both halves: it signs with a stable identifier, so one answer
// sticks, and it stamps that identifier in at link time so this file can tell whether
// it was used. A bare `go build` leaves signedIdentifier empty, and we say so rather
// than let the user rediscover the whole chain above from a permission dialog.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// defaultIdentifier is what scripts/build.sh signs with unless TOPIC_IDENTIFIER says
// otherwise. Named here only so `doctor` can print a `tccutil reset` line that works
// on a binary built without the stamp — which is exactly when it cannot read it back.
const defaultIdentifier = "io.github.verveguy.topic"

// signedIdentifier is stamped in at link time by scripts/build.sh:
//
//	go build -ldflags "-X main.signedIdentifier=io.github.verveguy.topic"
//
// Empty means the binary came from a bare `go build`.
var signedIdentifier string

// signingNotice is the one-line warning for a binary built outside the supported
// path, or "" when there is nothing to say. Only macOS has TCC, so only macOS cares.
func signingNotice() string {
	if runtime.GOOS != "darwin" || signedIdentifier != "" {
		return ""
	}
	return "topic: built without a stable code-signing identity, so macOS will re-ask for\n" +
		"       permissions forever (and name `topic` for requests it did not make).\n" +
		"       Rebuild with `make build`.  Detail: topic doctor"
}

// warnIfUnsigned prints that notice on stderr. Called from the commands that create
// tmux sessions, because those are the ones that make topic the responsible process
// and therefore the ones where the missing identity starts costing something.
func warnIfUnsigned() {
	if n := signingNotice(); n != "" {
		fmt.Fprintln(os.Stderr, n)
	}
}

// codesignIdentifier asks macOS what identity a binary actually carries. Reported
// separately from the stamped value so `doctor` can catch the case where the two
// disagree — a rebuild that skipped signing, or a re-signed binary from elsewhere.
// codesign writes its detail to stderr, hence CombinedOutput.
func codesignIdentifier(path string) string {
	out, err := exec.Command("codesign", "-d", "--verbose=2", path).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Identifier="); ok {
			return v
		}
	}
	return ""
}

// cmdDoctor reports whether this binary can hold a macOS permission decision, and
// says what to do when it cannot.
func cmdDoctor(_ []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	stamped := signedIdentifier
	if stamped == "" {
		stamped = "(none — built with a bare `go build`)"
	}
	fmt.Printf("  binary:        %s\n", exe)
	fmt.Printf("  stamped id:    %s\n", stamped)

	if runtime.GOOS != "darwin" {
		fmt.Println("\n  Not macOS: there is no TCC here, and nothing to fix.")
		return nil
	}

	actual := codesignIdentifier(exe)
	shown := actual
	if shown == "" {
		shown = "(unsigned)"
	}
	fmt.Printf("  signing id:    %s\n", shown)
	fmt.Println()

	switch {
	case actual == "" || actual == "a.out":
		fmt.Println("  PROBLEM: this binary has no identity macOS can key a permission")
		fmt.Println("  decision against, so every dialog naming `topic` will come back —")
		fmt.Println("  whether you click Allow or Don't Allow.")
		fmt.Println()
		fmt.Println("  Fix:  make build")
		fmt.Println()
		fmt.Println("  Then restart the tmux server so sessions are re-parented onto the")
		fmt.Println("  signed binary (this ends every session, daemons included):")
		fmt.Println("    topic cycle down && tmux kill-server")
		fmt.Println("    tccutil reset All " + defaultIdentifier)
		fmt.Println("    topic cycle up")

	case signedIdentifier != "" && actual != signedIdentifier:
		fmt.Printf("  PROBLEM: signed as %q but built expecting %q.\n", actual, signedIdentifier)
		fmt.Println("  Something re-signed this binary after the build. Rebuild: make build")

	default:
		fmt.Println("  OK: this binary can hold a permission decision.")
		fmt.Println()
		fmt.Println("  If dialogs still name `topic`, the running tmux server predates the")
		fmt.Println("  signed binary and its sessions are still parented onto the old one:")
		fmt.Println("    topic cycle down && tmux kill-server && topic cycle up")
		fmt.Println()
		fmt.Println("  Answer the next dialog with Don't Allow. It sticks now, and a grant")
		fmt.Println("  to `topic` is inherited by every session and MCP server it spawns.")
	}
	return nil
}
