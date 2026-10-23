package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRotationContainmentAndVersion(t *testing.T) {
	dir := t.TempDir()
	l, err := openWith(dir, "test", 200, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 40; i++ {
		if err := l.Append(map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	files := l.files()
	if len(files) == 0 || len(files) > 3 {
		t.Fatalf("rotation retention wrong: %d files", len(files))
	}
	// version header present and not returned as a record
	recs, err := l.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 40 {
		t.Fatalf("expected 40 records, got %d", len(recs))
	}
	var v map[string]any
	json.Unmarshal(recs[0], &v)
	if v["i"].(float64) != 0 {
		t.Fatalf("first record wrong: %v", v)
	}
	// all files are inside dir (ST-12)
	for _, f := range files {
		if filepath.Dir(f) != dir {
			t.Fatalf("ledger escaped dir: %s", f)
		}
	}
}

func TestCrashTailTolerance(t *testing.T) {
	dir := t.TempDir()
	l, err := openWith(dir, "crash", 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		l.Append(map[string]int{"i": i})
	}
	l.Close()
	// simulate crash: partial trailing line
	f := l.files()[0]
	fh, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(`{"i": 5, "part`)
	fh.Close()

	l2, err := Open(dir, "crash")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	recs, err := l2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("crash tail not salvaged: %d records", len(recs))
	}
}

func TestPathSanity(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, "x")
	defer l.Close()
	if l.SizeBytes() <= 0 {
		t.Fatal("size should include header")
	}
	_ = fmt.Sprint
	_ = os.Getenv
}
