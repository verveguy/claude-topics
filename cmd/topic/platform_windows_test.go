//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What topic passes to MSYS2 programs must arrive exactly: seed prompts quote topic
// names, daemon start lines quote their echo, tmux formats contain #{...}. Go's own
// Windows quoting mangled the first two (a quoted phrase with spaces arrived as
// \Ab\). This runs MSYS's printf through msysCmd and compares.
func TestMsysCmdArgumentsArriveExactly(t *testing.T) {
	printf := filepath.Join(filepath.Dir(tmuxBin()), "printf.exe")
	if _, err := os.Stat(printf); err != nil {
		t.Skip("MSYS2 printf not found next to tmux")
	}
	args := []string{
		`You are starting the topic "ZZ a b". Brief at C:\x\y`,
		`/usr/bin/bash -lic 'sleep 1; echo "[topic] daemon a b exited with status $?"'; exec /usr/bin/bash -l`,
		`#{pane_current_command}`,
		`it's`,
		`back\slash\\ "quote" end\`,
		`C:\Users\me\.claude-personal`,
		`\\server\share`,
		`trailing\\`,
		``,
	}
	out, err := msysCmd(printf, append([]string{`[%s]\n`}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, a := range args {
		want.WriteString("[" + a + "]\n")
	}
	if string(out) != want.String() {
		t.Errorf("arguments did not arrive intact:\n got %q\nwant %q", out, want.String())
	}
}
