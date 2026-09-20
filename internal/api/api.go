package api

import (
	"net/http"
	"time"

	"metrologylab/internal/domain"
	"metrologylab/internal/engine"
	"metrologylab/internal/store"
)

type Server struct {
	st  *store.Store
	eng *engine.Engine
	now func() time.Time
	mux *http.ServeMux
}
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details []domain.Issue `json:"details,omitempty"`
}
type ValidationErrors struct{ Issues []domain.Issue }

func (v *ValidationErrors) Error() string { return "input validation failed" }

type ConflictError struct {
	Code    string
	Message string
	Body    any
}

func (c *ConflictError) Error() string { return c.Message }

func New(st *store.Store, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	s := &Server{st: st, eng: engine.New(now), now: now, mux: http.NewServeMux()}
	s.routes()
	return withRecovery(withBody(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	s.mux.HandleFunc("GET /api/state", s.getState)
	s.mux.HandleFunc("POST /api/observations", s.createObservation)
	s.mux.HandleFunc("POST /api/groups", s.createGroup)
	s.mux.HandleFunc("POST /api/certificates", s.createCertificate)
	s.mux.HandleFunc("POST /api/scenarios", s.createScenario)
	s.mux.HandleFunc("POST /api/scenarios/{id}/copy", s.copyScenario)
	s.mux.HandleFunc("POST /api/scenarios/{id}/freeze", s.freezeScenario)
	s.mux.HandleFunc("POST /api/certificates/{id}/replace", s.replaceCertificate)
	s.mux.HandleFunc("POST /api/recalculations", s.recalculate)
	s.mux.HandleFunc("POST /api/recalculations/atomic", s.recalculateAtomic)
	s.mux.HandleFunc("GET /api/scenarios/{id}/comparison", s.comparison)
	s.mux.HandleFunc("POST /api/audits/export", s.exportAudit)
	s.mux.HandleFunc("POST /api/audits/import", s.importAudit)
	s.mux.HandleFunc("GET /", staticHandler(indexHTML))
}

type ObservationReq struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Value               float64 `json:"value"`
	Unit                string  `json:"unit"`
	StandardUncertainty float64 `json:"standardUncertainty"`
	Distribution        string  `json:"distribution"`
	DegreesOfFreedom    float64 `json:"degreesOfFreedom"`
	CorrelationGroupID  string  `json:"correlationGroupId"`
}
type GroupReq struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Coefficient float64 `json:"coefficient"`
}
type CertificateReq struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Correction          float64   `json:"correction"`
	Unit                string    `json:"unit"`
	StandardUncertainty float64   `json:"standardUncertainty"`
	DegreesOfFreedom    float64   `json:"degreesOfFreedom"`
	ValidFrom           time.Time `json:"validFrom"`
	ExpiresAt           time.Time `json:"expiresAt"`
}
type ScenarioReq struct {
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	OutputNodeId         string                       `json:"outputNodeId"`
	Nodes                []domain.Node                `json:"nodes"`
	CorrelationOverrides []domain.CorrelationOverride `json:"correlationOverrides"`
}
type CopyReq struct {
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	CorrelationOverrides []domain.CorrelationOverride `json:"correlationOverrides"`
}
type ReplaceReq struct {
	NewCertificate CertificateReq `json:"newCertificate"`
	NewID          string         `json:"newId"`
}
type RecalcReq struct {
	ScenarioIDs []string `json:"scenarioIds"`
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.st.State()) }

