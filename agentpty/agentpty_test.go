package agentpty

import (
	"bytes"
	"strings"
	"testing"

	"github.com/adamvduke/pst"
)

func TestPlainOutputPassesUnchanged(t *testing.T) {
	var s scanner
	out, seqs := s.feed([]byte("hello \x1b]0;title\x07 world"))
	if string(out) != "hello \x1b]0;title\x07 world" || len(seqs) != 0 {
		t.Fatalf("out %q seqs %v", out, seqs)
	}
}

func TestReportIsRemovedWithEitherTerminator(t *testing.T) {
	var s scanner
	out, seqs := s.feed([]byte("a\x1b]7501;state=working\x07b\x1b]7501;state=done\x1b\\c"))
	if string(out) != "abc" || len(seqs) != 2 {
		t.Fatalf("out %q seqs %v", out, seqs)
	}
	if string(seqs[0].body) != "state=working" || string(seqs[1].body) != "state=done" {
		t.Fatalf("bodies %q %q", seqs[0].body, seqs[1].body)
	}
	if seqs[0].offset != 1 || seqs[1].offset != 2 {
		t.Fatalf("offsets %d %d", seqs[0].offset, seqs[1].offset)
	}
}

func TestReportSplitAcrossReads(t *testing.T) {
	var s scanner
	data := []byte("x\x1b]7501;state=blocked:kind=permission\x1b\\y")
	var out []byte
	var bodies []string
	for i := range data {
		o, seqs := s.feed(data[i : i+1])
		out = append(out, o...)
		for _, f := range seqs {
			bodies = append(bodies, string(f.body))
		}
	}
	if string(out) != "xy" || len(bodies) != 1 || bodies[0] != "state=blocked:kind=permission" {
		t.Fatalf("out %q bodies %q", out, bodies)
	}
}

func TestMarkerPrefixAtEndIsHeldThenReleased(t *testing.T) {
	var s scanner
	if out, _ := s.feed([]byte("abc\x1b]75")); string(out) != "abc" {
		t.Fatalf("first %q", out)
	}
	if out, _ := s.feed([]byte("0x")); string(out) != "\x1b]750x" {
		t.Fatalf("second %q", out)
	}
}

func TestUnterminatedRunIsGivenUp(t *testing.T) {
	var s scanner
	out, seqs := s.feed(append([]byte("\x1b]7501;"), bytes.Repeat([]byte("a"), maxSequence+10)...))
	if len(seqs) != 0 || !bytes.HasPrefix(out, []byte("\x1b]7501;aaa")) {
		t.Fatalf("seqs %v out %q", seqs, out[:20])
	}
}

func TestParse(t *testing.T) {
	if _, k := parse([]byte("?"), 0); k != probe {
		t.Errorf("probe: %v", k)
	}
	if _, k := parse([]byte("state=idle"), maxSequence+1); k != discarded {
		t.Errorf("oversize: %v", k)
	}
	for _, body := range []string{"app=x", "state=sleeping", "state=idle:msg=A", "state=idle:id=a//b"} {
		if _, k := parse([]byte(body), 0); k != discarded {
			t.Errorf("%s: %v", body, k)
		}
	}
	// pst v0.1.0 would apply these to the root record.
	for _, body := range []string{"state=idle:id=a b", "state=idle:id=café", "state=idle: id =a!"} {
		if _, k := parse([]byte(body), 0); k != discarded {
			t.Errorf("%s: %v", body, k)
		}
	}
	r, k := parse([]byte("state=blocked:kind=permission:id=build/test:app=claude-code:msg=QWxsb3cgQmFzaDogbHM"), 0)
	if k != accepted || r.State != pst.StateBlocked || r.Kind != pst.KindPermission || r.ID != "build/test" ||
		r.App != "claude-code" || r.Msg != "Allow Bash: ls" {
		t.Errorf("accepted: %v %+v", k, r)
	}
}

func apply(t *testing.T, store *pst.Store, body string) {
	t.Helper()
	r, k := parse([]byte(body), 0)
	if k != accepted {
		t.Fatalf("%s: %v", body, k)
	}
	store.Apply(r)
}

func TestSummarizeMapsRootAndChildren(t *testing.T) {
	store := pst.NewStore()
	apply(t, store, "state=blocked:kind=permission:app=claude-code:msg=QWxsb3cgQmFzaDogbHM")
	apply(t, store, "state=working:id=task1")
	apply(t, store, "state=done:id=task2")
	got := summarize(store, 2)
	want := Status{State: "blocked", Kind: "permission", Msg: "Allow Bash: ls", App: "claude-code", Subagents: 1, Shells: 2}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestExitKeepsOnlyDoneAndError(t *testing.T) {
	for state, kept := range map[string]string{"done": "done", "error": "error", "working": "", "blocked": "", "idle": ""} {
		store := pst.NewStore()
		apply(t, store, "state="+state+":msg=cmVzdWx0")
		apply(t, store, "state=working:id=task")
		store.ProcessExited()
		s := summarize(store, 0)
		wantMsg := ""
		if kept != "" {
			wantMsg = "result"
		}
		if s.State != kept || s.Subagents != 0 || s.Msg != wantMsg {
			t.Errorf("%s: %+v", state, s)
		}
	}
}

func TestRunningShellsCountsAgentCommandsOnly(t *testing.T) {
	rows := map[int]proc{
		10: {1, "agent-pty stub"},
		11: {10, "claude"},
		12: {11, "/bin/zsh -c source ~/.claude/shell-snapshots/snapshot-zsh.sh && { sleep 45 }"},
		13: {12, "/bin/zsh -c source ~/.claude/shell-snapshots/snapshot-zsh.sh && { sleep 45 }"},
		14: {13, "sleep 45"},
		15: {11, "sh /home/u/.claude/hooks/notify.sh"},
		16: {11, "node mcp-server.js"},
		20: {1, "/bin/zsh -c source ~/.claude/shell-snapshots/other-session.sh"},
	}
	if n := runningShells(rows, 10); n != 1 {
		t.Errorf("under the stub: %d", n)
	}
	if n := runningShells(rows, 16); n != 0 {
		t.Errorf("under the MCP server: %d", n)
	}
}

func TestProcessRowsSeesThisProcess(t *testing.T) {
	rows := processRows()
	if rows == nil {
		t.Skip("ps failed")
	}
	for _, p := range rows {
		if strings.Contains(p.args, "agentpty.test") {
			return
		}
	}
	t.Fatal("this test binary is missing from ps")
}
