package agentpty

import "github.com/adamvduke/pst"

// Status is what the relay hands its observers: the agent's own record and
// counts of what runs under it.
type Status struct {
	State     string // idle, working, blocked, done, error; "" when there is no root record
	Kind      string // permission, question, auth; blocked only
	Msg       string // the root record's message, as the agent sent it
	App       string // the root record's app
	Subagents int    // child records that are working or blocked
	Shells    int    // background shells still running (Claude Code only)
}

// Observer receives the status. Update runs on the relay's goroutine for
// every change and must not block; Close runs once, after the command
// exits, with the records that survive the exit.
type Observer interface {
	Update(Status)
	Close(Status)
}

func summarize(store *pst.Store, shells int) Status {
	s := Status{Shells: shells}
	if root, ok := store.Get(""); ok {
		s.State = string(root.State)
		s.Kind = string(root.Kind)
		s.Msg = root.Msg
		s.App = root.App
	}
	for _, r := range store.All() {
		if r.ID != "" && (r.State == pst.StateWorking || r.State == pst.StateBlocked) {
			s.Subagents++
		}
	}
	return s
}
