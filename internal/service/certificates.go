package service

import (
	"metrolab/internal/store"
	"sort"
)

type CertificateInput struct {
	Slope           float64 `json:"slope"`
	Intercept       float64 `json:"intercept"`
	SlopeU          float64 `json:"slope_u"`
	InterceptU      float64 `json:"intercept_u"`
	SlopeInterceptR float64 `json:"slope_intercept_r"`
	Nu              float64 `json:"nu"`
	ValidFrom       string  `json:"valid_from"`
	ValidUntil      string  `json:"valid_until"`
}

func (s *Service) PutCertificate(in CertificateInput, requestID string) (*Certificate, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putCertLocked(in, "", requestID)
}

func (s *Service) putCertLocked(in CertificateInput, supersedes, requestID string) (*Certificate, *Error) {
	if requestID != "" {
		if id, ok := s.idem[requestID]; ok {
			if c := s.certs[id]; c != nil {
				return c, nil
			}
		}
	}
	if in.Nu <= 0 {
		return nil, invalid("bad_certificate", "nu must be positive")
	}
	if in.SlopeU < 0 || in.InterceptU < 0 {
		return nil, invalid("bad_certificate", "uncertainties must be >= 0")
	}
	if in.SlopeInterceptR < -1 || in.SlopeInterceptR > 1 {
		return nil, invalid("bad_certificate", "slope/intercept correlation must be within [-1,1]")
	}
	if in.ValidFrom == "" || in.ValidUntil == "" || in.ValidFrom > in.ValidUntil {
		return nil, invalid("bad_certificate", "valid_from/valid_until are required and ordered (RFC3339)")
	}
	id := s.newID("crt")
	rev := 1
	if supersedes != "" {
		if old := s.certs[supersedes]; old != nil {
			rev = old.Revision + 1
		}
	}
	c := &Certificate{ID: id, Revision: rev, Supersedes: supersedes,
		Slope: in.Slope, Intercept: in.Intercept, SlopeU: in.SlopeU, InterceptU: in.InterceptU,
		SlopeInterceptR: in.SlopeInterceptR, Nu: in.Nu,
		ValidFrom: in.ValidFrom, ValidUntil: in.ValidUntil, CreatedAt: nowUTC()}
	if _, e := s.emit("certificate.put", id, requestID, map[string]any{"object": c}); e != nil {
		return nil, e
	}
	if supersedes != "" {
		old := s.certs[supersedes]
		if old != nil && old.RevokedAt == "" {
			if _, e := s.emit("certificate.revoke", supersedes, "", map[string]any{"revoked_at": nowUTC(), "replaced_by": id}); e != nil {
				return nil, e
			}
		}
	}
	return c, nil
}

// ReplaceCertificate installs a new revision and reports every result affected.
// Nothing is recomputed until the user selects individual or atomic batch mode.
func (s *Service) ReplaceCertificate(oldID string, in CertificateInput, requestID string) (*Certificate, []AffectedResult, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.certs[oldID] == nil {
		return nil, nil, invalid("not_found", "no such certificate: "+oldID)
	}
	c, e := s.putCertLocked(in, oldID, requestID)
	if e != nil {
		return nil, nil, e
	}
	// point calibration nodes referencing the old certificate to the new one on
	// draft scenarios; frozen scenarios keep the pinned snapshot.
	for _, sc := range s.scenarios {
		if sc.Frozen {
			continue
		}
		changed := false
		for i := range sc.Nodes {
			if sc.Nodes[i].Kind == "calibration" && sc.Nodes[i].CertificateID == oldID {
				sc.Nodes[i].CertificateID = c.ID
				changed = true
			}
		}
		if changed {
			if _, ee := s.emit("scenario.put", sc.ID, "", map[string]any{"object": sc}); ee != nil {
				return nil, nil, ee
			}
		}
	}
	return c, s.affectedByCert(oldID), nil
}

