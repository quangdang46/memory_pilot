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
func (s *fsStore) resolve(name string) (string, error) {
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("path escape rejected: %q", name)
	}
	clean := filepath.Clean(name)
	p := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escape rejected: %q", name)
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
