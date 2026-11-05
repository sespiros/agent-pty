package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sespiros/agent-pty/internal/term"
)

var bin, fake string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agent-pty-test")
	if err != nil {
		panic(err)
	}
	bin, fake = filepath.Join(dir, "agent-pty"), filepath.Join(dir, "fakeagent")
	for out, pkg := range map[string]string{bin: ".", fake: "./testdata/fakeagent"} {
		if b, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
			panic(string(b))
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t   *testing.T
	dir string
	log string
	env []string
}

func setup(t *testing.T) *env {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "tmux"), []byte("#!/bin/sh\nprintf \"%s\\n\" \"$*\" >> \"$TMUX_LOG\"\n"), 0o755)
	e := &env{t: t, dir: dir, log: filepath.Join(dir, "tmux.log")}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "AGENT_PTY_ACTIVE", "AGENT_PTY", "TERM_PROGRAM", "TMUX", "TMUX_PANE", "PATH":
			continue
		}
		e.env = append(e.env, kv)
	}
	e.env = append(e.env, "PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"),
		"TMUX=/tmp/fake,1,0", "TMUX_PANE=%7", "TMUX_LOG="+e.log)
	return e
}

func (e *env) without(keys ...string) []string {
	var out []string
	for _, kv := range e.env {
		k, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, x := range keys {
			keep = keep && k != x
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

func (e *env) lines() []string {
	b, _ := os.ReadFile(e.log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// runInPTY runs argv with a new pty as its controlling terminal and returns
// the exit code and everything it wrote.
func runInPTY(t *testing.T, env []string, argv ...string) (int, string) {
	t.Helper()
	master, slave, err := term.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	term.SetSize(int(slave.Fd()), 24, 80)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	var out bytes.Buffer
	read := make(chan struct{})
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := master.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				close(read)
				return
			}
		}
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var werr error
	select {
	case werr = <-waited:
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		t.Fatal("timed out")
	}
	select {
	case <-read:
	case <-time.After(time.Second):
	}
	code := 0
	if x, ok := werr.(*exec.ExitError); ok {
		code = x.ExitCode()
	}
	return code, out.String()
}

func TestReportsReachTmuxAndNotTheTerminal(t *testing.T) {
	e := setup(t)
	replyFile := filepath.Join(e.dir, "reply")
	code, out := runInPTY(t, e.env, bin, fake, "probe", replyFile, "3")
	if code != 3 {
		t.Fatalf("code %d, output %q", code, out)
	}
	got, _ := os.ReadFile(replyFile)
	if string(got) != "\x1b]7501;?:states=idle,working,done,blocked,error:kinds=permission,question,auth\x1b\\" {
		t.Errorf("reply %q", got)
	}
	if strings.Contains(out, "7501") || !strings.Contains(out, "beforemiddleafter") {
		t.Errorf("output %q", out)
	}
	log, _ := os.ReadFile(e.log)
	w, d := strings.Index(string(log), "@agent_state working"), strings.Index(string(log), "@agent_state done")
	if w < 0 || d < 0 || w > d {
		t.Errorf("order in %s", log)
	}
	if !strings.Contains(string(log), "@agent_msg All good") {
		t.Errorf("no message in %s", log)
	}
	lines := e.lines()
	last := lines[len(lines)-1]
	// The done result outlives the agent; the wrapper's own mark does not.
	for _, want := range []string{"-t %7 @agent_state done", "-u -t %7 @agent_pty", "-u -t %7 @agent_shells"} {
		if !strings.Contains(last, want) {
			t.Errorf("last line %q lacks %q", last, want)
		}
	}
}

func TestAShellLeftRunningIsCountedUntilItEnds(t *testing.T) {
	e := setup(t)
	if code, out := runInPTY(t, e.env, bin, fake, "shell"); code != 0 {
		t.Fatalf("code %d, output %q", code, out)
	}
	lines := e.lines()
	counted, cleared := -1, -1
	for i, l := range lines {
		if counted < 0 && strings.Contains(l, "@agent_shells 1") {
			counted = i
		}
		if counted >= 0 && i > counted && i < len(lines)-1 && strings.Contains(l, "-u -t %7 @agent_shells") {
			cleared = i
		}
	}
	if counted < 0 || cleared < 0 {
		t.Fatalf("counted %d cleared %d in %q", counted, cleared, lines)
	}
}

func TestSubagentsAreCountedAndABadIDLeavesTheRoot(t *testing.T) {
	e := setup(t)
	if code, out := runInPTY(t, e.env, bin, fake, "subagents"); code != 0 {
		t.Fatalf("code %d, output %q", code, out)
	}
	log, _ := os.ReadFile(e.log)
	if !strings.Contains(string(log), "@agent_subagents 2") || !strings.Contains(string(log), "@agent_subagents 1") {
		t.Errorf("counts in %s", log)
	}
	if strings.Contains(string(log), "@agent_state idle") {
		t.Errorf("the report with a bad id reached the root: %s", log)
	}
}

func TestANewRunClearsWhatAnEarlierRunLeft(t *testing.T) {
	e := setup(t)
	if code, _ := runInPTY(t, e.env, bin, "/bin/sh", "-c", "exit 0"); code != 0 {
		t.Fatalf("code %d", code)
	}
	first := e.lines()[0]
	for _, k := range []string{"@agent_state", "@agent_kind", "@agent_msg", "@agent_subagents", "@agent_shells"} {
		if !strings.Contains(first, "-u -t %7 "+k) {
			t.Errorf("first line %q does not unset %s", first, k)
		}
	}
	if !strings.Contains(first, "-t %7 @agent_pty ") {
		t.Errorf("first line %q does not set @agent_pty", first)
	}
}

func TestSignalExitCode(t *testing.T) {
	e := setup(t)
	if code, out := runInPTY(t, e.env, bin, "/bin/sh", "-c", "kill -TERM $$"); code != 128+15 {
		t.Fatalf("code %d, output %q", code, out)
	}
}

func TestRunsDirectlyOutsideTmux(t *testing.T) {
	e := setup(t)
	code, out := runInPTY(t, e.without("TMUX", "TMUX_PANE"), bin, "/bin/sh", "-c", `printf "$AGENT_PTY_ACTIVE"; exit 5`)
	if code != 5 || strings.TrimSpace(out) != "" {
		t.Fatalf("code %d output %q", code, out)
	}
	if _, err := os.Stat(e.log); err == nil {
		t.Error("tmux was called")
	}
}

func TestMarksItsChildAndDoesNotNest(t *testing.T) {
	e := setup(t)
	code, out := runInPTY(t, e.env, bin, bin, "/bin/sh", "-c", `printf "active=$AGENT_PTY_ACTIVE"`)
	if code != 0 || !strings.Contains(out, "active=1") {
		t.Fatalf("code %d output %q", code, out)
	}
	log, _ := os.ReadFile(e.log)
	if n := len(regexp.MustCompile(`-t %7 @agent_pty \d`).FindAll(log, -1)); n != 1 {
		t.Errorf("%d relays set @agent_pty", n)
	}
}

func TestMissingCommand(t *testing.T) {
	e := setup(t)
	code, out := runInPTY(t, e.env, bin, "/nonexistent/agent")
	if code != 127 || !strings.Contains(out, "/nonexistent/agent") {
		t.Fatalf("code %d output %q", code, out)
	}
}

func runCLI(t *testing.T, env []string, args ...string) int {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	err := cmd.Run()
	if x, ok := err.(*exec.ExitError); ok {
		return x.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0
}

func TestStatusSetsOptionsInAPlainPane(t *testing.T) {
	e := setup(t)
	if code := runCLI(t, e.env, "status", "working", "--app", "deploy", "--msg", "release"); code != 0 {
		t.Fatalf("code %d", code)
	}
	log, _ := os.ReadFile(e.log)
	if !strings.Contains(string(log), "-t %7 @agent_state working") || !strings.Contains(string(log), "@agent_msg release") {
		t.Fatalf("log %s", log)
	}
}

func TestJobReportsWorkingThenErrorWithTheExitCode(t *testing.T) {
	e := setup(t)
	if code := runCLI(t, e.env, "job", "--app", "deploy", "--", "/bin/sh", "-c", "exit 4"); code != 4 {
		t.Fatalf("code %d", code)
	}
	log, _ := os.ReadFile(e.log)
	w, x := strings.Index(string(log), "@agent_state working"), strings.Index(string(log), "@agent_state error")
	if w < 0 || x < w || !strings.Contains(string(log), "@agent_msg /bin/sh -c exit 4 (exit 4)") {
		t.Fatalf("log %s", log)
	}
}

func TestStatusRefusesAReportPastTheLimits(t *testing.T) {
	e := setup(t)
	if code := runCLI(t, e.env, "status", "idle", "--app", strings.Repeat("a", 40)); code != 2 {
		t.Fatalf("code %d", code)
	}
	if _, err := os.Stat(e.log); err == nil {
		t.Error("tmux was called")
	}
}

func TestStatusIsANoopWithoutATerminalOrTmux(t *testing.T) {
	e := setup(t)
	if code := runCLI(t, e.without("TMUX", "TMUX_PANE"), "status", "done"); code != 0 {
		t.Fatalf("code %d", code)
	}
	if _, err := os.Stat(e.log); err == nil {
		t.Error("tmux was called")
	}
}

// The agent stops itself as Ctrl-Z would; the relay must stop too, and
// carry on when continued, as a job-control shell expects.
func TestSuspendAndResume(t *testing.T) {
	e := setup(t)
	master, slave, err := term.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	cmd := exec.Command(bin, "/bin/sh", "-c", "echo start; kill -TSTP $$; echo resumed")
	cmd.Env = e.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	var out bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	pid := cmd.Process.Pid
	wait := func() syscall.WaitStatus {
		var ws syscall.WaitStatus
		done := make(chan struct{})
		go func() { syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("timed out, output %q", out.String())
		}
		return ws
	}
	if ws := wait(); ws&0xff != 0x7f {
		t.Fatalf("the relay did not stop: status %#x, output %q", int(ws), out.String())
	}
	syscall.Kill(pid, syscall.SIGCONT)
	if ws := wait(); !ws.Exited() || ws.ExitStatus() != 0 {
		t.Fatalf("status %#x after continue, output %q", int(ws), out.String())
	}
	time.Sleep(200 * time.Millisecond)
	if !strings.Contains(out.String(), "resumed") {
		t.Fatalf("output %q", out.String())
	}
}
