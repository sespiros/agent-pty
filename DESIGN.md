# agent-pty design

## Problem

Claude Code, Codex and Pi report their state with OSC 7501, the [Program Status Protocol](https://www.superlogical.com/rex/docs/build/program-status).
They send reports only after the terminal answers the probe `ESC ] 7501 ; ? ST`.
tmux, screen and most terminals do not answer, so the agents stay silent there.

agent-pty runs the agent on a pty of its own, answers the probe, keeps the records, and hands a summary of them to observers.
It is a library first: notchify imports it for popups, and its own small CLI uses it for tmux status.

## Layers

```
notchify wrap ─┐                       ┌─ tmuxstatus (window options)
cmd/agent-pty ─┴─ agentpty.Run(cmd, observers…) ─┼─ notchify popup observer
                        │                └─ anything that implements Observer
                        └─ pst (parse, record store)
```

- `github.com/adamvduke/pst` parses reports and keeps the record set with the spec's rules. It knows nothing about ptys.
- `agentpty` is the relay. `Run` starts the command on a new pty, copies bytes both ways, removes OSC 7501 sequences from the output, answers the probe, applies reports to a `pst.Store`, and calls each `Observer` when the `Status` changes.
- `tmuxstatus` is one `Observer`. It writes `@agent_state`, `@agent_kind`, `@agent_msg`, `@agent_subagents`, `@agent_shells` and `@agent_pty` as tmux window options.
- `cmd/agent-pty` is `agentpty.Run` with `tmuxstatus`, plus `status` and `job` for scripts.

## API

```go
type Status struct {
    State     string // idle, working, blocked, done, error; "" when there is no root record
    Kind      string // permission, question, auth; blocked only
    Msg       string // the root record's message, as the agent sent it
    App       string // the root record's app
    Subagents int    // child records that are working or blocked
    Shells    int    // background shells still running (Claude Code only)
    Title     string // the last window title the program set (OSC 0 or 2)
}

type Observer interface {
    Update(Status) // every change, on the relay's goroutine; must not block
    Close(Status)  // once, after the command exits, with the records that survive
}

func Run(ctx context.Context, argv []string, obs ...Observer) (int, error)
func Nested() bool // true inside another agent-pty
```

`Run` returns the command's exit code in the shell convention (128 + signal for a signal).
The caller decides whether to wrap at all: `cmd/agent-pty` wraps only inside tmux, `notchify wrap` wraps in any terminal that does not answer the probe itself.

## Decisions

- **Reports are removed from the output.** The relay answers the probe, so a terminal behind it that also speaks the protocol would get a second copy of every report and answer a second probe.
- **The job-control stub is a re-exec.** The kernel discards SIGTSTP for a process group with no parent in its session, so the agent cannot lead the pty session itself or Ctrl-Z stops nothing. A small stub leads the session and runs the agent as its foreground job. Go cannot run code between fork and exec, so `Run` starts its own executable with `AGENTPTY_STUB=1`, and an `init` function in `agentpty` turns that process into the stub. Programs that import `agentpty` need no extra call.
- **Background shells are counted from the process list.** Claude Code sends no record for a `run_in_background` shell. The relay counts direct descendants whose argv contains `shell-snapshots/`, after a report and then every two seconds while any run. Drop this when Claude reports them as child records.
- **A report with an invalid `id` is discarded.** pst v0.1.0 skips the malformed pair and applies the report to the root record, which the spec forbids. The relay checks the id itself until pst is fixed.
- **No dependencies beyond pst**, which uses only the standard library. The pty code calls `syscall` directly.

## Not covered

- Codex sends no message, no child records, and no blocked state under `approvals_reviewer=auto_review`.
- An agent started outside the wrapper reports nothing. Hooks remain the fallback for those.
