package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWALReplayAndTornTail(t *testing.T) {
	dir := t.TempDir()
	st, recs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := st.Append(Record{Event: "e", ID: "id", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RewriteCollection("things", []json.RawMessage{json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// append a torn, partial line simulating a crash mid-fsync
	f, err := os.OpenFile(filepath.Join(dir, walName), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":6,"event":"half"`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	st2, recs2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs2) != 5 {
		t.Fatalf("replay count: first=%d after-torn=%d", len(recs), len(recs2))
	}
	if recs2[4].Seq != 5 {
		t.Fatalf("last seq want 5 got %d", recs2[4].Seq)
	}
	// appending after recovery continues sequence safely
	seq, err := st2.Append(Record{Event: "e", ID: "post", Payload: json.RawMessage(`{}`)})
	if err != nil || seq != 6 {
		t.Fatalf("post-recovery append seq=%d err=%v", seq, err)
	}
	st2.Close()

	// materialized collection still readable/intact
	b, err := os.ReadFile(filepath.Join(dir, "things.json"))
	if err != nil || string(b) == "" {
		t.Fatalf("collection lost: %v %q", err, b)
	}
}

func TestAppendBatchAtomic(t *testing.T) {
	dir := t.TempDir()
	st, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	recs := []Record{
		{Event: "a", Payload: json.RawMessage(`{}`)},
		{Event: "b", Payload: json.RawMessage(`{}`)},
		{Event: "c", Payload: json.RawMessage(`{}`)},
	}
	seqs, err := st.AppendBatch(recs)
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 3 || seqs[0] != 1 || seqs[2] != 3 {
		t.Fatalf("batch seqs %v", seqs)
	}
	st.Close()
	st2, recs2, err := Open(dir)
	if err != nil || len(recs2) != 3 {
		t.Fatalf("replay after batch: %d %v", len(recs2), err)
	}
	st2.Close()
}
