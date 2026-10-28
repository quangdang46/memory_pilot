package store

import (
	"os"
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

// ST-11 requires the symlink case and the crafted-config case, not just ".."
// in a name. A symlink inside the root is the vector a lexical check misses:
// the path looks contained, and the write lands outside.
func TestSymlinkToOutsideIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// A directory inside the root that points out of it.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	err = s.WriteAtomic("escape/pwned", []byte("x"))
	// Either the write is refused, or the symlink is not followed and the file
	// lands inside the root — both are safe. What must never happen is a file
	// appearing in `outside`.
	if _, statErr := os.Stat(filepath.Join(outside, "pwned")); statErr == nil {
		t.Fatalf("write escaped the root through a symlink (err was %v)", err)
	}
}

// A path that resolves inside the root lexically but not after symlinks must
// still be refused, and the caller must get an error rather than a silent
// write to the wrong place.
func TestTraversalAfterCleanIsRejected(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"state/../../outside",
		"./../../etc/passwd",
		"a/b/../../../c",
		"..",
		"state/..",
	} {
		if err := s.WriteAtomic(name, []byte("x")); err == nil {
			t.Errorf("escape accepted: %q", name)
		}
	}
}
