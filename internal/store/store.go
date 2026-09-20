package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metrologylab/internal/domain"
)

var ErrConflict = errors.New("state conflict")
var ErrNotFound = errors.New("not found")

type Store struct {
	root  string
	mu    sync.RWMutex
	state *domain.State
}

func Open(root string) (*Store, error) {
	s := &Store{root: root}
	for _, d := range dirs(root) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.recoverStaging(); err != nil {
		return nil, err
	}
	st := domain.NewState()
	if err := loadJSON(filepath.Join(root, "manifest.json"), st); err != nil {
		return nil, err
	}
	s.state = st
	return s, nil
}

func (s *Store) State() *domain.State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp, err := deepCopy(s.state)
	if err != nil {
		panic(err)
	}
	return cp
}

func (s *Store) Event(id string) (domain.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ev domain.Event
	err := loadJSON(filepath.Join(s.root, "events", id+".json"), &ev)
	if errors.Is(err, fs.ErrNotExist) {
		return ev, ErrNotFound
	}
	return ev, err
}

func (s *Store) Events() []domain.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Event{}
	for i := int64(1); i <= s.state.EventSeq; i++ {
		var ev domain.Event
		if err := loadJSON(filepath.Join(s.root, "events", fmt.Sprintf("evt-%012d.json", i)), &ev); err == nil && ev.ID != "" {
			out = append(out, ev)
		}
	}
	return out
}

func (s *Store) Replay(requestID string) (domain.RequestRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.state.Requests[requestID]
	return r, ok
}

