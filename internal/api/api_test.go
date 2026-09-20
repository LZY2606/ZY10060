package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"metrologylab/internal/api"
	"metrologylab/internal/store"
)

type H struct {
	*testing.T
	client *http.Client
	url    string
}

func newH(t *testing.T, now time.Time) *H {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(api.New(st, func() time.Time { return now }))
	t.Cleanup(ts.Close)
	return &H{T: t, client: ts.Client(), url: ts.URL}
}
func (h *H) post(path string, reqId string, v any) (map[string]any, []byte, int) {
	b, _ := json.Marshal(v)
	r, err := http.NewRequest("POST", h.url+path, bytes.NewReader(b))
	if err != nil {
		h.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	if reqId != "" {
		r.Header.Set("X-Request-ID", reqId)
	}
	resp, err := h.client.Do(r)
	if err != nil {
		h.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.Fatal(err)
	}
	out := map[string]any{}
	json.Unmarshal(body, &out)
	return out, body, resp.StatusCode
}
func (h *H) mustPost(path, reqId string, v any) map[string]any {
	out, _, code := h.post(path, reqId, v)
	if code > 299 {
		h.Fatalf("POST %s %d %s", path, code, v)
	}
	return out
}
func jget(m map[string]any, path ...string) map[string]any {
	cur := m
	for _, p := range path {
		var ok bool
		cur, ok = cur[p].(map[string]any)
		if !ok {
			return map[string]any{}
		}
	}
	return cur
}

func TestCalculationValidationAndIdempotency(t *testing.T) {
	h := newH(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h.mustPost("/api/groups", "g1", map[string]any{"id": "grp", "coefficient": .5})
	for _, id := range []string{"a", "b"} {
		h.mustPost("/api/observations", id, map[string]any{"id": id, "name": id, "value": 10.02, "unit": "mm", "standardUncertainty": .05, "distribution": "normal", "degreesOfFreedom": 9, "correlationGroupId": "grp"})
	}
	sc := map[string]any{"id": "s1", "name": "diff", "outputNodeId": "out", "nodes": []map[string]any{{"id": "na", "type": "observation", "observationId": "a"}, {"id": "nb", "type": "observation", "observationId": "b"}, {"id": "out", "type": "difference", "leftId": "na", "rightId": "nb"}}}
	r := h.mustPost("/api/scenarios", "s1", sc)
	res := jget(r, "result")
	if res["status"] != "ready" {
		t.Fatalf("%#v", res)
	}
	bad := map[string]any{"id": "bad", "outputNodeId": "out", "nodes": []map[string]any{{"id": "n", "type": "observation", "observationId": "a"}, {"id": "out", "type": "conversion", "inputId": "n", "toUnit": "kg"}}}
	_, _, code := h.post("/api/scenarios", "bad", bad)
	if code != 400 {
		t.Fatalf("code=%d", code)
	}
	replay := h.mustPost("/api/scenarios", "s1", sc)
	if jget(replay, "result")["status"] != "ready" {
		t.Fatal("replay failed")
	}
	changed := map[string]any{}
	for k, v := range sc {
		changed[k] = v
	}
	changed["name"] = "different"
	_, _, code = h.post("/api/scenarios", "s1", changed)
	if code != 409 {
		t.Fatalf("expected conflict got %d", code)
	}
}

func TestExpiredCertificateReplacementAtomicAndFreeze(t *testing.T) {
	current := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := newH(t, current)
	h.mustPost("/api/observations", "oa", map[string]any{"id": "a", "value": 10, "unit": "mm", "standardUncertainty": .05, "distribution": "normal", "degreesOfFreedom": 12})
	cert := map[string]any{"id": "c1", "name": "old", "correction": .01, "unit": "mm", "standardUncertainty": .02, "degreesOfFreedom": 20, "validFrom": current.AddDate(-1, 0, 0).Format(time.RFC3339), "expiresAt": current.Add(-time.Hour).Format(time.RFC3339)}
	h.mustPost("/api/certificates", "c1", cert)
	sc := map[string]any{"id": "s1", "outputNodeId": "out", "nodes": []map[string]any{{"id": "in", "type": "observation", "observationId": "a"}, {"id": "out", "type": "calibration", "inputId": "in", "certificateId": "c1"}}}
	r := h.mustPost("/api/scenarios", "s1", sc)
	if jget(r, "result")["status"] != "waiting_confirmation" {
		t.Fatalf("%#v", r)
	}
	newCert := map[string]any{}
	for k, v := range cert {
		newCert[k] = v
	}
	newCert["id"] = "c2"
	newCert["expiresAt"] = current.AddDate(1, 0, 0).Format(time.RFC3339)
	newCert["correction"] = .02
	rep := h.mustPost("/api/certificates/c1/replace", "rep", map[string]any{"newId": "c2", "newCertificate": newCert})
	ids := rep["affectedScenarioIds"].([]any)
	if len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("affected=%#v", ids)
	}
	atomic := h.mustPost("/api/recalculations/atomic", "atom", map[string]any{"scenarioIds": []string{"s1"}})
	if jget(atomic, "results", "s1")["status"] != "ready" {
		t.Fatalf("%#v", atomic)
	}
	f := h.mustPost("/api/scenarios/s1/freeze", "freeze", map[string]any{})
	if f["frozen"] == nil {
		t.Fatal("freeze missing")
	}
}

func TestAuditExportImportHashAndFrozenReproduction(t *testing.T) {
	current := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := newH(t, current)
	h.mustPost("/api/observations", "oa", map[string]any{"id": "a", "value": 10, "unit": "mm", "standardUncertainty": .05, "distribution": "normal", "degreesOfFreedom": 12})
	cert := map[string]any{"id": "c1", "name": "cert", "correction": .01, "unit": "mm", "standardUncertainty": .02, "degreesOfFreedom": 20, "validFrom": current.AddDate(0, -1, 0).Format(time.RFC3339), "expiresAt": current.AddDate(0, 1, 0).Format(time.RFC3339)}
	h.mustPost("/api/certificates", "oc", cert)
	sc := map[string]any{"id": "s1", "outputNodeId": "out", "nodes": []map[string]any{{"id": "in", "type": "observation", "observationId": "a"}, {"id": "conv", "type": "conversion", "inputId": "in", "toUnit": "m"}, {"id": "out", "type": "calibration", "inputId": "conv", "certificateId": "c1"}}}
	original := h.mustPost("/api/scenarios", "os", sc)
	h.mustPost("/api/scenarios/s1/freeze", "of", map[string]any{})
	out, _, code := h.post("/api/audits/export", "oe", map[string]any{"scenarioIds": []string{"s1"}})
	if code != 200 {
		t.Fatal("export failed")
	}
	fresh := newH(t, current)
	imp := fresh.mustPost("/api/audits/import", "oi", out)
	if imp["imported"] != true {
		t.Fatalf("import response %#v", imp)
	}
	if imp["checksum"] != out["checksum"] {
		t.Fatal("imported checksum changed")
	}
	stateResp, err := fresh.client.Get(fresh.url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stateResp.Body.Close()
	stateBytes, _ := io.ReadAll(stateResp.Body)
	var restoredState map[string]any
	json.Unmarshal(stateBytes, &restoredState)
	restored := jget(restoredState, "activeResults", "s1")
	if restored["value"] != jget(original, "result")["value"] || restored["upperBound"] != jget(original, "result")["upperBound"] {
		t.Fatalf("numeric result changed: %v vs %v", restored, jget(original, "result"))
	}
	if len(restoredState["frozenResults"].(map[string]any)) != 1 {
		t.Fatal("frozen result was not imported")
	}
}
func mapsKeys(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
