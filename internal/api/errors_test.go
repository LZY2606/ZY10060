package api_test

import (
	"testing"
	"time"
)

func TestUnitMismatchIs400WithEdge(t *testing.T) {
	h := newH(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h.mustPost("/api/observations", "a", map[string]any{"id": "a", "value": 1, "unit": "mm", "standardUncertainty": .1, "distribution": "normal", "degreesOfFreedom": 9})
	sc := map[string]any{"id": "bad", "outputNodeId": "c", "nodes": []map[string]any{{"id": "n", "type": "observation", "observationId": "a"}, {"id": "c", "type": "conversion", "inputId": "n", "toUnit": "kg"}}}
	out, _, code := h.post("/api/scenarios", "bad", sc)
	if code != 400 {
		t.Fatalf("code=%d", code)
	}
	details := out["error"].(map[string]any)["details"].([]any)
	first := details[0].(map[string]any)
	if first["code"] != "UNIT_DIMENSION_MISMATCH" || first["edge"] != "n->c" {
		t.Fatalf("%#v", first)
	}
}
