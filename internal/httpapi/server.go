// Package httpapi exposes the service over a small JSON HTTP API. The browser
// is a view only: every numeric judgment is enforced server side.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"metrolab/internal/service"
	"net/http"
)

type Server struct {
	svc    *service.Service
	static http.Handler
}

func New(svc *service.Service, static http.Handler) *Server {
	return &Server{svc: svc, static: static}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/observations", s.listObs)
	mux.HandleFunc("POST /api/observations", s.putObs)
	mux.HandleFunc("GET /api/scenarios", s.listScn)
	mux.HandleFunc("POST /api/scenarios", s.createScn)
	mux.HandleFunc("GET /api/scenarios/{id}", s.getScn)
	mux.HandleFunc("PUT /api/scenarios/{id}", s.updateScn)
	mux.HandleFunc("POST /api/scenarios/{id}/copy", s.copyScn)
	mux.HandleFunc("POST /api/scenarios/{id}/freeze", s.freezeScn)
	mux.HandleFunc("POST /api/scenarios/{id}/compute", s.computeScn)
	mux.HandleFunc("GET /api/results", s.listRes)
	mux.HandleFunc("GET /api/results/{id}", s.getRes)
	mux.HandleFunc("POST /api/results/{id}/confirm", s.confirmRes)
	mux.HandleFunc("POST /api/results/{a}/diff/{b}", s.diffRes)
	mux.HandleFunc("POST /api/results/{id}/recompute", s.recomputeRes)
	mux.HandleFunc("POST /api/results/recompute-batch", s.recomputeBatch)
	mux.HandleFunc("GET /api/results/{id}/audit", s.auditGet)
	mux.HandleFunc("POST /api/audit/import", s.auditImport)
	mux.HandleFunc("GET /api/certificates", s.listCerts)
	mux.HandleFunc("POST /api/certificates", s.putCert)
	mux.HandleFunc("POST /api/certificates/{id}/replace", s.replaceCert)
	mux.HandleFunc("GET /api/certificates/{id}/affected", s.affectedCert)
	mux.HandleFunc("GET /api/events", s.listEvents)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// requestID extracts the idempotency key from header or JSON body.
func requestID(r *http.Request) string {
	if v := r.Header.Get("X-Request-Id"); v != "" {
		return v
	}
	return r.URL.Query().Get("request_id")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

type errBody struct {
	Error    errBodyInner     `json:"error"`
	Problems []serviceProblem `json:"problems,omitempty"`
}
type errBodyInner struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type serviceProblem struct {
	Code   string `json:"code"`
	Target string `json:"target"`
	Edge   string `json:"edge,omitempty"`
	Detail string `json:"detail"`
}

func writeSvcError(w http.ResponseWriter, e *service.Error) {
	code := http.StatusInternalServerError
	switch e.Kind {
	case service.KindInvalid:
		code = http.StatusBadRequest
	case service.KindConflict:
		code = http.StatusConflict
	}
	body := errBody{Error: errBodyInner{Kind: string(e.Kind), Code: e.Code, Message: e.Message}}
	for _, p := range e.Problems {
		body.Problems = append(body.Problems, serviceProblem{Code: p.Code, Target: p.Target, Edge: p.Edge, Detail: p.Detail})
	}
	writeJSON(w, code, body)
}

// decodeJSON limits request size and reports format errors distinctly.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody{Error: errBodyInner{Kind: "invalid_input", Code: "bad_body", Message: err.Error()}})
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody{Error: errBodyInner{Kind: "invalid_input", Code: "bad_json", Message: "request body is not valid JSON: " + err.Error()}})
		return false
	}
	if dec.More() {
		writeJSON(w, http.StatusBadRequest, errBody{Error: errBodyInner{Kind: "invalid_input", Code: "bad_json", Message: "request body contains multiple documents"}})
		return false
	}
	return true
}

// fail500 is reserved for unexpected internal failures.
func fail500(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, errBody{Error: errBodyInner{Kind: "internal", Code: "internal", Message: "internal failure"}})
}

var _ = errors.New
