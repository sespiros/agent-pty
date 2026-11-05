# agent-pty

Claude Code, Codex and Pi report their state with OSC 7501, the [Program Status Protocol](https://www.superlogical.com/rex/docs/build/program-status): idle, working, blocked on you, done, or failed.
They send the reports only after the terminal answers the protocol's probe, and tmux, screen and most terminals do not answer.

agent-pty runs a program on a pseudo-terminal of its own, answers the probe, keeps the records the program reports, and hands a summary of them to observers.
Every other byte passes through unchanged.

It is a Go library first.
[notchify](https://github.com/sespiros/notchify) uses it to turn the reports into notifications.
The `agent-pty` command uses it to show the status in tmux.

## Library

```go
import "github.com/sespiros/agent-pty/agentpty"

code, err := agentpty.Run(ctx, []string{"claude"}, myObserver)
```

An observer gets an `agentpty.Status` on every change: the state, the blocked kind and message, the app, how many child records (subagents) are busy, and the window title the program set.
`tmuxstatus.New(pane)` is an observer that writes the status to tmux window options.
See [DESIGN.md](DESIGN.md) for the layers and the decisions behind them.

## Command

```sh
go install github.com/sespiros/agent-pty/cmd/agent-pty@latest
agent-pty claude            # inside tmux: status in @agent_state, @agent_msg, ...
agent-pty status working --app deploy --msg "release"
agent-pty job -- make release   # working while it runs, then done or error
```

Outside tmux, when stdin is not a terminal, or with `AGENT_PTY=0`, `agent-pty` runs the command directly.

## tmux status line

`agent-pty` (and `notchify wrap --tmux-status`) write these window options:

| option | value |
|---|---|
| `@agent_state` | `idle`, `working`, `blocked`, `done` or `error` |
| `@agent_kind` | `permission`, `question` or `auth`, while blocked |
| `@agent_msg` | the agent's one-line message |
| `@agent_subagents` | child records still working or blocked (Claude Code reports one per subagent); unset when 0 |
| `@agent_shells` | Claude Code background shells still running; unset when 0 |
| `@agent_pty` | the relay's pid while it runs |

Show them in front of each window name, for example in `~/.tmux.conf`:

```tmux
set -g window-status-format ' #{?#{==:#{@agent_state},working},#[fg=green]▶#[default] ,#{?#{==:#{@agent_state},blocked},#[fg=red]!#[default] ,#{?#{==:#{@agent_state},done},#[fg=blue]✓#[default] ,#{?#{==:#{@agent_state},error},#[fg=red]✗#[default] ,}}}}#{?#{>:#{@agent_subagents},0},» ,}#{?#{>:#{@agent_shells},0},◷ ,}#W '
set -g window-status-current-format ' #{?#{==:#{@agent_state},working},#[fg=green]▶#[default] ,#{?#{==:#{@agent_state},blocked},#[fg=red]!#[default] ,#{?#{==:#{@agent_state},done},#[fg=blue]✓#[default] ,#{?#{==:#{@agent_state},error},#[fg=red]✗#[default] ,}}}}#{?#{>:#{@agent_subagents},0},» ,}#{?#{>:#{@agent_shells},0},◷ ,}#W '
```

Done and error stay after the agent exits, since the result is still unread; clear them with `tmux set -wu @agent_state`, or from a hook such as `session-window-changed` when the window is viewed.

## Dependencies

[pst](https://github.com/adamvduke/pst) parses the reports and keeps the records.
Both use only the Go standard library.

## License

GPL-3.0. See [LICENSE](LICENSE).