// affectedByCert lists every non-archived result whose snapshot used certID.
func (s *Service) affectedByCert(certID string) []AffectedResult {
	var out []AffectedResult
	for _, r := range sortedResults(s.results) {
		used := false
		for _, c := range r.Spec.Certificates {
			if c.ID == certID {
				used = true
			}
		}
		if !used {
			continue
		}
		sc := s.scenarios[r.ScenarioID]
		reason := "snapshot used certificate " + certID
		if sc != nil && sc.Frozen {
			reason = "frozen snapshot reproducible; new calculations will use replacement"
		}
		out = append(out, AffectedResult{ResultID: r.ID, ScenarioID: r.ScenarioID,
			Status: r.Status, Frozen: sc != nil && sc.Frozen, Reason: reason})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ResultID < out[j].ResultID })
	return out
}

func (s *Service) AffectedByCertificate(certID string) []AffectedResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.certs[certID] == nil {
		return nil
	}
	return s.affectedByCert(certID)
}

// RecomputeResult recomputes one result's scenario (individual mode). Frozen
// scenarios are skipped: their pinned snapshot remains reproducible.
func (s *Service) RecomputeResult(resultID, requestID string) (*Result, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.results[resultID]
	if old == nil {
		return nil, invalid("not_found", "no such result: "+resultID)
	}
	sc := s.scenarios[old.ScenarioID]
	if sc == nil {
		return nil, internalErr("result has no scenario")
	}
	if sc.Frozen {
		return nil, conflict("scenario_frozen", "frozen result cannot be recomputed in place; unfreeze via copy")
	}
	return s.computeLocked(sc, requestID, false)
}

// RecomputeBatch recomputes many draft results atomically: either every new
// result lands or none does (single WAL batch, single fsync).
func (s *Service) RecomputeBatch(resultIDs []string, requestID string) ([]*Result, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Idempotent replay: the same batch request_id always returns its
	// original results and never creates a second business outcome.
	if requestID != "" {
		if ev, ok := s.batchIdem[requestID]; ok {
			var out []*Result
			for _, id := range ev.ResultIDs {
				if r := s.results[id]; r != nil {
					out = append(out, r)
				}
			}
			if len(out) == len(ev.ResultIDs) {
				return out, nil
			}
		}
	}
	for _, id := range resultIDs {
		r := s.results[id]
		if r == nil {
			return nil, invalid("not_found", "no such result: "+id)
		}
		if sc := s.scenarios[r.ScenarioID]; sc != nil && sc.Frozen {
			return nil, conflict("scenario_frozen", "batch contains frozen result "+id)
		}
	}
	type prepared struct {
		r   *Result
		sc  *Scenario
		rid string
	}
	var items []prepared
	for i, id := range resultIDs {
		old := s.results[id]
		sc := s.scenarios[old.ScenarioID]
		spec := s.buildSpec(sc)
		out, probs := runForValidation(spec)
		if probs != nil {
			return nil, invalid("batch_aborted", "atomic batch rejected: "+id+" failed validation", probs...)
		}
		rid := requestID
		if i > 0 {
			rid = requestID + "#" + itoaS(i)
		}
		rid2 := s.newID("res")
		r := &Result{ID: rid2, ScenarioID: sc.ID, Spec: spec, Output: out,
			Status: out.Status, Confirmed: false, RequestID: rid, CreatedAt: nowUTC(),
			AuditDigest: out.ChecksumInputs}
		items = append(items, prepared{r: r, sc: sc, rid: rid})
	}
	var recs []store.Record
	var newIDs []string
	for _, it := range items {
		rec, e := s.makeRecord("result.put", it.r.ID, it.rid, map[string]any{"object": it.r})
		if e != nil {
			return nil, e
		}
		recs = append(recs, rec)
		newIDs = append(newIDs, it.r.ID)
	}
	if requestID != "" {
		rec, e := s.makeRecord("result.batch", requestID, requestID, map[string]any{"result_ids": newIDs})
		if e != nil {
			return nil, e
		}
		recs = append(recs, rec)
	}
	if e := s.emitBatch(recs); e != nil {
		return nil, e
	}
	if requestID != "" {
		s.batchIdem[requestID] = batchIndex{ResultIDs: newIDs}
	}
	var out []*Result
	for _, id := range newIDs {
		out = append(out, s.results[id])
	}
	return out, nil
}

func (s *Service) ListCertificates() []*Certificate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedCerts(s.certs)
}

func itoaS(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
