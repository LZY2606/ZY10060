package httpapi

import (
	"io"
	"net/http"
	"strings"

	"metrolab/internal/service"
)

func (s *Server) listObs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"observations": s.svc.ListObservations()})
}

func (s *Server) putObs(w http.ResponseWriter, r *http.Request) {
	var in service.ObservationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	o, e := s.svc.PutObservation(in, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 201, map[string]any{"observation": o})
}

func (s *Server) listScn(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"scenarios": s.svc.ListScenarios()})
}

func (s *Server) createScn(w http.ResponseWriter, r *http.Request) {
	var in service.ScenarioInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sc, e := s.svc.CreateScenario(in, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 201, map[string]any{"scenario": sc})
}

func (s *Server) getScn(w http.ResponseWriter, r *http.Request) {
	sc, e := s.svc.GetScenario(r.PathValue("id"))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"scenario": sc})
}

func (s *Server) updateScn(w http.ResponseWriter, r *http.Request) {
	var in service.ScenarioInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sc, e := s.svc.UpdateScenario(r.PathValue("id"), in, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"scenario": sc})
}

type copyReq struct {
	Name   string               `json:"name"`
	Groups *[]service.CorrGroup `json:"groups,omitempty"`
	Pairs  *[]service.CorrPair  `json:"pairs,omitempty"`
}

func (s *Server) copyScn(w http.ResponseWriter, r *http.Request) {
	var in copyReq
	if !decodeJSON(w, r, &in) {
		return
	}
	sc, e := s.svc.CopyScenario(r.PathValue("id"), in.Name, in.Groups, in.Pairs, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 201, map[string]any{"scenario": sc})
}

func (s *Server) freezeScn(w http.ResponseWriter, r *http.Request) {
	sc, res, e := s.svc.FreezeScenario(r.PathValue("id"), requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"scenario": sc, "result": res})
}

type computeReq struct {
	Confirmed bool   `json:"confirmed"`
	AsOf      string `json:"as_of,omitempty"`
}

func (s *Server) computeScn(w http.ResponseWriter, r *http.Request) {
	in := computeReq{}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &in) {
			return
		}
	}
	rep, e := s.svc.Compute(r.PathValue("id"), requestID(r), in.Confirmed, in.AsOf)
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) listRes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"results": s.svc.ListResults(r.URL.Query().Get("scenario"))})
}

func (s *Server) getRes(w http.ResponseWriter, r *http.Request) {
	res, e := s.svc.GetResult(r.PathValue("id"))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"result": res})
}

func (s *Server) confirmRes(w http.ResponseWriter, r *http.Request) {
	res, e := s.svc.ConfirmPending(r.PathValue("id"), requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"result": res})
}

func (s *Server) diffRes(w http.ResponseWriter, r *http.Request) {
	d, e := s.svc.DiffResults(r.PathValue("a"), r.PathValue("b"))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) recomputeRes(w http.ResponseWriter, r *http.Request) {
	res, e := s.svc.RecomputeResult(r.PathValue("id"), requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"result": res})
}

type batchReq struct {
	ResultIDs []string `json:"result_ids"`
}

func (s *Server) recomputeBatch(w http.ResponseWriter, r *http.Request) {
	var in batchReq
	if !decodeJSON(w, r, &in) {
		return
	}
	res, e := s.svc.RecomputeBatch(in.ResultIDs, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"results": res, "count": len(res)})
}

func (s *Server) auditGet(w http.ResponseWriter, r *http.Request) {
	rd, e := s.svc.ExportAudit(r.PathValue("id"))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-`+r.PathValue("id")+`.tar"`)
	w.WriteHeader(200)
	_, _ = io.Copy(w, rd)
}

func (s *Server) auditImport(w http.ResponseWriter, r *http.Request) {
	storeResult := r.URL.Query().Get("store") == "true"
	rep, e := s.svc.ImportAudit(r.Body, storeResult, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) listCerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"certificates": s.svc.ListCertificates()})
}

func (s *Server) putCert(w http.ResponseWriter, r *http.Request) {
	var in service.CertificateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, e := s.svc.PutCertificate(in, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 201, map[string]any{"certificate": c})
}

func (s *Server) replaceCert(w http.ResponseWriter, r *http.Request) {
	var in service.CertificateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, affected, e := s.svc.ReplaceCertificate(r.PathValue("id"), in, requestID(r))
	if e != nil {
		writeSvcError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"certificate": c, "affected": affected})
}

func (s *Server) affectedCert(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"affected": s.svc.AffectedByCertificate(r.PathValue("id"))})
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	writeJSON(w, 200, map[string]any{"events": s.svc.ListEvents(limit)})
}

var _ = strings.TrimSpace