func (s *Server) createObservation(w http.ResponseWriter, r *http.Request) {
	var req ObservationReq
	if !decode(w, r, &req) {
		return
	}
	issues := validateID(req.ID)
	if req.Unit == "" || !knownUnit(req.Unit) {
		issues = append(issues, domain.Issue{Code: "INVALID_UNIT", Path: "unit", Message: "unit is required and supported"})
	}
	if req.StandardUncertainty < 0 {
		issues = append(issues, domain.Issue{Code: "INVALID_UNCERTAINTY", Path: "standardUncertainty", Message: "standard uncertainty must be non-negative"})
	}
	if !validDistribution(req.Distribution) {
		issues = append(issues, domain.Issue{Code: "INVALID_DISTRIBUTION", Path: "distribution", Message: "distribution must be normal, rectangular, triangular, t, or unknown"})
	}
	if req.Distribution == "t" && req.DegreesOfFreedom <= 0 {
		issues = append(issues, domain.Issue{Code: "INVALID_DEGREES_OF_FREEDOM", Path: "degreesOfFreedom", Message: "t distribution requires positive degrees of freedom"})
	}
	st0 := s.st.State()
	if req.CorrelationGroupID != "" {
		if _, ok := st0.Groups[req.CorrelationGroupID]; !ok {
			issues = append(issues, domain.Issue{Code: "CORRELATION_GROUP_NOT_FOUND", Path: "correlationGroupId", InputID: req.CorrelationGroupID, Message: "group does not exist"})
		}
	}
	if len(issues) > 0 {
		writeError(w, 400, &ValidationErrors{Issues: issues})
		return
	}
	body, code, err := s.mutate(r, "observation.created", "/api/observations", map[string]any{"id": req.ID}, func(st *domain.State) ([]byte, error) {
		if _, ok := st.Observations[req.ID]; ok {
			return nil, &ConflictError{Code: "OBSERVATION_EXISTS", Message: "observation already exists"}
		}
		st.Observations[req.ID] = domain.Observation{ID: req.ID, Name: req.Name, Value: req.Value, Unit: req.Unit, StandardUncertainty: req.StandardUncertainty, Distribution: req.Distribution, DegreesOfFreedom: req.DegreesOfFreedom, CorrelationGroupID: req.CorrelationGroupID, CreatedAt: s.now().UTC()}
		return jsonMust(map[string]any{"observation": st.Observations[req.ID]})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 201, raw(body))
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var req GroupReq
	if !decode(w, r, &req) {
		return
	}
	issues := validateID(req.ID)
	if req.Coefficient < -1 || req.Coefficient > 1 {
		issues = append(issues, domain.Issue{Code: "CORRELATION_NOT_PSD", Path: "coefficient", Message: "coefficient must be in [-1,1]"})
	}
	if len(issues) > 0 {
		writeError(w, 400, &ValidationErrors{Issues: issues})
		return
	}
	body, _, err := s.mutate(r, "group.created", "/api/groups", map[string]any{"id": req.ID}, func(st *domain.State) ([]byte, error) {
		if _, ok := st.Groups[req.ID]; ok {
			return nil, &ConflictError{Code: "GROUP_EXISTS", Message: "group already exists"}
		}
		st.Groups[req.ID] = domain.CorrelationGroup{ID: req.ID, Name: req.Name, Coefficient: req.Coefficient, CreatedAt: s.now().UTC()}
		return jsonMust(map[string]any{"group": st.Groups[req.ID]})
	})
	if err != nil {
		writeStoreError(w, 400, err, nil)
		return
	}
	writeJSON(w, 201, raw(body))
}

func (s *Server) createCertificate(w http.ResponseWriter, r *http.Request) {
	var req CertificateReq
	if !decode(w, r, &req) {
		return
	}
	issues := validateID(req.ID)
	if req.Unit == "" || !knownUnit(req.Unit) {
		issues = append(issues, domain.Issue{Code: "INVALID_UNIT", Path: "unit", Message: "unit is required and supported"})
	}
	if req.StandardUncertainty < 0 {
		issues = append(issues, domain.Issue{Code: "INVALID_UNCERTAINTY", Path: "standardUncertainty", Message: "standard uncertainty must be non-negative"})
	}
	if !req.ExpiresAt.After(req.ValidFrom) {
		issues = append(issues, domain.Issue{Code: "INVALID_CERTIFICATE_PERIOD", Path: "expiresAt", Message: "expiresAt must be after validFrom"})
	}
	if req.DegreesOfFreedom <= 0 {
		issues = append(issues, domain.Issue{Code: "INVALID_DEGREES_OF_FREEDOM", Path: "degreesOfFreedom", Message: "degrees of freedom must be positive"})
	}
	if len(issues) > 0 {
		writeError(w, 400, &ValidationErrors{Issues: issues})
		return
	}
	body, code, err := s.mutate(r, "certificate.created", "/api/certificates", map[string]any{"id": req.ID}, func(st *domain.State) ([]byte, error) {
		if _, ok := st.Certificates[req.ID]; ok {
			return nil, &ConflictError{Code: "CERTIFICATE_EXISTS", Message: "certificate already exists"}
		}
		st.Certificates[req.ID] = certFromReq(req, s.now().UTC())
		return jsonMust(map[string]any{"certificate": st.Certificates[req.ID]})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 201, raw(body))
}

func (s *Server) createScenario(w http.ResponseWriter, r *http.Request) {
	var req ScenarioReq
	if !decode(w, r, &req) {
		return
	}
	if issues := validateID(req.ID); len(issues) > 0 {
		writeError(w, 400, &ValidationErrors{Issues: issues})
		return
	}
	sc := domain.Scenario{ID: req.ID, Name: req.Name, OutputNode: req.OutputNodeId, Nodes: req.Nodes, Overrides: req.CorrelationOverrides, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()}
	if res := s.eng.Evaluate(sc, s.st.State()); len(res.BlockingIssues) > 0 && !onlyExpired(res.BlockingIssues) {
		writeError(w, 400, &ValidationErrors{Issues: res.BlockingIssues})
		return
	}
	body, code, err := s.mutate(r, "scenario.created", "/api/scenarios", map[string]any{"id": req.ID}, func(st *domain.State) ([]byte, error) {
		if _, ok := st.Scenarios[req.ID]; ok {
			return nil, &ConflictError{Code: "SCENARIO_EXISTS", Message: "scenario already exists"}
		}
		res := s.eng.Evaluate(sc, st)
		st.Scenarios[sc.ID] = sc
		st.Results[sc.ID] = res
		return jsonMust(map[string]any{"scenario": sc, "result": res})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 201, raw(body))
}

func (s *Server) copyScenario(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")
	var req CopyReq
	if !decode(w, r, &req) {
		return
	}
	issues := validateID(req.ID)
	if len(issues) > 0 {
		writeError(w, 400, &ValidationErrors{Issues: issues})
		return
	}
	st0 := s.st.State()
	src, ok := st0.Scenarios[sourceID]
	if !ok {
		writeError(w, 404, &ValidationErrors{[]domain.Issue{{Code: "SCENARIO_NOT_FOUND", InputID: sourceID, Message: "source scenario is missing"}}})
		return
	}
	if _, exists := st0.Scenarios[req.ID]; exists {
		writeError(w, 409, &ConflictError{Code: "SCENARIO_EXISTS", Message: "target scenario already exists"})
		return
	}
	cp := src
	cp.ID = req.ID
	cp.Name = req.Name
	cp.FrozenResultID = ""
	cp.Overrides = req.CorrelationOverrides
	cp.CreatedAt = s.now().UTC()
	cp.UpdatedAt = s.now().UTC()
	if res := s.eng.Evaluate(cp, st0); len(res.BlockingIssues) > 0 && !onlyExpired(res.BlockingIssues) {
		writeError(w, 400, &ValidationErrors{Issues: res.BlockingIssues})
		return
	}
	body, code, err := s.mutate(r, "scenario.copied", "/api/scenarios/"+sourceID+"/copy", map[string]any{"sourceId": sourceID, "id": req.ID}, func(st *domain.State) ([]byte, error) {
		if _, ok := st.Scenarios[sourceID]; !ok {
			return nil, &ConflictError{Code: "SCENARIO_NOT_FOUND", Message: "source scenario disappeared"}
		}
		if _, ok := st.Scenarios[req.ID]; ok {
			return nil, &ConflictError{Code: "SCENARIO_EXISTS", Message: "target scenario already exists"}
		}
		res := s.eng.Evaluate(cp, st)
		st.Scenarios[cp.ID] = cp
		st.Results[cp.ID] = res
		return jsonMust(map[string]any{"scenario": cp, "result": res})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 201, raw(body))
}
