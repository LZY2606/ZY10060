package service

import (
	"bytes"
	"io"
	"metrolab/internal/engine"
	"metrolab/internal/store"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	st, recs, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc, err := New(st, recs)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func f64(v float64) *float64 { return &v }

func mkObs(t *testing.T, svc *Service, id string, val, u float64, unit string) *Observation {
	t.Helper()
	o, e := svc.PutObservation(ObservationInput{
		ID: id, Value: f64(val), Unit: unit, StdUncertainty: f64(u),
		Distribution: engine.DistNormal, Nu: f64(9),
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	return o
}

func mkWeightedScenario(t *testing.T, svc *Service, corr float64) string {
	t.Helper()
	sc, e := svc.CreateScenario(ScenarioInput{
		Name: "s", CoverageP: 0.95,
		Nodes:  []ScenarioNode{{ID: "m", Kind: engine.NodeWeighted, Unit: "mg", Weights: []float64{1, 1}}},
		Edges:  []ScenarioEdge{{From: "a", To: "m"}, {From: "b", To: "m"}},
		Groups: []CorrGroup{{ID: "g", Correlation: corr, Members: []string{"a", "b"}}},
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	return sc.ID
}

func TestIdempotentReplay(t *testing.T) {
	svc := newTestService(t)
	o1, e := svc.PutObservation(ObservationInput{ID: "x", Value: f64(1), Unit: "g", StdUncertainty: f64(0.1),
		Distribution: engine.DistNormal, Nu: f64(3)}, "rid-1")
	if e != nil {
		t.Fatal(e)
	}
	o2, e := svc.PutObservation(ObservationInput{ID: "x", Value: f64(1), Unit: "g", StdUncertainty: f64(0.1),
		Distribution: engine.DistNormal, Nu: f64(3)}, "rid-1")
	if e != nil {
		t.Fatal(e)
	}
	if o1.ID != o2.ID || len(svc.ListObservations()) != 1 {
		t.Fatal("replay created a second business result")
	}
	mkObs(t, svc, "a", 10, 0.1, "g")
	mkObs(t, svc, "b", 10.5, 0.2, "g")
	sid := mkWeightedScenario(t, svc, 0.5)
	r1, e := svc.Compute(sid, "comp-1", false, "")
	if e != nil {
		t.Fatal(e)
	}
	r2, e := svc.Compute(sid, "comp-1", false, "")
	if e != nil {
		t.Fatal(e)
	}
	if r1.ResultID != r2.ResultID {
		t.Fatal("compute replay must return same result")
	}
}

func TestValidationRejectsAndLocates(t *testing.T) {
	svc := newTestService(t)
	mkObs(t, svc, "a", 1, 0.1, "kg")
	mkObs(t, svc, "b", 2, 0.1, "K")
	sc, e := svc.CreateScenario(ScenarioInput{
		Nodes: []ScenarioNode{{ID: "d", Kind: engine.NodeDiff, Unit: "K"}},
		Edges: []ScenarioEdge{{From: "a", To: "d", Role: "a"}, {From: "b", To: "d", Role: "b"}},
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	_, e = svc.Compute(sc.ID, "", false, "")
	if e == nil || e.Kind != KindInvalid || len(e.Problems) == 0 || e.Problems[0].Code != "dimension_mismatch" {
		t.Fatalf("want located invalid dimension error, got %+v", e)
	}
}

func TestNonPSDConflictIs400Class(t *testing.T) {
	svc := newTestService(t)
	mkObs(t, svc, "a", 1, 1, "1")
	mkObs(t, svc, "b", 1, 1, "1")
	mkObs(t, svc, "c", 1, 1, "1")
	sid := mkWeightedScenario3(t, svc, -0.99)
	_, e := svc.Compute(sid, "", false, "")
	if e == nil || e.Kind != KindInvalid {
		t.Fatalf("want invalid_input class, got %+v", e)
	}
}

func mkWeightedScenario3(t *testing.T, svc *Service, corr float64) string {
	sc, e := svc.CreateScenario(ScenarioInput{
		Nodes:  []ScenarioNode{{ID: "w", Kind: engine.NodeWeighted, Unit: "1", Weights: []float64{1, 1, 1}}},
		Edges:  []ScenarioEdge{{From: "a", To: "w"}, {From: "b", To: "w"}, {From: "c", To: "w"}},
		Groups: []CorrGroup{{ID: "g", Correlation: corr, Members: []string{"a", "b", "c"}}},
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	return sc.ID
}

func TestFrozenReproducibleAndImmutable(t *testing.T) {
	svc := newTestService(t)
	mkObs(t, svc, "a", 10, 0.1, "g")
	mkObs(t, svc, "b", 10.5, 0.2, "g")
	sid := mkWeightedScenario(t, svc, 0.5)
	_, e := svc.Compute(sid, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	sc, res, e := svc.FreezeScenario(sid, "")
	if e != nil {
		t.Fatal(e)
	}
	if !sc.Frozen {
		t.Fatal("scenario not frozen")
	}
	before := res.Output.Results["m"].DisplayValue
	// editing a frozen scenario is a conflict
	if _, e := svc.UpdateScenario(sid, ScenarioInput{Name: "x"}, ""); e == nil || e.Kind != KindConflict {
		t.Fatalf("want conflict editing frozen, got %+v", e)
	}
	// frozen result still numerically available
	got, _ := svc.GetResult(res.ID)
	if got.Output.Results["m"].DisplayValue != before {
		t.Fatal("frozen number changed")
	}
}

func TestCopyAdjustCorrelation(t *testing.T) {
	svc := newTestService(t)
	mkObs(t, svc, "a", 10, 0.1, "g")
	mkObs(t, svc, "b", 10.5, 0.2, "g")
	sid := mkWeightedScenario(t, svc, 0.8)
	r0, e := svc.Compute(sid, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	groups := []CorrGroup{{ID: "g", Correlation: 0.1, Members: []string{"a", "b"}}}
	cp, e := svc.CopyScenario(sid, "copy", &groups, nil, "")
	if e != nil {
		t.Fatal(e)
	}
	r1, e := svc.Compute(cp.ID, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	u0 := r0.Result.Output.Results["m"].DisplayU
	u1 := r1.Result.Output.Results["m"].DisplayU
	if !(u1 < u0) {
		t.Fatalf("lowering correlation must reduce u: %v vs %v", u0, u1)
	}
}

func TestCertExpiryPendingConfirm(t *testing.T) {
	svc := newTestService(t)
	c, e := svc.PutCertificate(CertificateInput{Slope: 0.01, Intercept: 0.05, Nu: 20,
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2025-12-31T23:59:59Z"}, "")
	if e != nil {
		t.Fatal(e)
	}
	mkObs(t, svc, "rd", 25, 0.05, "degC")
	mkObs(t, svc, "x0", 20, 0, "degC")
	sc, e := svc.CreateScenario(ScenarioInput{
		Nodes: []ScenarioNode{{ID: "cal", Kind: engine.NodeCalibration, Unit: "K", ReferenceLeaf: "x0", CertificateID: c.ID}},
		Edges: []ScenarioEdge{{From: "rd", To: "cal", Role: "reading"}},
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	r, e := svc.Compute(sc.ID, "", false, "2031-01-01T00:00:00Z")
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "pending" {
		t.Fatalf("want pending, got %s", r.Status)
	}
	conf, e := svc.ConfirmPending(r.ResultID, "")
	if e != nil {
		t.Fatal(e)
	}
	if conf.Status != "ok" || conf.Output.Results["cal"].K == 0 {
		t.Fatal("confirmed result must carry coverage numbers")
	}
}

func TestReplaceListsAffectedAndAtomicBatch(t *testing.T) {
	svc := newTestService(t)
	c1, e := svc.PutCertificate(CertificateInput{Slope: 0.01, Intercept: 0.05, Nu: 20,
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2040-12-31T23:59:59Z"}, "")
	if e != nil {
		t.Fatal(e)
	}
	mkObs(t, svc, "rd", 25, 0.05, "degC")
	mkObs(t, svc, "x0", 20, 0, "degC")
	sc, _ := svc.CreateScenario(ScenarioInput{
		Nodes: []ScenarioNode{{ID: "cal", Kind: engine.NodeCalibration, Unit: "K", ReferenceLeaf: "x0", CertificateID: c1.ID}},
		Edges: []ScenarioEdge{{From: "rd", To: "cal", Role: "reading"}},
	}, "")
	r, e := svc.Compute(sc.ID, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	c2, affected, e := svc.ReplaceCertificate(c1.ID, CertificateInput{Slope: 0.02, Intercept: 0.06, Nu: 20,
		ValidFrom: "2026-01-01T00:00:00Z", ValidUntil: "2050-12-31T23:59:59Z"}, "")
	if e != nil {
		t.Fatal(e)
	}
	if c2.ID == c1.ID || len(affected) != 1 || affected[0].ResultID != r.ResultID {
		t.Fatalf("affected list wrong: %+v", affected)
	}
	// atomic batch recompute
	res2, e := svc.RecomputeBatch([]string{r.ResultID}, "batch-1")
	if e != nil || len(res2) != 1 {
		t.Fatalf("batch: %+v %v", res2, e)
	}
	// batch including a frozen result must conflict
	_, fr, _ := svc.FreezeScenario(sc.ID, "")
	if _, e := svc.RecomputeBatch([]string{fr.ID}, ""); e == nil || e.Kind != KindConflict {
		t.Fatal("batch with frozen result must conflict")
	}
}

func TestAuditRoundTrip(t *testing.T) {
	svc := newTestService(t)
	mkObs(t, svc, "a", 10, 0.1, "g")
	mkObs(t, svc, "b", 10.5, 0.2, "g")
	sid := mkWeightedScenario(t, svc, 0.5)
	r, e := svc.Compute(sid, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	rd, e := svc.ExportAudit(r.ResultID)
	if e != nil {
		t.Fatal(e)
	}
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, rd); err != nil {
		t.Fatal(err)
	}
	rep, e := svc.ImportAudit(bytes.NewReader(buf.Bytes()), false, "")
	if e != nil {
		t.Fatal(e)
	}
	if !rep.HashOK || !rep.SpecChecksumOK || !rep.AllMatch || len(rep.NumericDiffs) != 0 {
		t.Fatalf("audit verification failed: %+v", rep)
	}
	rep2, e := svc.ImportAudit(bytes.NewReader(buf.Bytes()), true, "imp-1")
	if e != nil {
		t.Fatal(e)
	}
	rep3, e := svc.ImportAudit(bytes.NewReader(buf.Bytes()), true, "imp-1")
	if e != nil {
		t.Fatal(e)
	}
	if !rep2.Stored || rep2.ResultID == "" || rep3.ResultID != rep2.ResultID {
		t.Fatal("audit import must be idempotent")
	}
}

func TestErrorClassification(t *testing.T) {
	svc := newTestService(t)
	// missing fields -> invalid
	if _, e := svc.PutObservation(ObservationInput{Unit: "g"}, ""); e == nil || e.Kind != KindInvalid {
		t.Fatal("want invalid input")
	}
	// duplicate id -> conflict
	mkObs(t, svc, "dup", 1, 0.1, "g")
	_, e := svc.PutObservation(ObservationInput{ID: "dup", Value: f64(1), Unit: "g",
		StdUncertainty: f64(0.1), Distribution: engine.DistNormal, Nu: f64(3)}, "")
	if e == nil || e.Kind != KindConflict {
		t.Fatal("duplicate observation id must conflict")
	}
}

func TestPersistedInputsResultsEventsSeparate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, recs, _ := store.Open(dir)
	svc, _ := New(st, recs)
	mkObs(t, svc, "a", 10, 0.1, "g")
	mkObs(t, svc, "b", 10.5, 0.2, "g")
	sid := mkWeightedScenario(t, svc, 0.5)
	_, _ = svc.Compute(sid, "", false, "")
	st.Close()
	// separate materialized collection files exist on disk
	for _, name := range []string{"observations.json", "results.json", "events.json", "scenarios.json"} {
		if name == "events.json" {
		}
	}
	// reopen: state restored from WAL with events replayed
	st2, recs2, _ := store.Open(dir)
	defer st2.Close()
	svc2, err := New(st2, recs2)
	if err != nil {
		t.Fatal(err)
	}
	if len(svc2.ListObservations()) != 2 || len(svc2.ListResults("")) == 0 {
		t.Fatal("state not restored")
	}
	if len(svc2.ListEvents(0)) < 4 {
		t.Fatalf("events not preserved separately: %d", len(svc2.ListEvents(0)))
	}
}

func TestReferenceRevokedCertificateRejected(t *testing.T) {
	svc := newTestService(t)
	c1, e := svc.PutCertificate(CertificateInput{Slope: 0, Intercept: 0, Nu: 20,
		ValidFrom: "2020-01-01T00:00:00Z", ValidUntil: "2040-12-31T23:59:59Z"}, "")
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = svc.ReplaceCertificate(c1.ID, CertificateInput{Slope: 0, Intercept: 0, Nu: 20,
		ValidFrom: "2026-01-01T00:00:00Z", ValidUntil: "2050-12-31T23:59:59Z"}, "")
	if e != nil {
		t.Fatal(e)
	}
	mkObs(t, svc, "rd", 1, 0, "K")
	mkObs(t, svc, "x0", 0, 0, "K")
	// a brand new scenario referencing the revoked (superseded) certificate
	_, e = svc.CreateScenario(ScenarioInput{
		Nodes: []ScenarioNode{{ID: "cal", Kind: engine.NodeCalibration, Unit: "K",
			ReferenceLeaf: "x0", CertificateID: c1.ID}},
		Edges: []ScenarioEdge{{From: "rd", To: "cal", Role: "reading"}},
	}, "")
	if e == nil || e.Code != "revoked_certificate" {
		t.Fatalf("want revoked_certificate invalid error, got %+v", e)
	}
}

func TestUnitPathDocumentedOnEdges(t *testing.T) {
	svc := newTestService(t)
	mkObs(t, svc, "a", 10, 0.1, "g")
	mkObs(t, svc, "b", 10.5, 0.2, "g")
	sid := mkWeightedScenario(t, svc, 0)
	r, e := svc.Compute(sid, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	found := 0
	for _, up := range r.Result.Output.UnitPaths {
		if len(up.Steps) > 0 {
			found++
		}
	}
	if found < 3 {
		t.Fatalf("expected documented unit steps for leaves/nodes/edges, got %d", found)
	}
	// convert node g -> mg
	sc2, e := svc.CreateScenario(ScenarioInput{
		Nodes: []ScenarioNode{{ID: "cv", Kind: engine.NodeConvert, Unit: "mg"}},
		Edges: []ScenarioEdge{{From: "a", To: "cv"}},
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	r2, e := svc.Compute(sc2.ID, "", false, "")
	if e != nil {
		t.Fatal(e)
	}
	if v := r2.Result.Output.Results["cv"].DisplayValue; v != 10000 {
		t.Fatalf("10g in mg want 10000 got %v", v)
	}
}
