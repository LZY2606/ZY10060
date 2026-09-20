package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"metrolab/internal/service"
	"metrolab/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func testServer(t *testing.T) (*httptest.Server, *service.Service) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	st, recs, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc, err := service.New(st, recs)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(svc, http.NotFoundHandler()).Routes())
	t.Cleanup(srv.Close)
	return srv, svc
}

func do(t *testing.T, method, url string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(b, &out)
	return res.StatusCode, out
}

func TestHTTPCodes(t *testing.T) {
	srv, _ := testServer(t)
	// malformed JSON -> 400 invalid_input
	code, body := do(t, "POST", srv.URL+"/api/observations", nil, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/api/observations", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != 400 {
		t.Fatalf("bad json want 400 got %d", res.StatusCode)
	}
	res.Body.Close()
	// valid create 201
	code, body = do(t, "POST", srv.URL+"/api/observations", map[string]any{
		"id": "a", "value": 1, "unit": "g", "std_uncertainty": 0.1,
		"distribution": "normal", "nu": 9}, map[string]string{"X-Request-Id": "r1"})
	if code != 201 {
		t.Fatalf("create want 201 got %d %v", code, body)
	}
	// replay same request id -> 201 same object, no duplicate
	code, body2 := do(t, "POST", srv.URL+"/api/observations", map[string]any{
		"id": "a", "value": 1, "unit": "g", "std_uncertainty": 0.1,
		"distribution": "normal", "nu": 9}, map[string]string{"X-Request-Id": "r1"})
	if code != 201 {
		t.Fatalf("replay want 201, got %d", code)
	}
	o1, _ := json.Marshal(body["observation"])
	o2, _ := json.Marshal(body2["observation"])
	if !bytes.Equal(o1, o2) {
		t.Fatal("idempotent replay returned different object")
	}
	// duplicate id -> 409 state conflict
	code, body = do(t, "POST", srv.URL+"/api/observations", map[string]any{
		"id": "a", "value": 2, "unit": "g", "std_uncertainty": 0.1,
		"distribution": "normal", "nu": 9}, nil)
	if code != 409 || body["error"].(map[string]any)["kind"] != "state_conflict" {
		t.Fatalf("dup want 409 conflict, got %d %v", code, body)
	}
	// missing field -> 400 invalid
	code, body = do(t, "POST", srv.URL+"/api/observations", map[string]any{"unit": "g"}, nil)
	if code != 400 || body["error"].(map[string]any)["kind"] != "invalid_input" {
		t.Fatalf("missing want 400 invalid, got %d", code)
	}
	// 404 for unknown result
	req, _ = http.NewRequest("GET", srv.URL+"/api/results/nope", nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 400 {
		t.Fatalf("unknown result currently mapped to 400 invalid, got %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestHTTPFullChainAndAudit(t *testing.T) {
	srv, _ := testServer(t)
	post := func(path string, body any, rid string) (int, map[string]any) {
		h := map[string]string{}
		if rid != "" {
			h["X-Request-Id"] = rid
		}
		return do(t, "POST", srv.URL+path, body, h)
	}
	for _, o := range []map[string]any{
		{"id": "a", "value": 10, "unit": "g", "std_uncertainty": 0.1, "distribution": "normal", "nu": 9},
		{"id": "b", "value": 10.5, "unit": "g", "std_uncertainty": 0.2, "distribution": "normal", "nu": 9},
	} {
		if code, b := post("/api/observations", o, ""); code != 201 {
			t.Fatalf("obs: %d %v", code, b)
		}
	}
	code, b := post("/api/scenarios", map[string]any{
		"name": "s", "coverage_p": 0.95,
		"nodes":  []map[string]any{{"id": "m", "kind": "weighted", "unit": "mg", "weights": []float64{1, 1}}},
		"edges":  []map[string]any{{"from": "a", "to": "m"}, {"from": "b", "to": "m"}},
		"groups": []map[string]any{{"id": "g", "correlation": 0.5, "members": []string{"a", "b"}}},
	}, "")
	if code != 201 {
		t.Fatal(b)
	}
	sid := b["scenario"].(map[string]any)["id"].(string)
	code, b = post("/api/scenarios/"+sid+"/compute", map[string]any{}, "c1")
	if code != 200 {
		t.Fatalf("compute %d %v", code, b)
	}
	if b["status"] != "ok" {
		t.Fatal("status not ok")
	}
	rid := b["result_id"].(string)
	// audit export is a tar
	req, _ := http.NewRequest("GET", srv.URL+"/api/results/"+rid+"/audit", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/x-tar" {
		t.Fatalf("audit export: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	tarBytes, _ := io.ReadAll(res.Body)
	res.Body.Close()
	// import verify
	req, _ = http.NewRequest("POST", srv.URL+"/api/audit/import?store=true", bytes.NewReader(tarBytes))
	req.Header.Set("X-Request-Id", "imp")
	res, _ = http.DefaultClient.Do(req)
	var rep map[string]any
	json.NewDecoder(res.Body).Decode(&rep)
	res.Body.Close()
	if res.StatusCode != 200 || rep["all_match"] != true {
		t.Fatalf("import verification failed: %d %v", res.StatusCode, rep)
	}
}
