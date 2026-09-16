package test

import (
	"os"
	"os/exec"
	"runtime"
)

// tmuxCommand runs tmux the way the CLI does, so live-test cleanup reaches the same
// server. On Windows that means MSYS2's tmux, with MSYS=noglob: the MSYS2 runtime
// otherwise glob-expands arguments passed from a native process like this test binary.
func tmuxCommand(args ...string) *exec.Cmd {
	if runtime.GOOS != "windows" {
		return exec.Command("tmux", args...)
	}
	bin := os.Getenv("TOPIC_TMUX")
	if bin == "" {
		if p, err := exec.LookPath("tmux"); err == nil {
			bin = p
		} else {
			bin = `C:\msys64\usr\bin\tmux.exe`
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "MSYSTEM=MSYS", "MSYS2_PATH_TYPE=inherit", "MSYS=noglob")
	return cmd
}
