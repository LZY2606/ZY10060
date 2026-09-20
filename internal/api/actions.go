package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"metrologylab/internal/domain"
)

func (s *Server) freezeScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st0 := s.st.State()
	sc, ok := st0.Scenarios[id]
	if !ok {
		writeError(w, 404, &ValidationErrors{[]domain.Issue{{Code: "SCENARIO_NOT_FOUND", InputID: id, Message: "scenario is missing"}}})
		return
	}
	res := st0.Results[id]
	if sc.FrozenResultID != "" {
		writeError(w, 409, &ConflictError{Code: "SCENARIO_ALREADY_FROZEN", Message: "scenario already has a frozen result", Body: st0.FrozenResults[sc.FrozenResultID]})
		return
	}
	if res.Status != "ready" {
		writeError(w, 409, &ConflictError{Code: "RESULT_NOT_READY", Message: "only a ready result can be frozen", Body: res})
		return
	}
	frozenID := id + "-frozen-" + fmtID(s.now())
	body, code, err := s.mutate(r, "scenario.frozen", "/api/scenarios/"+id+"/freeze", map[string]any{"id": id, "frozenResultId": frozenID}, func(st *domain.State) ([]byte, error) {
		sc, ok := st.Scenarios[id]
		if !ok {
			return nil, &ConflictError{Code: "SCENARIO_NOT_FOUND", Message: "scenario disappeared"}
		}
		if sc.FrozenResultID != "" {
			return nil, &ConflictError{Code: "SCENARIO_ALREADY_FROZEN", Message: "already frozen"}
		}
		f := domain.FrozenResult{ID: frozenID, ScenarioID: id, Result: st.Results[id], FrozenAt: s.now().UTC()}
		st.FrozenResults[frozenID] = f
		sc.FrozenResultID = frozenID
		st.Scenarios[id] = sc
		return jsonMust(map[string]any{"frozen": f})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 200, raw(body))
}

func (s *Server) replaceCertificate(w http.ResponseWriter, r *http.Request) {
	oldID := r.PathValue("id")
	var req ReplaceReq
	if !decode(w, r, &req) {
		return
	}
	req.NewCertificate.ID = firstNonEmpty(req.NewID, req.NewCertificate.ID, oldID+"-revised")
	if req.NewCertificate.ID == oldID {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "INVALID_CERTIFICATE_ID", Path: "newId", Message: "replacement certificate id must differ"}}})
		return
	}
	st0 := s.st.State()
	old, ok := st0.Certificates[oldID]
	if !ok {
		writeError(w, 404, &ValidationErrors{[]domain.Issue{{Code: "CERTIFICATE_NOT_FOUND", InputID: oldID, Message: "certificate is missing"}}})
		return
	}
	if old.ReplacedBy != "" {
		writeError(w, 409, &ConflictError{Code: "CERTIFICATE_ALREADY_REPLACED", Message: "certificate has already been replaced", Body: old})
		return
	}
	newCert := certFromReq(req.NewCertificate, s.now().UTC())
	if !newCert.ExpiresAt.After(newCert.ValidFrom) || newCert.StandardUncertainty < 0 || newCert.DegreesOfFreedom <= 0 {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "INVALID_CERTIFICATE", Path: "newCertificate", Message: "replacement certificate is invalid"}}})
		return
	}
	affected := s.certificateUsers(st0, oldID)
	body, code, err := s.mutate(r, "certificate.replaced", "/api/certificates/"+oldID+"/replace", map[string]any{"oldId": oldID, "newId": newCert.ID, "affectedScenarioIds": affected}, func(st *domain.State) ([]byte, error) {
		old, ok := st.Certificates[oldID]
		if !ok {
			return nil, &ConflictError{Code: "CERTIFICATE_NOT_FOUND", Message: "certificate disappeared"}
		}
		if old.ReplacedBy != "" {
			return nil, &ConflictError{Code: "CERTIFICATE_ALREADY_REPLACED", Message: "already replaced"}
		}
		st.Certificates[newCert.ID] = newCert
		old.ReplacedBy = newCert.ID
		st.Certificates[oldID] = old
		return jsonMust(map[string]any{"oldCertificate": old, "newCertificate": newCert, "affectedScenarioIds": affected, "affectedResults": affectedResults(st, affected), "recalculationRequired": true})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 200, raw(body))
}

