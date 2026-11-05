// Package tmuxstatus is an agentpty.Observer that writes the status to tmux
// window options, for a status line format to read.
package tmuxstatus

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
	"unicode"

	"github.com/sespiros/agent-pty/agentpty"
)

// The options this package writes. They are the interface to the tmux
// status format; a change here needs the same change in the format.
const (
	State     = "@agent_state"     // idle, working, blocked, done, error
	Kind      = "@agent_kind"      // permission, question, auth, while blocked
	Msg       = "@agent_msg"       // the one-line message, without invisible formatting
	Subagents = "@agent_subagents" // child records still working or blocked, unset when 0
	Shells    = "@agent_shells"    // background shells still running, unset when 0
	Pty       = "@agent_pty"       // the relay's pid while it runs
)

var options = []string{State, Kind, Msg, Subagents, Shells, Pty}

// MsgLimit is how many characters of the message the status line gets.
const MsgLimit = 120

// Observer applies option changes in order on a worker goroutine, off the
// relay's I/O path. An empty value unsets the option.
type Observer struct {
	pane  string
	shown map[string]string
	known bool
	jobs  chan []string
	done  chan struct{}
}

// New starts an observer for a pane. It first unsets every option, since the
// window can still hold a done or error result an earlier run left, and sets
// @agent_pty to this process's pid.
func New(pane string) *Observer {
	o := &Observer{pane: pane, shown: map[string]string{}, jobs: make(chan []string, 64), done: make(chan struct{})}
	go o.run()
	initial := []pair{}
	for _, k := range options {
		initial = append(initial, pair{k, ""})
	}
	initial[len(initial)-1].value = strconv.Itoa(os.Getpid())
	o.apply(initial, true)
	return o
}

func (o *Observer) run() {
	defer close(o.done)
	for argv := range o.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
		cancel()
	}
}

type pair struct{ key, value string }

func view(s agentpty.Status) []pair {
	count := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	return []pair{
		{State, s.State},
		{Kind, s.Kind},
		{Msg, Shown(s.Msg)},
		{Subagents, count(s.Subagents)},
		{Shells, count(s.Shells)},
	}
}

// apply queues the pairs that differ from what is shown, or all of them.
func (o *Observer) apply(pairs []pair, all bool) {
	var changes []pair
	for _, p := range pairs {
		if all || o.shown[p.key] != p.value {
			o.shown[p.key] = p.value
			changes = append(changes, p)
		}
	}
	if len(changes) > 0 {
		o.jobs <- commands(o.pane, changes)
	}
}

// Update implements agentpty.Observer.
func (o *Observer) Update(s agentpty.Status) { o.apply(view(s), false) }

// Close shows what survives the exit, unsets the rest and @agent_pty, and
// waits up to five seconds for tmux.
func (o *Observer) Close(s agentpty.Status) {
	final := append(view(s), pair{Pty, ""})
	o.apply(final, true)
	close(o.jobs)
	select {
	case <-o.done:
	case <-time.After(5 * time.Second):
	}
}

// Write sets the options for one status in a pane, once, without a relay:
// for a script in a plain tmux pane. It leaves @agent_shells and @agent_pty
// alone.
func Write(pane string, s agentpty.Status) error {
	argv := commands(pane, view(s)[:4])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
}

// commands is one tmux invocation that applies the pairs in order, then
// redraws the status line.
func commands(pane string, pairs []pair) []string {
	argv := []string{"tmux"}
	for i, p := range pairs {
		if i > 0 {
			argv = append(argv, ";")
		}
		if p.value == "" {
			argv = append(argv, "set-option", "-w", "-q", "-u", "-t", pane, p.key)
		} else {
			argv = append(argv, "set-option", "-w", "-q", "-t", pane, p.key, p.value)
		}
	}
	return append(argv, ";", "refresh-client", "-S")
}

// Shown is a message as the status line shows it: without control or
// invisible formatting characters such as text direction overrides, and cut
// to MsgLimit characters.
func Shown(text string) string {
	out := make([]rune, 0, len(text))
	for _, r := range text {
		if unicode.IsPrint(r) {
			out = append(out, r)
			if len(out) == MsgLimit {
				break
			}
		}
	}
	return string(out)
}
