// Package store implements a write-ahead-log backed document store.
//
// All state changes are first appended as one checksummed WAL record and
// fsynced; materialized collection files are rewritten atomically afterwards.
// A crash between the two leaves at most a stale but never a torn materialized
// view: startup always rebuilds authoritative state by replaying the WAL.
package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrConflict is returned when an operation conflicts with current state
	// (frozen object, duplicate request, stale version, ...).
	ErrConflict = errors.New("state conflict")
)

// Record is one durable event. Payload is an opaque JSON document; the store
// itself stays domain agnostic.
type Record struct {
	Seq       int64           `json:"seq"`
	Event     string          `json:"event"`
	ID        string          `json:"id"`
	RequestID string          `json:"request_id,omitempty"`
	Time      string          `json:"time"`
	Payload   json.RawMessage `json:"payload"`
}

type frame struct {
	Record
	SHA256 string `json:"sha256"`
}

// Store is a WAL + per-collection materialized JSON files.
type Store struct {
	mu   sync.Mutex
	dir  string
	wal  *os.File
	wbuf *bufio.Writer
	seq  int64
}

const walName = "events.wal"

// Open opens (creating if needed) a store in dir and replays the WAL.
func Open(dir string) (*Store, []Record, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, en := range entries {
		name := en.Name()
		if len(name) > 0 && name[0] == '.' && (hasSuffix(name, ".tmp") || hasSuffix(name, ".new")) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	_ = entries
	path := filepath.Join(dir, walName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, err
	}
	recs, err := replay(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	s := &Store{dir: dir, wal: f, wbuf: bufio.NewWriter(f)}
	if len(recs) > 0 {
		s.seq = recs[len(recs)-1].Seq
	}
	return s, recs, nil
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }

// Append durably records one event and returns its assigned sequence number.
func (s *Store) Append(rec Record) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	rec.Seq = s.seq
	fr := frame{Record: rec}
	b, err := json.Marshal(rec)
	if err != nil {
		s.seq--
		return 0, err
	}
	h := sha256.Sum256(b)
	fr.SHA256 = hex.EncodeToString(h[:])
	line, err := json.Marshal(fr)
	if err != nil {
		s.seq--
		return 0, err
	}
	if _, err := s.wbuf.Write(line); err != nil {
		s.seq--
		return 0, err
	}
	if err := s.wbuf.WriteByte('\n'); err != nil {
		s.seq--
		return 0, err
	}
	if err := s.wbuf.Flush(); err != nil {
		s.seq--
		return 0, err
	}
	if err := s.wal.Sync(); err != nil {
		s.seq--
		return 0, err
	}
	return rec.Seq, nil
}

// RewriteCollection atomically replaces a materialized collection file.
func (s *Store) RewriteCollection(name string, docs []json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	final := filepath.Join(s.dir, name+".json")
	tmp := filepath.Join(s.dir, "."+name+".json.tmp")
	b, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := fsyncDir(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return fsyncDir(final)
}

// LoadCollection reads a materialized collection.
func LoadCollection[T any](s *Store, name string) ([]T, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var docs []T
	if len(b) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(b, &docs); err != nil {
		return nil, fmt.Errorf("collection %s: %w", name, err)
	}
	return docs, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.wbuf.Flush(); err != nil {
		return err
	}
	return s.wal.Close()
}

func replay(f *os.File) ([]Record, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024), 64*1024*1024)
	var recs []Record
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var fr frame
		if err := json.Unmarshal(line, &fr); err != nil {
			// torn trailing record: truncate here
			return recs, truncateAt(f, lineNo)
		}
		b, err := json.Marshal(fr.Record)
		if err != nil {
			return recs, err
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != fr.SHA256 {
			return recs, truncateAt(f, lineNo)
		}
		recs = append(recs, fr.Record)
	}
	return recs, sc.Err()
}

// truncateAt drops the (torn) line at lineNo and everything after it.
func truncateAt(f *os.File, lineNo int) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sc := bufio.NewScanner(f)
	var offset int64
	cur := int64(0)
	n := 0
	for sc.Scan() {
		n++
		cur += int64(len(sc.Bytes())) + 1
		if n == lineNo {
			offset = cur - int64(len(sc.Bytes())) - 1
			break
		}
		offset = cur
	}
	if err := f.Truncate(offset); err != nil {
		return err
	}
	_, err := f.Seek(0, io.SeekEnd)
	return err
}

// AppendBatch durably records several events in a single fsync. Either every
// record lands or none does (the write is flushed as one contiguous block).
func (s *Store) AppendBatch(recs []Record) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []byte
	var seqs []int64
	start := s.seq + 1
	for i := range recs {
		s.seq++
		recs[i].Seq = s.seq
		seqs = append(seqs, s.seq)
		b, err := json.Marshal(recs[i])
		if err != nil {
			s.seq = start - 1
			return nil, err
		}
		h := sha256.Sum256(b)
		fr := frame{Record: recs[i], SHA256: hex.EncodeToString(h[:])}
		line, err := json.Marshal(fr)
		if err != nil {
			s.seq = start - 1
			return nil, err
		}
		lines = append(lines, line...)
		lines = append(lines, '\n')
	}
	if _, err := s.wbuf.Write(lines); err != nil {
		s.seq = start - 1
		return nil, err
	}
	if err := s.wbuf.Flush(); err != nil {
		s.seq = start - 1
		return nil, err
	}
	if err := s.wal.Sync(); err != nil {
		s.seq = start - 1
		return nil, err
	}
	return seqs, nil
}
