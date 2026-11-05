package agentpty

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// WORKAROUND: Claude Code sends no OSC 7501 record for a run_in_background
// shell, so the relay counts the processes. Drop when Claude reports
// background shells as child records.
// Claude Code runs each Bash command as `zsh -c 'source .../shell-snapshots/...'`,
// a direct child of its own process. No hook shell carries the mark.
const shellMark = "shell-snapshots/"

type proc struct {
	ppid int
	args string
}

// processRows lists every process, or returns nil when ps fails.
func processRows() map[int]proc {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil
	}
	rows := map[int]proc{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		rows[pid] = proc{ppid: ppid, args: strings.Join(f[2:], " ")}
	}
	return rows
}

// runningShells counts the agent's shell commands under root, without the
// shells they start themselves.
func runningShells(rows map[int]proc, root int) int {
	children := map[int][]int{}
	for pid, p := range rows {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	type item struct {
		pid    int
		inside bool
	}
	count, stack := 0, []item{{root, false}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		marked := strings.Contains(rows[it.pid].args, shellMark)
		if marked && !it.inside {
			count++
		}
		for _, c := range children[it.pid] {
			stack = append(stack, item{c, it.inside || marked})
		}
	}
	return count
}
