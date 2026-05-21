//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess marks cmd so its child runs in a new session, surviving the
// parent's exit. Without this, Claude Code's SessionEnd hook would wait for
// the LLM extract call to finish.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
