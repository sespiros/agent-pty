// Package agentpty runs a program on a pty of its own and gives it a
// terminal that speaks OSC 7501, the Program Status Protocol: it answers the
// probe, keeps the records the program reports, and hands a summary of them
// to observers. See DESIGN.md.
package agentpty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/adamvduke/pst"
	"github.com/sespiros/agent-pty/internal/term"
)

// ActiveEnv is set to 1 in the environment of every program Run starts, so
// hooks and nested wrappers can tell that the relay covers the terminal.
const ActiveEnv = "AGENT_PTY_ACTIVE"

const (
	scanDelay    = time.Second     // after a report, before the first process scan
	scanInterval = 2 * time.Second // between scans while shells run
	drainIdle    = 50 * time.Millisecond
)

// ErrNotTerminal means stdin is not a terminal, so there is nothing to relay.
var ErrNotTerminal = errors.New("agentpty: stdin is not a terminal")

// Nested reports whether this process already runs under a relay.
func Nested() bool { return os.Getenv(ActiveEnv) == "1" }

// Interactive reports whether stdin and stdout are terminals, which Run
// needs.
func Interactive() bool { return term.IsTerminal(0) && term.IsTerminal(1) }

// Exec replaces this process with argv, for callers that decide not to
// relay. It returns only on failure.
func Exec(argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}

type relay struct {
	master    *os.File
	stub      int
	scanner   scanner
	store     *pst.Store
	observers []Observer
	last      Status
	shells    int
	scanDue   time.Time
}

// Run runs argv on a new pty and relays it to this process's terminal until
// it exits. It returns the exit code in the shell convention: 128 plus the
// signal number when a signal ended it.
func Run(ctx context.Context, argv []string, observers ...Observer) (int, error) {
	if len(argv) == 0 {
		return 2, errors.New("agentpty: no command")
	}
	saved, err := term.Get(0)
	if err != nil {
		return 1, ErrNotTerminal
	}
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	master, slave, err := term.Open()
	if err != nil {
		return 1, err
	}
	defer master.Close()
	_ = term.Set(int(slave.Fd()), saved)
	term.CopySize(0, int(slave.Fd()))

	env := []string{stubEnv + "=1", ActiveEnv + "=1"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, stubEnv+"=") && !strings.HasPrefix(kv, ActiveEnv+"=") {
			env = append(env, kv)
		}
	}
	sfd := slave.Fd()
	pid, err := syscall.ForkExec(exe, append([]string{exe}, argv...), &syscall.ProcAttr{
		Env:   env,
		Files: []uintptr{sfd, sfd, sfd},
		Sys:   &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0},
	})
	slave.Close()
	if err != nil {
		return 1, err
	}

	r := &relay{master: master, stub: pid, store: pst.NewStore(), observers: observers}
	_ = term.Set(0, term.Raw(saved))
	defer term.Set(0, saved)

	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGWINCH, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer signal.Stop(signals)

	output := make(chan []byte, 16)
	go func() {
		defer close(output)
		for {
			buf := make([]byte, 65536)
			n, err := master.Read(buf)
			if n > 0 {
				output <- buf[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	// The goroutine stays blocked on stdin after the command exits; the
	// caller exits soon after, and nothing reads stdin in between.
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if _, werr := master.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan int, 1)
	go func() { exited <- r.wait(saved) }()

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	code := -1
	var idle <-chan time.Time
	for {
		select {
		case data, ok := <-output:
			if !ok {
				if code < 0 {
					code = <-exited
				}
				return r.finish(code), nil
			}
			r.handle(data)
			if code >= 0 {
				idle = time.After(drainIdle)
			}
		case code = <-exited:
			// Output the command wrote just before it exited.
			idle = time.After(drainIdle)
		case <-idle:
			return r.finish(code), nil
		case s := <-signals:
			if s == syscall.SIGWINCH {
				term.CopySize(0, int(master.Fd()))
			} else {
				_ = syscall.Kill(-pid, s.(syscall.Signal))
			}
		case <-tick.C:
			r.scan()
		case <-ctx.Done():
			_ = syscall.Kill(-pid, syscall.SIGHUP)
		}
	}
}

// wait reaps the stub and returns its exit code. When the stub stops (the
// agent was suspended), it gives the terminal back, stops this process too,
// and resumes the stub when this process is continued.
func (r *relay) wait(saved *syscall.Termios) int {
	return await(r.stub, func() {
		_ = term.Set(0, saved)
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		_ = term.Set(0, term.Raw(saved))
		term.CopySize(0, int(r.master.Fd()))
		_ = syscall.Kill(-r.stub, syscall.SIGCONT)
	}, nil)
}

func (r *relay) handle(data []byte) {
	out, seqs := r.scanner.feed(data)
	done := 0
	for _, f := range seqs {
		os.Stdout.Write(out[done:f.offset])
		done = f.offset
		report, kind := parse(f.body, f.size)
		switch kind {
		case probe:
			// Answer before the rest of the output reaches the outer
			// terminal, so the reply lands ahead of its answer to the
			// agent's next query.
			r.master.Write(reply)
		case accepted:
			r.store.Apply(report)
			r.publish()
			if r.scanDue.IsZero() {
				r.scanDue = time.Now().Add(scanDelay)
			}
		}
	}
	os.Stdout.Write(out[done:])
}

func (r *relay) publish() {
	s := summarize(r.store, r.shells)
	if s == r.last {
		return
	}
	r.last = s
	for _, o := range r.observers {
		o.Update(s)
	}
}

// scan counts the agent's running shells when a scan is due. A report starts
// the scans; they stop when no shell runs. A shell that the turn leaves
// running is a background one.
func (r *relay) scan() {
	now := time.Now()
	if r.scanDue.IsZero() || now.Before(r.scanDue) {
		return
	}
	count := 0
	if rows := processRows(); rows != nil {
		count = runningShells(rows, r.stub)
	}
	r.scanDue = time.Time{}
	if count > 0 {
		r.scanDue = now.Add(scanInterval)
	}
	if count != r.shells {
		r.shells = count
		r.publish()
	}
}

// finish applies the exit to the records and closes the observers.
func (r *relay) finish(code int) int {
	r.store.ProcessExited()
	final := summarize(r.store, 0)
	for _, o := range r.observers {
		o.Close(final)
	}
	return code
}
