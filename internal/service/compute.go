package service

import (
	"metrolab/internal/engine"
	"sort"
	"time"
)

// buildSpec materializes the engine snapshot from current stored inputs.
func (s *Service) buildSpec(sc *Scenario) engine.Spec {
	var spec engine.Spec
	usedObs := map[string]bool{}
	usedCerts := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if usedObs[id] {
			return
		}
		if _, ok := s.obs[id]; ok {
			usedObs[id] = true
		}
	}
	nodeByID := map[string]ScenarioNode{}
	for _, n := range sc.Nodes {
		nodeByID[n.ID] = n
	}
	visited := map[string]bool{}
	var walkNode func(id string)
	walkNode = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, ed := range sc.Edges {
			if ed.To == id {
				walk(ed.From)
				if _, isNode := nodeByID[ed.From]; isNode {
					walkNode(ed.From)
				}
			}
		}
	}
	for _, n := range sc.Nodes {
		walkNode(n.ID)
		if n.Kind == engine.NodeCalibration {
			usedCerts[n.CertificateID] = true
			usedObs[n.ReferenceLeaf] = true
		}
	}
	var obsIDs []string
	for id := range usedObs {
		obsIDs = append(obsIDs, id)
	}
	sort.Strings(obsIDs)
	for _, id := range obsIDs {
		o := s.obs[id]
		leaf := engine.LeafSpec{ID: o.ID, Value: o.Value, Unit: o.Unit,
			StdUncertainty: o.StdUncertainty, Distribution: o.Distribution, Nu: o.Nu}
		for _, g := range sc.Groups {
			for _, m := range g.Members {
				if m == id {
					if leaf.GroupID != "" {
						leaf.GroupID = leaf.GroupID + "," + g.ID
					} else {
						leaf.GroupID = g.ID
					}
				}
			}
		}
		spec.Leaves = append(spec.Leaves, leaf)
	}
	for _, n := range sc.Nodes {
		spec.Nodes = append(spec.Nodes, engine.NodeSpec{
			ID: n.ID, Kind: n.Kind, Unit: n.Unit, Weights: n.Weights,
			ReferenceLeaf: n.ReferenceLeaf, CertificateID: n.CertificateID,
		})
	}
	for _, e := range sc.Edges {
		spec.Edges = append(spec.Edges, engine.EdgeSpec{From: e.From, To: e.To, Role: e.Role, Weight: e.Weight})
	}
	for _, g := range sc.Groups {
		spec.Groups = append(spec.Groups, engine.GroupSpec{ID: g.ID, Correlation: g.Correlation})
	}
	for _, p2 := range sc.Pairs {
		spec.PairRules = append(spec.PairRules, engine.PairRule{A: p2.A, B: p2.B, Correlation: p2.Correlation})
	}
	var certIDs []string
	for id := range usedCerts {
		certIDs = append(certIDs, id)
	}
	sort.Strings(certIDs)
	for _, id := range certIDs {
		c := s.certs[id]
		spec.Certificates = append(spec.Certificates, engine.CertInfo{
			ID: c.ID, Slope: c.Slope, Intercept: c.Intercept, SlopeU: c.SlopeU,
			InterceptU: c.InterceptU, SlopeInterceptR: c.SlopeInterceptR,
			Nu: c.Nu, ValidUntil: c.ValidUntil, RevokedAt: c.RevokedAt,
		})
	}
	spec.CoverageP = sc.CoverageP
	return spec
}

type ComputeReply struct {
	ResultID string  `json:"result_id"`
	Status   string  `json:"status"`
	Result   *Result `json:"result"`
}

// Compute evaluates a draft scenario; caller must hold s.mu.
func (s *Service) computeLocked(sc *Scenario, requestID string, confirmed bool, asOf ...string) (*Result, *Error) {
	var spec engine.Spec
	if sc.Frozen && sc.LatestResultID != "" {
		if old := s.results[sc.LatestResultID]; old != nil {
			spec = old.Spec
		}
	} else {
		spec = s.buildSpec(sc)
	}
	if sc.Frozen {
		spec.Frozen = true
	}
	if len(asOf) > 0 && asOf[0] != "" {
		spec.Now = asOf[0]
	}
	out, probs := engine.Run(spec, confirmed)
	if probs != nil {
		return nil, invalid("calculation_rejected", "inputs failed metrological validation", probs...)
	}
	if out.Status == "pending" {
		out.RedactConfidence()
	}
	id := s.newID("res")
	r := &Result{ID: id, ScenarioID: sc.ID, Spec: spec, Output: out,
		Status: out.Status, Confirmed: confirmed, RequestID: requestID, CreatedAt: nowUTC(),
		AuditDigest: out.ChecksumInputs}
	if _, e := s.emit("result.put", id, requestID, map[string]any{"object": r}); e != nil {
		return nil, e
	}
	return r, nil
}

// Compute runs a fresh calculation for a scenario.
func (s *Service) Compute(scenarioID, requestID string, confirmed bool, asOf string) (*ComputeReply, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.scenarios[scenarioID]
	if sc == nil {
		return nil, invalid("not_found", "no such scenario: "+scenarioID)
	}
	if requestID != "" {
		if id, ok := s.idem[requestID]; ok {
			if r := s.results[id]; r != nil {
				return &ComputeReply{ResultID: r.ID, Status: r.Status, Result: r}, nil
			}
		}
	}
	if asOf != "" {
		if _, err := time.Parse(time.RFC3339, asOf); err != nil {
			return nil, invalid("bad_as_of", "as_of must be RFC3339")
		}
	}
	r, e := s.computeLocked(sc, requestID, confirmed, asOf)
	if e != nil {
		return nil, e
	}
	if r.Status == "pending" && r.Output != nil {
		r.Output.RedactConfidence()
	}
	return &ComputeReply{ResultID: r.ID, Status: r.Status, Result: r}, nil
}

// ConfirmPending explicitly accepts a calculation that depended on an expired
// certificate, then re-runs it (numbers become visible only after this step).
func (s *Service) ConfirmPending(resultID, requestID string) (*Result, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.results[resultID]
	if r == nil {
		return nil, invalid("not_found", "no such result: "+resultID)
	}
	sc := s.scenarios[r.ScenarioID]
	if sc == nil {
		return nil, internalErr("result has no scenario")
	}
	if r.Status != "pending" {
		return r, nil
	}
	if sc.Frozen {
		return nil, conflict("scenario_frozen", "frozen results cannot be re-confirmed")
	}
	if _, e := s.emit("result.confirm", resultID, requestID, map[string]any{"result_id": resultID}); e != nil {
		return nil, e
	}
	// recompute with confirmed=true and persist a fresh result row.
	nr, e := s.computeLocked(sc, requestID, true)
	if e != nil {
		return nil, e
	}
	return nr, nil
}

func (s *Service) GetResult(id string) (*Result, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.results[id]
	if r == nil {
		return nil, invalid("not_found", "no such result: "+id)
	}
	if r.Status == "pending" && r.Output != nil {
		r.Output.RedactConfidence()
	}
	return r, nil
}

func (s *Service) ListResults(scenarioID string) []*Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Result
	for _, r := range sortedResults(s.results) {
		if scenarioID == "" || r.ScenarioID == scenarioID {
			if r.Status == "pending" && r.Output != nil {
				r.Output.RedactConfidence()
			}
			out = append(out, r)
		}
	}
	return out
}