func (s *Server) recalculate(w http.ResponseWriter, r *http.Request)       { s.doRecalc(w, r, false) }
func (s *Server) recalculateAtomic(w http.ResponseWriter, r *http.Request) { s.doRecalc(w, r, true) }
func (s *Server) doRecalc(w http.ResponseWriter, r *http.Request, atomic bool) {
	var req RecalcReq
	if !decode(w, r, &req) {
		return
	}
	if len(req.ScenarioIDs) == 0 {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "SCENARIO_IDS_REQUIRED", Path: "scenarioIds", Message: "at least one scenario is required"}}})
		return
	}
	st0 := s.st.State()
	all := append([]string{}, req.ScenarioIDs...)
	sort.Strings(all)
	if atomic {
		for _, id := range all {
			if _, ok := st0.Scenarios[id]; !ok {
				writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "SCENARIO_NOT_FOUND", InputID: id, Message: "scenario is missing"}}})
				return
			}
		}
		for _, id := range all {
			if res := s.eng.Evaluate(st0.Scenarios[id], st0); len(res.BlockingIssues) > 0 && !onlyExpired(res.BlockingIssues) {
				writeError(w, 400, &ValidationErrors{Issues: res.BlockingIssues})
				return
			}
		}
	}
	path := "/api/recalculations"
	eventType := "scenarios.recalculated"
	if atomic {
		path += "/atomic"
		eventType = "scenarios.recalculated.atomic"
	}
	body, code, err := s.mutate(r, eventType, path, map[string]any{"scenarioIds": all, "atomic": atomic}, func(st *domain.State) ([]byte, error) {
		updated := map[string]domain.Result{}
		failed := map[string][]domain.Issue{}
		for _, id := range all {
			sc, ok := st.Scenarios[id]
			if !ok {
				failed[id] = []domain.Issue{{Code: "SCENARIO_NOT_FOUND", InputID: id, Message: "scenario is missing"}}
				if atomic {
					return nil, &ConflictError{Code: "ATOMIC_RECALC_FAILED", Body: failed}
				}
				continue
			}
			res := s.eng.Evaluate(sc, st)
			if len(res.BlockingIssues) > 0 {
				failed[id] = res.BlockingIssues
				if atomic {
					return nil, &ConflictError{Code: "ATOMIC_RECALC_FAILED", Body: failed}
				}
				continue
			}
			sc.UpdatedAt = s.now().UTC()
			st.Scenarios[id] = sc
			st.Results[id] = res
			updated[id] = res
		}
		return jsonMust(map[string]any{"results": updated, "failed": failed, "atomic": atomic})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 200, raw(body))
}

func (s *Server) comparison(w http.ResponseWriter, r *http.Request) {
	ids := []string{}
	for _, part := range splitCSV(r.URL.Query().Get("ids")) {
		ids = append(ids, part)
	}
	if len(ids) == 0 {
		r.ParseForm()
		ids = splitCSV(r.Form.Get("ids"))
	}
	if len(ids) < 2 {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "IDS_REQUIRED", Path: "ids", Message: "at least two scenario ids are required"}}})
		return
	}
	st := s.st.State()
	out := []map[string]any{}
	for _, id := range ids {
		sc, ok := st.Scenarios[id]
		if !ok {
			writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "SCENARIO_NOT_FOUND", InputID: id, Message: "scenario is missing"}}})
			return
		}
		item := map[string]any{"scenario": sc, "activeResult": st.Results[id]}
		if sc.FrozenResultID != "" {
			item["frozen"] = st.FrozenResults[sc.FrozenResultID]
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"ruleSetVersion": domain.RuleSetVersion, "scenarios": out})
}

func (s *Server) certificateUsers(st *domain.State, certID string) []string {
	ids := []string{}
	for id, sc := range st.Scenarios {
		for _, n := range sc.Nodes {
			if n.Type == "calibration" && n.CertificateID == certID {
				ids = append(ids, id)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}
func affectedResults(st *domain.State, ids []string) map[string]domain.Result {
	out := map[string]domain.Result{}
	for _, id := range ids {
		out[id] = st.Results[id]
	}
	return out
}
func certFromReq(req CertificateReq, now time.Time) domain.Certificate {
	return domain.Certificate{ID: req.ID, Name: req.Name, Correction: req.Correction, Unit: req.Unit, StandardUncertainty: req.StandardUncertainty, DegreesOfFreedom: req.DegreesOfFreedom, ValidFrom: req.ValidFrom.UTC(), ExpiresAt: req.ExpiresAt.UTC(), CreatedAt: now.UTC()}
}
func fmtID(t time.Time) string { return t.UTC().Format("20060102T150405.000000000Z") }
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
func splitCSV(s string) []string {
	out := []string{}
	for _, x := range split(s, ",") {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
func split(s string, sep string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, sep)
}
