// Package store: atomic file persistence inside the data root (SI-3).
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Store interface {
	WriteAtomic(name string, data []byte) error
	Read(name string) ([]byte, error)
	DataRoot() string
}

type fsStore struct{ root string }

func New(root string) (Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &fsStore{root: root}, nil
}

func (s *fsStore) DataRoot() string { return s.root }

// resolve rejects path escapes (ST-11).
//
// The lexical checks are not sufficient on their own. A symlink placed inside
// the root — "escape" pointing at /tmp/somewhere-else — makes
// "escape/pwned" look contained while the write lands outside. Measured: the
// lexical version of this function accepted exactly that and created a file in
// the target directory.
//
// The fix is to resolve the real path of the existing prefix and require it to
// still be inside the root. The final element may not exist yet (that is the
// normal case for a write), so only its parent chain is resolved.
func (s *fsStore) resolve(name string) (string, error) {
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("path escape rejected: %q", name)
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("empty path rejected: %q", name)
	}
	p := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escape rejected: %q", name)
	}
	// Symlink check: resolve what exists, and confirm it stayed inside.
	probe := filepath.Dir(p)
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			realRoot, rerr := filepath.EvalSymlinks(s.root)
			if rerr != nil {
				realRoot = s.root
			}
			inside, rerr := filepath.Rel(realRoot, real)
			if rerr != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("path escape rejected through a symlink: %q", name)
			}
			break
		}
		// Parent does not exist yet: walk up until something does. When we
		// reach the root itself, the remaining components are created by us
		// and cannot be symlinks.
		parent := filepath.Dir(probe)
		if parent == probe || len(parent) <= len(s.root) {
			break
		}
		probe = parent
	}
	return p, nil
}

func (s *fsStore) WriteAtomic(name string, data []byte) error {
	p, err := s.resolve(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *fsStore) Read(name string) ([]byte, error) {
	p, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}
