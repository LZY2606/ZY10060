package service

import "metrolab/internal/engine"

type ObservationInput struct {
	ID             string              `json:"id,omitempty"`
	Value          *float64            `json:"value"`
	Unit           string              `json:"unit"`
	StdUncertainty *float64            `json:"std_uncertainty"`
	Distribution   engine.Distribution `json:"distribution"`
	Nu             *float64            `json:"nu,omitempty"`
}

// PutObservation creates an immutable original input record.
func (s *Service) PutObservation(in ObservationInput, requestID string) (*Observation, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requestID != "" {
		if id, ok := s.idem[requestID]; ok {
			if o := s.obs[id]; o != nil {
				return o, nil
			}
		}
	}
	if in.Value == nil || in.StdUncertainty == nil {
		return nil, invalid("missing_fields", "value and std_uncertainty are required")
	}
	if in.Unit == "" {
		return nil, invalid("missing_fields", "unit is required")
	}
	switch in.Distribution {
	case engine.DistNormal, engine.DistT, engine.DistRectangular, engine.DistTriangular, engine.DistUshaped:
	default:
		return nil, invalid("bad_distribution", "distribution must be one of normal,t,rectangular,triangular,u-shaped")
	}
	if *in.StdUncertainty < 0 {
		return nil, invalid("bad_uncertainty", "std_uncertainty must be >= 0")
	}
	if in.Distribution == engine.DistT && (in.Nu == nil || *in.Nu <= 0) {
		return nil, invalid("bad_distribution", "t distribution needs positive nu")
	}
	id := in.ID
	if id == "" {
		id = s.newID("obs")
	} else if _, exists := s.obs[id]; exists {
		return nil, conflict("observation_exists", "observation id already exists: "+id)
	}
	o := &Observation{
		ID: id, Value: *in.Value, Unit: in.Unit, StdUncertainty: *in.StdUncertainty,
		Distribution: in.Distribution, Nu: in.Nu, CreatedAt: nowUTC(),
	}
	if _, e := s.emit("observation.put", id, requestID, map[string]any{"object": o}); e != nil {
		return nil, e
	}
	return o, nil
}

func (s *Service) ListObservations() []*Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedObs(s.obs)
}
