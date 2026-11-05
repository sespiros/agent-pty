package agentpty

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/sespiros/agent-pty/internal/term"
)

// stubEnv marks the re-executed process that leads the pty session.
const stubEnv = "AGENTPTY_STUB"

// The kernel discards SIGTSTP for a process group with no parent in its own
// session, so an agent that led the pty session itself could not be
// suspended. Like a job-control shell, the stub leads the session, runs the
// agent as its foreground job in a group of its own, and stops itself when
// the agent stops, which the relay sees. Go cannot run code between fork and
// exec, so Run starts its own executable with stubEnv set and this init
// turns that process into the stub before main runs.
func init() {
	if os.Getenv(stubEnv) != "1" {
		return
	}
	os.Unsetenv(stubEnv)
	os.Exit(stub(os.Args[1:]))
}

func stub(argv []string) int {
	if len(argv) == 0 {
		return 2
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-pty: %s: %v\r\n", argv[0], err)
		return 127
	}
	attr := &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
		// Foreground makes the child set itself as the terminal's foreground
		// group before exec, with signals blocked, so SIGTTOU cannot stop it.
		Sys: &syscall.SysProcAttr{Foreground: true, Ctty: 0},
	}
	pid, err := syscall.ForkExec(path, argv, attr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-pty: %s: %v\r\n", argv[0], err)
		return 126
	}
	// After the fork, so the agent keeps the default action. The stub is now
	// in a background group and must still hand the terminal back.
	signal.Ignore(syscall.SIGTTOU)
	forward := make(chan os.Signal, 4)
	signal.Notify(forward, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		for s := range forward {
			_ = syscall.Kill(-pid, s.(syscall.Signal))
		}
	}()
	return await(pid, func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		_ = term.SetForeground(0, pid)
		_ = syscall.Kill(-pid, syscall.SIGCONT)
	}, func(sig syscall.Signal) {
		signal.Reset(sig)
		_ = syscall.Kill(os.Getpid(), sig)
	})
}