func (s *Store) Mutate(ctx context.Context, requestID, method, path, eventType string, payload map[string]any, mut func(*domain.State) ([]byte, error), fingerprint string) ([]byte, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requestID != "" {
		if rec, ok := s.state.Requests[requestID]; ok {
			if rec.Fingerprint != fingerprint {
				return nil, 409, ErrConflict
			}
			return rec.Response, rec.StatusCode, nil
		}
	}
	next, err := deepCopy(s.state)
	if err != nil {
		return nil, 500, err
	}
	response, err := mut(next)
	if err != nil {
		return response, statusForError(err), err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["requestId"] = requestID
	ev := domain.Event{Seq: next.EventSeq + 1, ID: fmt.Sprintf("evt-%012d", next.EventSeq+1), Type: eventType, RequestID: requestID, Payload: payload, CreatedAt: time.Now().UTC(), SchemaVersion: domain.SchemaVersion}
	if err := s.commit(ctx, next, ev, method, path, fingerprint, response); err != nil {
		return nil, 500, err
	}
	s.state = next
	return response, 200, nil
}

func (s *Store) commit(ctx context.Context, next *domain.State, ev domain.Event, method, path, fingerprint string, response []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := os.MkdirTemp(s.root, ".tx-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tx)
	if err := os.MkdirAll(filepath.Join(tx, "events"), 0o755); err != nil {
		return err
	}
	next.EventSeq = ev.Seq
	if ev.RequestID != "" {
		next.Requests[ev.RequestID] = domain.RequestRecord{Method: method, Path: path, Fingerprint: fingerprint, StatusCode: 200, Response: response, CompletedAt: ev.CreatedAt}
	}
	files := map[string]any{"events/" + ev.ID + ".json": ev, "manifest.json": next}
	deletes := []string{}
	addClass := func(class string, ids []string) {
		live := map[string]bool{}
		for _, id := range ids {
			files[class+"/"+id+".json"] = mapValue(next, class, id)
			live[id] = true
		}
		oldIDs, _ := os.ReadDir(filepath.Join(s.root, class))
		for _, en := range oldIDs {
			id := strings.TrimSuffix(en.Name(), ".json")
			if !live[id] {
				deletes = append(deletes, class+"/"+id+".json")
			}
		}
	}
	addClass("observations", keys(next.Observations))
	addClass("groups", keys(next.Groups))
	addClass("certificates", keys(next.Certificates))
	addClass("scenarios", keys(next.Scenarios))
	addClass("results", keys(next.Results))
	addClass("frozen", keys(next.FrozenResults))
	addClass("requests", keys(next.Requests))
	manifest := TxManifest{SchemaVersion: domain.SchemaVersion, CreatedAt: time.Now().UTC(), Files: []string{}}
	for name := range files {
		manifest.Files = append(manifest.Files, name)
	}
	sort.Strings(manifest.Files)
	sort.Strings(deletes)
	manifest.Deletes = deletes
	for _, name := range manifest.Files {
		dir := filepath.Join(tx, filepath.Dir(name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		body, err := jsonMarshalIndent(files[name])
		if err != nil {
			return err
		}
		if err := writeFileSync(filepath.Join(tx, name), body); err != nil {
			return err
		}
	}
	mb, err := jsonMarshalIndent(manifest)
	if err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(tx, "tx-manifest.json"), mb); err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(tx, "COMMITTED"), []byte("committed\n")); err != nil {
		return err
	}
	finalTx := filepath.Join(s.root, strings.Replace(filepath.Base(tx), ".tx-", "tx-", 1))
	if err := os.Rename(tx, finalTx); err != nil {
		return err
	}
	if err := syncDir(s.root); err != nil {
		return err
	}
	return applyCommitted(s.root, filepath.Base(finalTx))
}

func (s *Store) recoverStaging() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, en := range entries {
		if !strings.HasPrefix(en.Name(), ".tx-") && !strings.HasPrefix(en.Name(), "tx-") {
			continue
		}
		p := filepath.Join(s.root, en.Name())
		if en.Name()[:1] == "." {
			if _, err := os.Stat(filepath.Join(p, "COMMITTED")); err == nil {
				final := filepath.Join(s.root, "tx-"+strings.TrimPrefix(en.Name(), ".tx-"))
				if err := os.Rename(p, final); err != nil {
					return err
				}
				continue
			}
			if err := os.RemoveAll(p); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(p, "COMMITTED")); err == nil {
			if err := applyCommitted(s.root, en.Name()); err != nil {
				return err
			}
		} else {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyCommitted(root, txName string) error {
	tx := filepath.Join(root, txName)
	var m TxManifest
	if err := loadJSON(filepath.Join(tx, "tx-manifest.json"), &m); err != nil {
		return err
	}
	for _, rel := range m.Files {
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(tx, rel), dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := syncDir(filepath.Dir(dst)); err != nil {
			return err
		}
	}
	for _, rel := range m.Deletes {
		if err := os.Remove(filepath.Join(root, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := syncDir(filepath.Dir(filepath.Join(root, rel))); err != nil {
			return err
		}
	}
	return os.RemoveAll(tx)
}

func dirs(root string) []string {
	return []string{root, filepath.Join(root, "events"), filepath.Join(root, "observations"), filepath.Join(root, "groups"), filepath.Join(root, "certificates"), filepath.Join(root, "scenarios"), filepath.Join(root, "results"), filepath.Join(root, "frozen"), filepath.Join(root, "requests"), filepath.Join(root, "exports")}
}

type TxManifest struct {
	SchemaVersion string    `json:"schemaVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	Files         []string  `json:"files"`
	Deletes       []string  `json:"deletes"`
}
type jsonMarshaler interface{}

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}
func writeFileSync(path string, b []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func keys[V any](m map[string]V) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func mapValue(st *domain.State, class, id string) any {
	switch class {
	case "observations":
		return st.Observations[id]
	case "groups":
		return st.Groups[id]
	case "certificates":
		return st.Certificates[id]
	case "scenarios":
		return st.Scenarios[id]
	case "results":
		return st.Results[id]
	case "frozen":
		return st.FrozenResults[id]
	case "requests":
		return st.Requests[id]
	}
	return nil
}
func deepCopy(st *domain.State) (*domain.State, error) {
	var out domain.State
	b, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func statusForError(err error) int {
	if errors.Is(err, ErrConflict) {
		return 409
	}
	return 400
}
