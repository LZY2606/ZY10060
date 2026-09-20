package service

import (
	"encoding/json"
	"fmt"
	"metrolab/internal/engine"
	"metrolab/internal/store"
	"sort"
	"sync"
)

// Error categories mapped by the HTTP layer: invalid_input -> 400,
// state_conflict -> 409, internal -> 500.
type ErrorKind string

const (
	KindInvalid  ErrorKind = "invalid_input"
	KindConflict ErrorKind = "state_conflict"
	KindInternal ErrorKind = "internal"
)

// Error is the only error type crossing service boundaries.
type Error struct {
	Kind     ErrorKind        `json:"kind"`
	Code     string           `json:"code"`
	Message  string           `json:"message"`
	Problems []engine.Problem `json:"problems,omitempty"`
}

func (e *Error) Error() string { return string(e.Kind) + "/" + e.Code + ": " + e.Message }

func invalid(code, msg string, ps ...engine.Problem) *Error {
	return &Error{Kind: KindInvalid, Code: code, Message: msg, Problems: ps}
}
func conflict(code, msg string) *Error { return &Error{Kind: KindConflict, Code: code, Message: msg} }
func internalErr(msg string) *Error {
	return &Error{Kind: KindInternal, Code: "internal", Message: msg}
}

// Service is the authoritative state machine.
type Service struct {
	mu        sync.Mutex
	st        *store.Store
	obs       map[string]*Observation
	scenarios map[string]*Scenario
	certs     map[string]*Certificate
	results   map[string]*Result
	events    []Event
	idem      map[string]string     // request_id -> object id
	batchIdem map[string]batchIndex // batch request_id -> produced ids
	seq       int64
	idCounter int64
}

// New restores service state from the records replayed at store open.
func New(st *store.Store, recs []store.Record) (*Service, error) {
	s := &Service{
		st: st, obs: map[string]*Observation{}, scenarios: map[string]*Scenario{},
		certs: map[string]*Certificate{}, results: map[string]*Result{},
		idem: map[string]string{}, batchIdem: map[string]batchIndex{},
	}
	for _, r := range recs {
		if err := s.apply(r, false); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) newID(prefix string) string {
	s.idCounter++
	return fmt.Sprintf("%s_%06d", prefix, s.idCounter)
}

// emit appends an event durably and applies it. The idempotency check happens
// in the caller (which holds the domain context for a useful reply).
func (s *Service) emit(event string, id, requestID string, payload any) (Event, *Error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, internalErr(err.Error())
	}
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	seq, err := s.st.Append(store.Record{Event: event, ID: id, RequestID: requestID, Time: nowUTC(), Payload: b})
	if err != nil {
		return Event{}, internalErr("wal append: " + err.Error())
	}
	rec := store.Record{Seq: seq, Event: event, ID: id, RequestID: requestID, Time: nowUTC(), Payload: b}
	if err := s.apply(rec, true); err != nil {
		return Event{}, err
	}
	ev := Event{Seq: seq, Event: event, ID: id, RequestID: requestID, Time: rec.Time, Payload: raw}
	if ierr := s.materialize(); ierr != nil {
		return Event{}, internalErr(ierr.Error())
	}
	return ev, nil
}

