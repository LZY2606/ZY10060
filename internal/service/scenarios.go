package service

import (
	"metrolab/internal/engine"
)

type ScenarioInput struct {
	Name      string         `json:"name"`
	Nodes     []ScenarioNode `json:"nodes"`
	Edges     []ScenarioEdge `json:"edges"`
	Groups    []CorrGroup    `json:"groups"`
	Pairs     []CorrPair     `json:"pairs"`
	CoverageP float64        `json:"coverage_p,omitempty"`
}

func (s *Service) CreateScenario(in ScenarioInput, requestID string) (*Scenario, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requestID != "" {
		if id, ok := s.idem[requestID]; ok {
			if sc := s.scenarios[id]; sc != nil {
				return sc, nil
			}
		}
	}
	if e := s.validateScenario(in); e != nil {
		return nil, e
	}
	id := s.newID("scn")
	sc := &Scenario{ID: id, Name: in.Name, Nodes: in.Nodes, Edges: in.Edges,
		Groups: in.Groups, Pairs: in.Pairs, CoverageP: in.CoverageP, CreatedAt: nowUTC()}
	if _, e := s.emit("scenario.put", id, requestID, map[string]any{"object": sc}); e != nil {
		return nil, e
	}
	return sc, nil
}

// UpdateScenario changes graph/correlation of a draft scenario.
func (s *Service) UpdateScenario(id string, in ScenarioInput, requestID string) (*Scenario, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.scenarios[id]
	if sc == nil {
		return nil, invalid("not_found", "no such scenario: "+id)
	}
	if sc.Frozen {
		return nil, conflict("scenario_frozen", "frozen scenarios cannot be modified; copy it first")
	}
	if requestID != "" {
		if rid, ok := s.idem[requestID]; ok && rid != id {
			return nil, conflict("idempotency_collision", "request_id already used for another object")
		}
	}
	if e := s.validateScenario(in); e != nil {
		return nil, e
	}
	sc.Name, sc.Nodes, sc.Edges, sc.Groups, sc.Pairs, sc.CoverageP =
		in.Name, in.Nodes, in.Edges, in.Groups, in.Pairs, in.CoverageP
	sc.LatestResultID = ""
	if _, e := s.emit("scenario.put", id, requestID, map[string]any{"object": sc}); e != nil {
		return nil, e
	}
	return sc, nil
}

// CopyScenario clones a (possibly frozen) scenario into a new editable draft,
// optionally replacing correlation definitions.
func (s *Service) CopyScenario(id, name string, groups *[]CorrGroup, pairs *[]CorrPair, requestID string) (*Scenario, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.scenarios[id]
	if src == nil {
		return nil, invalid("not_found", "no such scenario: "+id)
	}
	if requestID != "" {
		if rid, ok := s.idem[requestID]; ok {
			if sc := s.scenarios[rid]; sc != nil {
				return sc, nil
			}
		}
	}
	ns := *src
	ng := make([]ScenarioNode, len(src.Nodes))
	copy(ng, src.Nodes)
	ne := make([]ScenarioEdge, len(src.Edges))
	copy(ne, src.Edges)
	ns.Nodes, ns.Edges = ng, ne
	g, p2 := append([]CorrGroup(nil), src.Groups...), append([]CorrPair(nil), src.Pairs...)
	if groups != nil {
		g = *groups
	}
	if pairs != nil {
		p2 = *pairs
	}
	ns.Groups, ns.Pairs = g, p2
	in := ScenarioInput{Name: name, Nodes: ns.Nodes, Edges: ns.Edges, Groups: ns.Groups, Pairs: ns.Pairs, CoverageP: ns.CoverageP}
	if e := s.validateScenario(in); e != nil {
		return nil, e
	}
	newID := s.newID("scn")
	ns.ID = newID
	ns.Name = name
	ns.Frozen = false
	ns.FrozenAt = ""
	ns.LatestResultID = ""
	ns.CreatedAt = nowUTC()
	ns.SourceOf = ""
	if _, e := s.emit("scenario.put", newID, requestID, map[string]any{"object": ns, "source_of": id}); e != nil {
		return nil, e
	}
	// remember lineage on the child (stored inside object via extra event)
	src.SourceOf = newID
	return &ns, nil
}

