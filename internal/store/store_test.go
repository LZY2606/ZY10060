package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"metrologylab/internal/domain"
	"metrologylab/internal/store"
)

func TestIdempotentRequestAndFingerprintConflict(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	mut := func(v string) ([]byte, error) { return []byte(v), nil }
	b1, _, err := st.Mutate(context.Background(), "r1", "POST", "/x", "test", nil, func(st *domain.State) ([]byte, error) {
		st.Observations["a"] = domain.Observation{ID: "a"}
		return mut("first")
	}, "finger")
	if err != nil || string(b1) != "first" {
		t.Fatalf("%s %v", b1, err)
	}
	b2, _, err := st.Mutate(context.Background(), "r1", "POST", "/x", "test", nil, func(st *domain.State) ([]byte, error) {
		st.Observations["b"] = domain.Observation{ID: "b"}
		return []byte("second"), nil
	}, "finger")
	if err != nil || string(b2) != "first" {
		t.Fatalf("replay changed result: %s %v", b2, err)
	}
	if len(st.State().Observations) != 1 {
		t.Fatal("replay created second business result")
	}
	_, _, err = st.Mutate(context.Background(), "r1", "POST", "/x", "test", nil, func(st *domain.State) ([]byte, error) { return []byte("x"), nil }, "different")
	if err == nil {
		t.Fatal("expected fingerprint conflict")
	}
}

func TestRecoversCommittedStaging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st.Mutate(context.Background(), "r", "POST", "/x", "test", nil, func(st *domain.State) ([]byte, error) {
		st.Observations["a"] = domain.Observation{ID: "a"}
		return []byte(`{"ok":true}`), nil
	}, "f")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx) == 0 {
		t.Fatal("no transaction dir to simulate")
	}
	// Normal Open applies committed transaction directories and must not duplicate event sequence.
	st2, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.State().Observations) != 1 || st2.State().EventSeq != 1 {
		t.Fatalf("state=%#v", st2.State())
	}
	if _, err := os.Stat(filepath.Join(root, "observations", "a.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDiscardsUncommittedStaging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(root, ".tx-bad", "observations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tx-bad", "observations", "ghost.json"), []byte("{\"id\":\"ghost\"}"), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.State().Observations["ghost"]; ok {
		t.Fatal("uncommitted staging was exposed")
	}
	if _, err := os.Stat(filepath.Join(root, ".tx-bad")); !os.IsNotExist(err) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestOpenAppliesCommittedStaging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if _, err := store.Open(root); err != nil {
		t.Fatal(err)
	}
	tx := filepath.Join(root, "tx-manual")
	if err := os.MkdirAll(filepath.Join(tx, "observations"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := domain.NewState()
	state.EventSeq = 1
	state.Observations["a"] = domain.Observation{ID: "a", Unit: "mm"}
	stateBytes, _ := json.Marshal(state)
	eventBytes, _ := json.Marshal(domain.Event{ID: "evt-000000000001", Seq: 1, Type: "test", SchemaVersion: domain.SchemaVersion})
	manifestBytes, _ := json.Marshal(struct {
		SchemaVersion string   `json:"schemaVersion"`
		Files         []string `json:"files"`
		Deletes       []string `json:"deletes"`
	}{domain.SchemaVersion, []string{"manifest.json", "events/evt-000000000001.json", "observations/a.json"}, nil})
	if err := os.WriteFile(filepath.Join(tx, "manifest.json"), stateBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tx, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tx, "events", "evt-000000000001.json"), eventBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tx, "observations", "a.json"), stateBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tx, "tx-manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tx, "COMMITTED"), []byte("committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.State().Observations) != 1 || reopened.State().EventSeq != 1 {
		t.Fatalf("state=%#v", reopened.State())
	}
}
