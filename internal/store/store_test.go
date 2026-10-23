package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPathEscapeRejected(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../evil", "a/../../evil", "/etc/hosts", "../../.."} {
		if err := s.WriteAtomic(name, []byte("x")); err == nil {
			t.Errorf("escape accepted: %q", name)
		}
	}
	if err := s.WriteAtomic("state/state.json", []byte("{}")); err != nil {
		t.Errorf("legitimate write failed: %v", err)
	}
	got, err := s.Read("state/state.json")
	if err != nil || strings.TrimSpace(string(got)) != "{}" {
		t.Errorf("round trip failed: %v %q", err, got)
	}
	_ = filepath.Separator
}
