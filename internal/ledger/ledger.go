// Package ledger: append-only JSONL with rotation, fsync, version header,
// and crash-tail tolerance (ADR-003, MP-ARCH-005).
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	versionHeader = `{"ledger_version":1}`
	defaultRotateBytes = 64 << 20 // 64 MB
	defaultKeep        = 8
)

type Ledger struct {
	mu          sync.Mutex
	dir         string
	name        string
	rotateBytes int64
	keep        int
	f           *os.File
	size        int64
	seq         int
}

func Open(dir, name string) (*Ledger, error) {
	return openWith(dir, name, defaultRotateBytes, defaultKeep)
}

func openWith(dir, name string, rotate int64, keep int) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Ledger{dir: dir, name: name, rotateBytes: rotate, keep: keep}
	// recovery: salvage a crash-truncated tail (tolerate a partial final line)
	files := l.files()
	if len(files) > 0 {
		cur := files[len(files)-1]
		salvage(cur)
		f, err := os.OpenFile(cur, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		st, _ := f.Stat()
		l.f, l.size, l.seq = f, st.Size(), seqOf(cur)
	} else {
		if err := l.rotateFile(); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *Ledger) files() []string {
	matches, _ := filepath.Glob(filepath.Join(l.dir, l.name+".*.jsonl"))
	sort.Strings(matches)
	return matches
}

func seqOf(p string) int {
	base := filepath.Base(p) // name.NNNN.jsonl
	i := strings.LastIndex(base, ".")
	var n int
	if i > 0 {
		fmt.Sscanf(base[:i], lNamePattern, &struct{}{}, &n)
		if n == 0 {
			fmt.Sscanf(strings.TrimSuffix(base, ".jsonl"), lNamePattern, &struct{}{}, &n)
		}
	}
	return n
}

const lNamePattern = "%s.%04d.jsonl"

// salvage truncates a trailing partial line (crash tolerance).
func salvage(path string) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return
	}
	data := make([]byte, st.Size())
	f.ReadAt(data, 0)
	if data[st.Size()-1] == '\n' {
		return
	}
	if idx := bytes.LastIndexByte(data, '\n'); idx >= 0 {
		f.Truncate(int64(idx + 1))
	} else {
		f.Truncate(0)
	}
}

func (l *Ledger) rotateFile() error {
	if l.f != nil {
		l.f.Close()
	}
	l.seq++
	p := filepath.Join(l.dir, fmt.Sprintf(lNamePattern, l.name, l.seq))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(versionHeader + "\n"); err != nil {
		f.Close()
		return err
	}
	l.f = f
	l.size = int64(len(versionHeader) + 1)
	l.prune()
	return nil
}

func (l *Ledger) prune() {
	files := l.files()
	if len(files) <= l.keep {
		return
	}
	for _, old := range files[:len(files)-l.keep] {
		os.Remove(old) // own data dir only (SI-3)
	}
}

// Append writes one JSON record followed by a newline and fsyncs.
func (l *Ledger) Append(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return fmt.Errorf("ledger closed")
	}
	if l.size+int64(len(b))+1 > l.rotateBytes {
		if err := l.rotateFile(); err != nil {
			return err
		}
	}
	n, err := l.f.Write(append(b, '\n'))
	l.size += int64(n)
	if err != nil {
		return err
	}
	return l.f.Sync()
}

func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// ReadAll replays every record (tolerating crash-truncated tails) in order.
func (l *Ledger) ReadAll() ([]json.RawMessage, error) {
	var out []json.RawMessage
	for _, p := range l.files() {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
		first := true
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if first {
				first = false
				if strings.Contains(line, "ledger_version") {
					continue
				}
			}
			out = append(out, json.RawMessage(line))
		}
		f.Close()
	}
	return out, nil
}

// SizeBytes reports the total on-disk size of the ledger set.
func (l *Ledger) SizeBytes() int64 {
	var total int64
	for _, p := range l.files() {
		if st, err := os.Stat(p); err == nil {
			total += st.Size()
		}
	}
	return total
}
