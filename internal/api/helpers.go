package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"metrologylab/internal/domain"
	"metrologylab/internal/store"
	"metrologylab/internal/units"
)

func (s *Server) mutate(r *http.Request, eventType, path string, payload map[string]any, fn func(*domain.State) ([]byte, error)) ([]byte, int, error) {
	body, ok := requestBody(r.Context())
	if !ok {
		body = []byte("{}")
	}
	sum := sha256.Sum256(append([]byte(r.Method+"\n"+path+"\n"), body...))
	return s.st.Mutate(r.Context(), r.Header.Get("X-Request-ID"), r.Method, path, eventType, payload, fn, hex.EncodeToString(sum[:]))
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "MALFORMED_JSON", Message: "request body cannot be read"}}})
		return false
	}
	setRequestBody(r.Context(), b)
	if err := json.Unmarshal(b, v); err != nil {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "MALFORMED_JSON", Message: err.Error()}}})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, `{"error":{"code":"INTERNAL_ERROR","message":"response encoding failed"}}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}
func jsonMust(v any) ([]byte, error) { return json.Marshal(v) }
func raw(b []byte) json.RawMessage   { return b }
func writeError(w http.ResponseWriter, status int, err error) {
	resp := ErrorResponse{Error: ErrorBody{Code: "INTERNAL_ERROR", Message: "internal server error"}}
	var ve *ValidationErrors
	var ce *ConflictError
	if errors.As(err, &ve) {
		resp.Error.Code = "INVALID_INPUT"
		resp.Error.Message = err.Error()
		resp.Error.Details = ve.Issues
		if status == 0 || status == 200 {
			status = 400
		}
	} else if errors.As(err, &ce) {
		resp.Error.Code = ce.Code
		resp.Error.Message = ce.Message
		status = 409
	} else if status == 0 || status == 200 {
		status = 500
	}
	writeJSON(w, status, resp)
	if ce != nil && ce.Body != nil {
		log.Printf("conflict body: %#v", ce.Body)
	}
}
func writeStoreError(w http.ResponseWriter, code int, err error, issues []domain.Issue) {
	if errors.Is(err, store.ErrConflict) {
		writeError(w, 409, err)
		return
	}
	if code >= 500 {
		writeError(w, 500, err)
		return
	}
	writeError(w, code, err)
}
func withRecovery(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				writeError(w, 500, fmt.Errorf("internal server error"))
			}
		}()
		h.ServeHTTP(w, r)
	})
}
func validateID(id string) []domain.Issue {
	if id == "" {
		return []domain.Issue{{Code: "ID_REQUIRED", Path: "id", Message: "id is required"}}
	}
	if strings.ContainsAny(id, "/\\\n") || id == "." || id == ".." {
		return []domain.Issue{{Code: "INVALID_ID", Path: "id", Message: "id contains unsafe characters"}}
	}
	return nil
}
func knownUnit(u string) bool { _, ok := units.Canonical(u); return ok }
func validDistribution(d string) bool {
	switch d {
	case "normal", "rectangular", "triangular", "t", "unknown":
		return true
	}
	return false
}
func onlyExpired(issues []domain.Issue) bool {
	if len(issues) == 0 {
		return false
	}
	for _, i := range issues {
		if i.Code != "CERTIFICATE_EXPIRED" {
			return false
		}
	}
	return true
}
func staticHandler(content []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	}
}
