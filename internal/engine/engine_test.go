package engine_test

import (
	"math"
	"testing"
	"time"

	"metrologylab/internal/domain"
	"metrologylab/internal/engine"
)

func TestDifferenceUncertaintyAndContributions(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := domain.NewState()
	st.Observations["a"] = domain.Observation{ID: "a", Value: 10.02, Unit: "mm", StandardUncertainty: 0.05, DegreesOfFreedom: 9}
	st.Observations["b"] = domain.Observation{ID: "b", Value: 10.00, Unit: "mm", StandardUncertainty: 0.08, DegreesOfFreedom: 8}
	st.Groups["g"] = domain.CorrelationGroup{ID: "g", Coefficient: 0.5}
	a := st.Observations["a"]
	b := st.Observations["b"]
	a.CorrelationGroupID = "g"
	b.CorrelationGroupID = "g"
	st.Observations["a"] = a
	st.Observations["b"] = b
	sc := domain.Scenario{ID: "s", OutputNode: "out", Nodes: []domain.Node{{ID: "na", Type: "observation", ObservationID: "a"}, {ID: "nb", Type: "observation", ObservationID: "b"}, {ID: "out", Type: "difference", LeftID: "na", RightID: "nb"}}}
	r := engine.New(func() time.Time { return now }).Evaluate(sc, st)
	if r.Status != "ready" {
		t.Fatalf("status=%s issues=%#v", r.Status, r.BlockingIssues)
	}
	if math.Abs(r.Value-0.02) > 1e-12 {
		t.Fatalf("value=%v", r.Value)
	}
	wantU := math.Sqrt(.05*.05 + .08*.08 - 2*.5*.05*.08)
	if math.Abs(r.StandardUncertainty-wantU) > 1e-12 {
		t.Fatalf("u=%v want %v", r.StandardUncertainty, wantU)
	}
	if len(r.Contributions) != 2 || r.Contributions[0].Rank != 1 {
		t.Fatalf("contributions=%#v", r.Contributions)
	}
	if r.FormulaVersions["coverage"] != "student-t-coverage/v1" {
		t.Fatal("formula version missing")
	}
}

func TestUnitMismatchLocatedToEdge(t *testing.T) {
	st := domain.NewState()
	st.Observations["a"] = domain.Observation{ID: "a", Value: 1, Unit: "mm", StandardUncertainty: .1}
	sc := domain.Scenario{OutputNode: "c", Nodes: []domain.Node{{ID: "n", Type: "observation", ObservationID: "a"}, {ID: "c", Type: "conversion", InputID: "n", ToUnit: "kg"}}}
	r := engine.New(time.Now).Evaluate(sc, st)
	if r.Status != "waiting_confirmation" || len(r.BlockingIssues) != 1 || r.BlockingIssues[0].Code != "UNIT_DIMENSION_MISMATCH" || r.BlockingIssues[0].Edge != "n->c" {
		t.Fatalf("%#v", r)
	}
}

func TestCycleAndNonPSD(t *testing.T) {
	st := domain.NewState()
	st.Observations["a"] = domain.Observation{ID: "a", Value: 1, Unit: "mm", StandardUncertainty: .1}
	st.Groups["g"] = domain.CorrelationGroup{ID: "g", Coefficient: 1}
	st.Observations["b"] = domain.Observation{ID: "b", Value: 1, Unit: "mm", StandardUncertainty: .1, CorrelationGroupID: "g"}
	a2 := st.Observations["a"]
	a2.CorrelationGroupID = "g"
	st.Observations["a"] = a2
	cyc := domain.Scenario{OutputNode: "a", Nodes: []domain.Node{{ID: "a", Type: "difference", LeftID: "b", RightID: "b"}, {ID: "b", Type: "difference", LeftID: "a", RightID: "a"}}}
	if got := engine.New(time.Now).Evaluate(cyc, st); len(got.BlockingIssues) == 0 || got.BlockingIssues[0].Code != "DEPENDENCY_CYCLE" {
		t.Fatalf("%#v", got)
	}
	nsd := domain.Scenario{OutputNode: "d", Nodes: []domain.Node{{ID: "na", Type: "observation", ObservationID: "a"}, {ID: "nb", Type: "observation", ObservationID: "b"}, {ID: "d", Type: "difference", LeftID: "na", RightID: "nb"}}, Overrides: []domain.CorrelationOverride{{LeftID: "a", RightID: "b", Coefficient: 1.1}}}
	if got := engine.New(time.Now).Evaluate(nsd, st); len(got.BlockingIssues) == 0 || got.BlockingIssues[0].Code != "CORRELATION_NOT_PSD" {
		t.Fatalf("%#v", got)
	}
}
