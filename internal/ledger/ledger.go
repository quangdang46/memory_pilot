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
	versionHeader      = `{"ledger_version":1}`
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
	// onError is called on the first failed write of an episode and again on
	// the first success after one. Every Append caller in the daemon discards
	// the error, so without this a broken ledger is indistinguishable from a
	// quiet one — which is how the daemon sat dead for 31 hours on 2026-10-26
	// while answering IPC normally.
	onError func(error)
	failing bool
	lastErr error
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
	base = strings.TrimSuffix(base, ".jsonl")
	// base is name.NNNN
	i := strings.LastIndex(base, ".")
	if i < 0 || i == len(base)-1 {
		return 0
	}
	var n int
	for j := i + 1; j < len(base); j++ {
		if base[j] < '0' || base[j] > '9' {
			return 0
		}
		n = n*10 + int(base[j]-'0')
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
	seq := l.seq + 1
	p := filepath.Join(l.dir, fmt.Sprintf(lNamePattern, l.name, seq))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(versionHeader + "\n"); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	old := l.f
	l.seq = seq
	l.f = f
	l.size = int64(len(versionHeader) + 1)
	if old != nil {
		old.Close()
	}
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

// Rotate closes the current file and starts a new one (T1b). Called by the
// executor's rotate_own_ledger action and automatically when the size cap is
// reached.
func (l *Ledger) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotateFile()
}

// Append writes one JSON record followed by a newline and fsyncs.
// OnError installs a callback for the first failure of a write episode and for
// the first success after it. Recovery is reported with a nil error.
func (l *Ledger) OnError(fn func(error)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onError = fn
}

// note records a transition in and out of failure, and returns err unchanged.
func (l *Ledger) note(err error) error {
	switch {
	case err != nil && !l.failing:
		l.failing, l.lastErr = true, err
		if l.onError != nil {
			l.onError(err)
		}
	case err == nil && l.failing:
		prev := l.lastErr
		l.failing, l.lastErr = false, nil
		if l.onError != nil {
			l.onError(fmt.Errorf("ledger recovered after: %w", prev))
		}
	}
	return err
}

func (l *Ledger) Append(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.note(l.appendLocked(b))
}

// Health is what `status` reports. SizeBytes alone is not health: it is
// measured from disk, so a ledger whose handle died still reports the size of
// the records it wrote before it died. On 2026-10-26 the daemon printed
// "ledger 40.8MB" for 31 hours while writing nothing. Healthy means writes are
// landing right now.
func (l *Ledger) Health() (healthy bool, lastError string, seq int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failing && l.lastErr != nil {
		return false, l.lastErr.Error(), l.seq
	}
	return l.f != nil, "", l.seq
}

// appendLocked writes one marshalled record. Callers hold l.mu.
func (l *Ledger) appendLocked(b []byte) error {
	if l.f == nil {
		return fmt.Errorf("ledger closed")
	}
	if l.size+int64(len(b))+1 > l.rotateBytes {
		if err := l.rotateFile(); err != nil {
			// rotation failed (e.g. collision / disk full) — keep writing into
			// the current file rather than killing the whole ledger. Losing
			// rotation is survivable; losing every subsequent record is not.
			// The live daemon was killed by the opposite choice.
		}
	}
	if l.f == nil {
		return fmt.Errorf("ledger closed")
	}
	n, err := l.f.Write(append(b, '\n'))
	l.size += int64(n)
	if err != nil {
		// Best-effort recovery: the handle may be broken. Try once to
		// reopen/rotate before giving up. Callers discard errors today, but
		// logging the failure is the job; the guarantee is we don't drop the
		// ledger forever because one rotation failed.
		if rerr := l.rotateFile(); rerr != nil {
			return err
		}
		n, err = l.f.Write(append(b, '\n'))
		if err != nil {
			return err
		}
		l.size += int64(n)
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	return nil
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