// apply folds one record into in-memory state.
func (s *Service) apply(rec store.Record, assigned bool) *Error {
	s.seq = rec.Seq
	if assigned {
		s.events = append(s.events, Event{Seq: rec.Seq, Event: rec.Event, ID: rec.ID, RequestID: rec.RequestID, Time: rec.Time})
	} else {
		var pl map[string]any
		_ = json.Unmarshal(rec.Payload, &pl)
		s.events = append(s.events, Event{Seq: rec.Seq, Event: rec.Event, ID: rec.ID, RequestID: rec.RequestID, Time: rec.Time, Payload: pl})
	}
	var pl map[string]json.RawMessage
	if err := json.Unmarshal(rec.Payload, &pl); err != nil && string(rec.Payload) != "null" {
		return internalErr("corrupt event payload for " + rec.Event + " seq=" + fmt.Sprint(rec.Seq))
	}
	decode := func(v any) *Error {
		if raw, ok := pl["object"]; ok {
			if err := json.Unmarshal(raw, v); err != nil {
				return internalErr(err.Error())
			}
		}
		return nil
	}
	switch rec.Event {
	case "observation.put":
		var o Observation
		if e := decode(&o); e != nil {
			return e
		}
		s.obs[o.ID] = &o
	case "scenario.put":
		var sc Scenario
		if e := decode(&sc); e != nil {
			return e
		}
		s.scenarios[sc.ID] = &sc
	case "scenario.freeze":
		if sc := s.scenarios[rec.ID]; sc != nil {
			var p struct {
				Frozen   bool   `json:"frozen"`
				FrozenAt string `json:"frozen_at"`
			}
			_ = json.Unmarshal(rec.Payload, &p)
			sc.Frozen = p.Frozen
			sc.FrozenAt = p.FrozenAt
		}
	case "certificate.put":
		var c Certificate
		if e := decode(&c); e != nil {
			return e
		}
		s.certs[c.ID] = &c
	case "certificate.revoke":
		var p struct {
			RevokedAt string `json:"revoked_at"`
		}
		_ = json.Unmarshal(rec.Payload, &p)
		if c := s.certs[rec.ID]; c != nil {
			c.RevokedAt = p.RevokedAt
		}
	case "result.put":
		var r Result
		if e := decode(&r); e != nil {
			return e
		}
		s.results[r.ID] = &r
		if sc := s.scenarios[r.ScenarioID]; sc != nil && !sc.Frozen {
			sc.LatestResultID = r.ID
		}
	case "result.batch":
		var p struct {
			ResultIDs []string `json:"result_ids"`
		}
		_ = json.Unmarshal(rec.Payload, &p)
		if rec.RequestID != "" {
			s.batchIdem[rec.RequestID] = batchIndex{ResultIDs: p.ResultIDs}
		}
	case "result.confirm":
		var p struct {
			ResultID string `json:"result_id"`
		}
		_ = json.Unmarshal(rec.Payload, &p)
		if r := s.results[p.ResultID]; r != nil && r.Status == "pending" {
			r.Status = "ok"
			r.Confirmed = true
			r.Output = nil
		}
	}
	if rec.RequestID != "" {
		s.idem[rec.RequestID] = rec.ID
	}
	return nil
}

// materialize rewrites the three logical collections (inputs, derived
// results, events) after a successfully applied event.
func (s *Service) materialize() error {
	var obs []json.RawMessage
	for _, o := range sortedObs(s.obs) {
		b, _ := json.Marshal(o)
		obs = append(obs, b)
	}
	if err := s.st.RewriteCollection("observations", obs); err != nil {
		return err
	}
	var scs []json.RawMessage
	for _, sc := range sortedScenarios(s.scenarios) {
		b, _ := json.Marshal(sc)
		scs = append(scs, b)
	}
	if err := s.st.RewriteCollection("scenarios", scs); err != nil {
		return err
	}
	var cs []json.RawMessage
	for _, c := range sortedCerts(s.certs) {
		b, _ := json.Marshal(c)
		cs = append(cs, b)
	}
	if err := s.st.RewriteCollection("certificates", cs); err != nil {
		return err
	}
	var rs []json.RawMessage
	for _, r := range sortedResults(s.results) {
		b, _ := json.Marshal(r)
		rs = append(rs, b)
	}
	if err := s.st.RewriteCollection("results", rs); err != nil {
		return err
	}
	var evs []json.RawMessage
	for _, ev := range s.events {
		b, _ := json.Marshal(ev)
		evs = append(evs, b)
	}
	if err := s.st.RewriteCollection("events", evs); err != nil {
		return err
	}
	return nil
}

func sortedObs(m map[string]*Observation) []*Observation {
	var out []*Observation
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func sortedScenarios(m map[string]*Scenario) []*Scenario {
	var out []*Scenario
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func sortedCerts(m map[string]*Certificate) []*Certificate {
	var out []*Certificate
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func sortedResults(m map[string]*Result) []*Result {
	var out []*Result
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var _ = engine.Run

// emitBatch applies several already-sequenced events from one batch.
func (s *Service) emitBatch(records []store.Record) *Error {
	seqs, err := s.st.AppendBatch(records)
	if err != nil {
		return internalErr("wal append batch: " + err.Error())
	}
	for i, r := range records {
		r.Seq = seqs[i]
		if e := s.apply(r, true); e != nil {
			return e
		}
	}
	if ierr := s.materialize(); ierr != nil {
		return internalErr(ierr.Error())
	}
	return nil
}

func (s *Service) makeRecord(event, id, requestID string, payload any) (store.Record, *Error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return store.Record{}, internalErr(err.Error())
	}
	return store.Record{Event: event, ID: id, RequestID: requestID, Time: nowUTC(), Payload: b}, nil
}

func (s *Service) ListEvents(limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	evs := append([]Event(nil), s.events...)
	if limit > 0 && len(evs) > limit {
		evs = evs[len(evs)-limit:]
	}
	return evs
}
