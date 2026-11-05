package agentpty

import "syscall"

// stopped reports whether a status from wait4 with WUNTRACED means the child
// stopped. syscall.WaitStatus.Stopped cannot be used: on the BSDs, macOS
// included, it is false when the stop signal was SIGSTOP, because it reads
// that status as "continued". The stub stops itself with SIGSTOP, so the
// raw check (WIFSTOPPED) is the one that works on every Unix.
func stopped(ws syscall.WaitStatus) bool { return ws&0xff == 0x7f }

// await waits for pid to stop or end. On a stop it calls onStop and waits
// again. When a signal ended pid, it calls onSignal, if given. It returns the
// exit code in the shell convention.
func await(pid int, onStop func(), onSignal func(syscall.Signal)) int {
	for {
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil); err != nil {
			if err == syscall.EINTR {
				continue
			}
			return 0
		}
		switch {
		case stopped(ws):
			onStop()
		case ws.Signaled():
			if onSignal != nil {
				onSignal(ws.Signal())
			}
			return 128 + int(ws.Signal())
		case ws.Exited():
			return ws.ExitStatus()
		}
	}
}
