// Command agent-pty gives a coding agent in tmux the terminal status
// protocol (OSC 7501) and shows its status in tmux window options.
//
//	agent-pty [--] COMMAND [ARG...]
//	agent-pty status STATE [--kind KIND] [--msg TEXT] [--app NAME]
//	agent-pty job [--app NAME] [--msg TEXT] -- COMMAND [ARG...]
//
// Outside tmux, when stdin or stdout is not a terminal, with AGENT_PTY=0, or
// inside another agent-pty, it runs COMMAND directly: a terminal that speaks
// the protocol then gets the reports itself.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"unicode"

	"github.com/adamvduke/pst"
	"github.com/sespiros/agent-pty/agentpty"
	"github.com/sespiros/agent-pty/tmuxstatus"
)

func main() {
	args := os.Args[1:]
	switch {
	case len(args) > 0 && args[0] == "status":
		os.Exit(status(args[1:]))
	case len(args) > 0 && args[0] == "job":
		os.Exit(job(args[1:]))
	case len(args) > 0 && args[0] == "--":
		args = args[1:]
	}
	os.Exit(run(args))
}

func run(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agent-pty [--] COMMAND [ARG...]")
		return 2
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || os.Getenv("TMUX") == "" || os.Getenv("AGENT_PTY") == "0" || agentpty.Nested() ||
		!agentpty.Interactive() {
		err := agentpty.Exec(argv)
		fmt.Fprintf(os.Stderr, "agent-pty: %s: %v\n", argv[0], err)
		return 127
	}
	code, err := agentpty.Run(context.Background(), argv, tmuxstatus.New(pane))
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-pty:", err)
	}
	return code
}

// clean removes control characters, which the protocol refuses, and cuts the
// text to limit bytes of UTF-8.
func clean(text string, limit int) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) {
			continue
		}
		if b.Len()+len(string(r)) > limit {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func status(args []string) int {
	fs := flag.NewFlagSet("agent-pty status", flag.ContinueOnError)
	kind := fs.String("kind", "", "permission, question or auth (blocked only)")
	msg := fs.String("msg", "", "one-line message")
	app := fs.String("app", "", "program name")
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agent-pty status STATE [--kind KIND] [--msg TEXT] [--app NAME]")
		return 2
	}
	state := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	r := pst.Report{State: pst.State(state), App: *app, Msg: clean(*msg, pst.MaxMsgBytes)}
	if r.State == pst.StateBlocked {
		r.Kind = pst.Kind(*kind)
	}
	// One validation path: what status sends is what a terminal accepts.
	seq, err := r.Encode()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-pty: the report breaks the OSC 7501 grammar or limits:", err)
		return 2
	}
	if agentpty.Nested() || os.Getenv("TERM_PROGRAM") == "rex" {
		tty, err := os.OpenFile("/dev/tty", os.O_WRONLY|syscall.O_NOCTTY, 0)
		if err != nil {
			return 0
		}
		defer tty.Close()
		tty.Write(seq)
		return 0
	}
	// CUSTOM CONFIG: a plain tmux pane has no reader for the sequence, so
	// status writes the options itself, the same way the relay does.
	if pane := os.Getenv("TMUX_PANE"); pane != "" && os.Getenv("TMUX") != "" {
		s := agentpty.Status{State: string(r.State), Kind: string(r.Kind), Msg: r.Msg, App: r.App}
		if r.State == pst.StateClear {
			s = agentpty.Status{}
		}
		_ = tmuxstatus.Write(pane, s)
	}
	return 0
}

// CUSTOM CONFIG: job is an agent-pty command, not part of OSC 7501.
func job(args []string) int {
	split := -1
	for i, a := range args {
		if a == "--" {
			split = i
			break
		}
	}
	if split < 0 || split == len(args)-1 {
		fmt.Fprintln(os.Stderr, "usage: agent-pty job [--app NAME] [--msg TEXT] -- COMMAND [ARG...]")
		return 2
	}
	fs := flag.NewFlagSet("agent-pty job", flag.ContinueOnError)
	app := fs.String("app", "job", "program name")
	msg := fs.String("msg", "", "message (default: the command)")
	if err := fs.Parse(args[:split]); err != nil {
		return 2
	}
	argv := args[split+1:]
	text := *msg
	if text == "" {
		text = strings.Join(argv, " ")
	}
	status([]string{"working", "--app", *app, "--msg", text})
	// The terminal sends Ctrl-C to the command too; wait for its own exit.
	// Caught, not ignored: an ignored signal stays ignored across exec, and
	// the command must still get Ctrl-C.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	signal.Stop(interrupts)
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		} else {
			code = exit.ExitCode()
		}
	case err != nil:
		fmt.Fprintf(os.Stderr, "agent-pty: %s: %v\n", argv[0], err)
		code = 127
	}
	if code == 0 {
		status([]string{"done", "--app", *app, "--msg", text})
	} else {
		status([]string{"error", "--app", *app, "--msg", fmt.Sprintf("%s (exit %d)", text, code)})
	}
	return code
}
