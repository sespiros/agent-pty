// Command fakeagent plays an agent for the end-to-end tests.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/sespiros/agent-pty/internal/term"
)

func main() {
	switch os.Args[1] {
	case "probe": // probe REPLYFILE CODE
		if t, err := term.Get(0); err == nil {
			term.Set(0, term.Raw(t))
		}
		os.Stdout.WriteString("before\x1b]7501;?\x1b\\")
		var got []byte
		buf := make([]byte, 1024)
		done := make(chan struct{})
		go func() {
			for !bytes.Contains(got, []byte("\x1b\\")) {
				n, err := os.Stdin.Read(buf)
				got = append(got, buf[:n]...)
				if err != nil {
					break
				}
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		os.WriteFile(os.Args[2], got, 0o644)
		os.Stdout.WriteString("\x1b]7501;state=working:app=fake\x07middle")
		time.Sleep(300 * time.Millisecond)
		os.Stdout.WriteString("\x1b]7501;state=done:app=fake:msg=QWxsIGdvb2Q\x1b\\after\r\n")
		time.Sleep(300 * time.Millisecond)
		code, _ := strconv.Atoi(os.Args[3])
		os.Exit(code)
	case "shell":
		// The trailing ':' keeps sh from exec-ing sleep, so the mark stays in argv.
		exec.Command("/bin/sh", "-c", ": shell-snapshots/x; sleep 3; :").Start()
		os.Stdout.WriteString("\x1b]7501;state=done:app=fake\x1b\\")
		time.Sleep(6 * time.Second)
	case "subagents":
		os.Stdout.WriteString("\x1b]7501;state=working:app=fake\x1b\\")
		os.Stdout.WriteString("\x1b]7501;state=working:id=a1\x1b\\\x1b]7501;state=working:id=a2\x1b\\")
		time.Sleep(300 * time.Millisecond)
		os.Stdout.WriteString("\x1b]7501;state=clear:id=a1\x1b\\")
		os.Stdout.WriteString("\x1b]7501;state=idle:id=a b\x1b\\")
		time.Sleep(300 * time.Millisecond)
	default:
		fmt.Fprintln(os.Stderr, "fakeagent: unknown mode")
		os.Exit(2)
	}
}