// FreezeScenario pins a scenario and its latest (or freshly computed) result.
func (s *Service) FreezeScenario(id string, requestID string) (*Scenario, *Result, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.scenarios[id]
	if sc == nil {
		return nil, nil, invalid("not_found", "no such scenario: "+id)
	}
	if sc.Frozen {
		return sc, s.results[sc.LatestResultID], nil
	}
	res := s.results[sc.LatestResultID]
	if res == nil {
		r, e := s.computeLocked(sc, requestID, false)
		if e != nil {
			return nil, nil, e
		}
		res = r
	}
	if res.Status == "pending" {
		return nil, nil, conflict("result_pending", "cannot freeze a result awaiting expired-certificate confirmation")
	}
	sc.Frozen = true
	sc.FrozenAt = nowUTC()
	sc.LatestResultID = res.ID
	if _, e := s.emit("scenario.freeze", sc.ID, requestID, map[string]any{"frozen": true, "frozen_at": sc.FrozenAt, "result_id": res.ID}); e != nil {
		return nil, nil, e
	}
	if _, e := s.emit("scenario.put", sc.ID, "", map[string]any{"object": sc}); e != nil {
		return nil, nil, e
	}
	return sc, res, nil
}

func (s *Service) ListScenarios() []*Scenario {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedScenarios(s.scenarios)
}

func (s *Service) GetScenario(id string) (*Scenario, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.scenarios[id]
	if sc == nil {
		return nil, invalid("not_found", "no such scenario: "+id)
	}
	return sc, nil
}

func (s *Service) validateScenario(in ScenarioInput) *Error {
	knownNodes := map[string]bool{}
	for _, n := range in.Nodes {
		if n.ID == "" {
			return invalid("empty_node_id", "every node needs an id")
		}
		if knownNodes[n.ID] {
			return invalid("duplicate_node_id", "duplicate node id: "+n.ID)
		}
		knownNodes[n.ID] = true
	}
	knownObs := map[string]bool{}
	for id := range s.obs {
		knownObs[id] = true
	}
	knownCerts := map[string]bool{}
	for id := range s.certs {
		knownCerts[id] = true
	}
	groupIDs := map[string]bool{}
	memberGroup := map[string]string{}
	for _, g := range in.Groups {
		if g.ID == "" {
			return invalid("empty_group_id", "group id required")
		}
		if groupIDs[g.ID] {
			return invalid("duplicate_group_id", "duplicate group id: "+g.ID)
		}
		groupIDs[g.ID] = true
	}
	for _, g := range in.Groups {
		for _, m := range g.Members {
			if !knownObs[m] {
				return invalid("unknown_group_member", "correlation group "+g.ID+" references unknown observation "+m)
			}
			if prev, dup := memberGroup[m]; dup {
				return invalid("ambiguous_group_membership", "observation "+m+" belongs to both groups "+prev+" and "+g.ID)
			}
			memberGroup[m] = g.ID
		}
	}
	for _, pr := range in.Pairs {
		if !knownObs[pr.A] || !knownObs[pr.B] {
			return invalid("unknown_pair_member", "pair members must be existing observations")
		}
	}
	for _, e := range in.Edges {
		if !knownNodes[e.To] {
			return invalid("edge_target_not_node", "edge "+e.From+"->"+e.To+" does not target a declared node")
		}
		if !knownObs[e.From] && !knownNodes[e.From] {
			return invalid("edge_source_unknown", "edge source does not exist: "+e.From)
		}
	}
	for _, n := range in.Nodes {
		if n.Kind == engine.NodeCalibration {
			c := s.certs[n.CertificateID]
			if c == nil {
				return invalid("unknown_certificate", "node "+n.ID+" references missing certificate "+n.CertificateID)
			}
			if c.RevokedAt != "" {
				return invalid("revoked_certificate", "node "+n.ID+" references revoked/superseded certificate "+n.CertificateID+"; use the current revision")
			}
		}
	}
	return nil
}
