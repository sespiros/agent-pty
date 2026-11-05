package tmuxstatus

import (
	"reflect"
	"testing"
)

func TestCommandsSetAndUnset(t *testing.T) {
	got := commands("%3", []pair{{State, "done"}, {Kind, ""}})
	want := []string{"tmux", "set-option", "-w", "-q", "-t", "%3", "@agent_state", "done", ";",
		"set-option", "-w", "-q", "-u", "-t", "%3", "@agent_kind", ";", "refresh-client", "-S"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestShownDropsInvisibleCharactersAndCuts(t *testing.T) {
	if got := Shown("a‮b\tc"); got != "abc" {
		t.Errorf("got %q", got)
	}
	long := make([]rune, MsgLimit+5)
	for i := range long {
		long[i] = 'é'
	}
	if got := []rune(Shown(string(long))); len(got) != MsgLimit {
		t.Errorf("len %d", len(got))
	}
}
