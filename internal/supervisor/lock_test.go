package supervisor

import (
	"path/filepath"
	"testing"
)

// ST-21: single instance per user/host (SI-8).
func TestST21SingleInstance(t *testing.T) {
	root := t.TempDir()
	f1, err := SingleInstance(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()
	if _, err := SingleInstance(root); err == nil {
		t.Fatal("second instance acquired the lock (SI-8 violation)")
	}
	// different roots are independent hosts-scopes: allowed
	f2, err := SingleInstance(filepath.Join(root, "other"))
	if err != nil {
		t.Errorf("independent data root should be lockable: %v", err)
	} else {
		f2.Close()
	}
}
