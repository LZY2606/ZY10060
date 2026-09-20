package engine

import (
	"math"
	"testing"
)

func approxEq(t *testing.T, got, want, tol float64, msg string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.10g want %.10g", msg, got, want)
	}
}

func TestWeightedMeanCorrelated(t *testing.T) {
	nu := 5.0
	spec := Spec{
		Leaves: []LeafSpec{
			{ID: "x1", Value: 10, Unit: "g", StdUncertainty: 0.1, Distribution: DistT, Nu: &nu},
			{ID: "x2", Value: 10.5, Unit: "g", StdUncertainty: 0.2, Distribution: DistT, Nu: &nu},
		},
		Nodes:  []NodeSpec{{ID: "m", Kind: NodeWeighted, Unit: "mg", Weights: []float64{1, 1}}},
		Edges:  []EdgeSpec{{From: "x1", To: "m"}, {From: "x2", To: "m"}},
		Groups: []GroupSpec{{ID: "g", Correlation: 0.7}},
	}
	spec.Leaves[0].GroupID, spec.Leaves[1].GroupID = "g", "g"
	out, probs := Run(spec, false)
	if probs != nil {
		t.Fatal(probs)
	}
	r := out.Results["m"]
	approxEq(t, r.DisplayValue, 10250, 1e-9, "mean in mg")
	// u^2 in g: 0.25*(0.1^2+0.2^2+2*0.7*0.1*0.2)=0.0195, in mg 1000x
	approxEq(t, r.DisplayU, math.Sqrt(0.0195)*1000, 1e-9, "correlated u")
	// WS nu: numerator (sum of variance contributions)^2, denominators only
	// self terms carry stated nu; the correlated cross-term has no separate
	// type-A degrees of freedom.
	// GUM G.4: numerator sums self variance terms only (conservative when
	// inputs are correlated); both leaves have nu=5 so nu_eff stays 5.
	approxEq(t, r.NuEff, 5, 1e-6, "nu eff")
	if r.K < 1.9 || r.K > 3 {
		t.Fatalf("k out of range: %v", r.K)
	}
	// pair contribution present and shares sum to 1
	sum, pair := 0.0, false
	for _, c := range r.Contributions {
		sum += c.Share
		if c.Kind == "pair" {
			pair = true
		}
	}
	if !pair {
		t.Fatal("expected a pair contribution")
	}
	approxEq(t, sum, 1, 1e-9, "contribution shares")
}

func TestDiffTemperatureAffine(t *testing.T) {
	spec := Spec{
		Leaves: []LeafSpec{
			{ID: "a", Value: 20, Unit: "degC", StdUncertainty: 0.1, Distribution: DistRectangular},
			{ID: "b", Value: 293.15, Unit: "K", StdUncertainty: 0.1, Distribution: DistRectangular},
		},
		Nodes: []NodeSpec{{ID: "d", Kind: NodeDiff, Unit: "K"}},
		Edges: []EdgeSpec{{From: "a", To: "d", Role: "a"}, {From: "b", To: "d", Role: "b"}},
	}
	out, probs := Run(spec, false)
	if probs != nil {
		t.Fatal(probs)
	}
	r := out.Results["d"]
	approxEq(t, r.DisplayValue, 0, 1e-9, "20degC - 293.15K = 0 K")
	approxEq(t, r.DisplayU, math.Sqrt(2)*0.1, 1e-12, "combined u")
}

func TestDimensionMismatchLocated(t *testing.T) {
	spec := Spec{
		Leaves: []LeafSpec{
			{ID: "a", Value: 1, Unit: "kg", StdUncertainty: 0.1, Distribution: DistNormal},
			{ID: "b", Value: 2, Unit: "K", StdUncertainty: 0.1, Distribution: DistNormal},
		},
		Nodes: []NodeSpec{{ID: "d", Kind: NodeDiff, Unit: "K"}},
		Edges: []EdgeSpec{{From: "a", To: "d", Role: "a"}, {From: "b", To: "d", Role: "b"}},
	}
	_, probs := Run(spec, false)
	if probs == nil || probs[0].Code != "dimension_mismatch" || probs[0].Edge != "a->d" {
		t.Fatalf("want located dimension_mismatch on a->d, got %+v", probs)
	}
}

func TestCycleLocated(t *testing.T) {
	spec := Spec{
		Leaves: []LeafSpec{{ID: "x", Value: 1, Unit: "1", StdUncertainty: 0.1, Distribution: DistNormal}},
		Nodes: []NodeSpec{
			{ID: "p", Kind: NodeConvert, Unit: "1"},
			{ID: "q", Kind: NodeConvert, Unit: "1"},
		},
		Edges: []EdgeSpec{{From: "x", To: "p"}, {From: "p", To: "q"}, {From: "q", To: "p"}},
	}
	_, probs := Run(spec, false)
	found := false
	for _, p := range probs {
		if p.Code == "dependency_cycle" && p.Edge != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want cycle problem on an edge, got %+v", probs)
	}
}

func TestNonPSDRejected(t *testing.T) {
	leaves := []LeafSpec{
		{ID: "a1", Value: 1, Unit: "1", StdUncertainty: 1, Distribution: DistNormal, Nu: ptr(5.0)},
		{ID: "a2", Value: 1, Unit: "1", StdUncertainty: 1, Distribution: DistNormal, Nu: ptr(5.0)},
		{ID: "a3", Value: 1, Unit: "1", StdUncertainty: 1, Distribution: DistNormal, Nu: ptr(5.0)},
	}
	for i := range leaves {
		leaves[i].GroupID = "g"
	}
	spec := Spec{
		Leaves: leaves,
		Nodes:  []NodeSpec{{ID: "w", Kind: NodeWeighted, Unit: "1", Weights: []float64{1, 1, 1}}},
		Edges:  []EdgeSpec{{From: "a1", To: "w"}, {From: "a2", To: "w"}, {From: "a3", To: "w"}},
		Groups: []GroupSpec{{ID: "g", Correlation: -0.99}},
	}
	_, probs := Run(spec, false)
	if probs == nil || probs[0].Code != "correlation_not_psd" {
		t.Fatalf("want correlation_not_psd, got %+v", probs)
	}
}

func TestPSDPositiveDefiniteAccepted(t *testing.T) {
	m := newSym(2)
	m.set(0, 0, 1)
	m.set(1, 1, 1)
	m.set(0, 1, 0.9)
	if f := checkPSD(m, 0); f != nil {
		t.Fatalf("valid matrix flagged: %+v", f)
	}
}

func TestExpiredCertificatePending(t *testing.T) {
	spec := Spec{
		Leaves: []LeafSpec{
			{ID: "rd", Value: 25, Unit: "degC", StdUncertainty: 0.05, Distribution: DistNormal, Nu: ptr(9.0)},
			{ID: "x0", Value: 20, Unit: "degC", StdUncertainty: 0, Distribution: DistRectangular},
		},
		Nodes:        []NodeSpec{{ID: "cal", Kind: NodeCalibration, Unit: "K", ReferenceLeaf: "x0", CertificateID: "c1"}},
		Edges:        []EdgeSpec{{From: "rd", To: "cal", Role: "reading"}},
		Certificates: []CertInfo{{ID: "c1", Slope: 0.01, Intercept: 0.05, Nu: 20, ValidUntil: "2025-12-31T23:59:59Z"}},
		Now:          "2031-01-01T00:00:00Z",
	}
	out, probs := Run(spec, false)
	if probs != nil {
		t.Fatal(probs)
	}
	if out.Status != "pending" {
		t.Fatalf("want pending, got %s", out.Status)
	}
	cal := out.Results["cal"]
	if cal.K != 0 || len(cal.PendingExpiredCerts) != 1 {
		t.Fatalf("pending node must not carry coverage: %+v", cal)
	}
	// confirmed flag releases numbers
	out2, _ := Run(spec, true)
	if out2.Status != "ok" || out2.Results["cal"].K == 0 {
		t.Fatalf("confirmed run must produce coverage: %+v", out2.Status)
	}
	// frozen snapshot also reproduces numbers
	spec.Frozen = true
	out3, _ := Run(spec, false)
	if out3.Status != "ok" {
		t.Fatalf("frozen snapshot must remain reproducible: %s", out3.Status)
	}
}

func TestCalibrationUncertaintyPropagation(t *testing.T) {
	spec := Spec{
		Leaves: []LeafSpec{
			{ID: "rd", Value: 0.01, Unit: "K", StdUncertainty: 0, Distribution: DistRectangular},
			{ID: "x0", Value: 0, Unit: "K", StdUncertainty: 0, Distribution: DistRectangular},
		},
		Nodes:        []NodeSpec{{ID: "cal", Kind: NodeCalibration, Unit: "K", ReferenceLeaf: "x0", CertificateID: "c"}},
		Edges:        []EdgeSpec{{From: "rd", To: "cal", Role: "reading"}},
		Certificates: []CertInfo{{ID: "c", Slope: 0, Intercept: 0, SlopeU: 0.001, InterceptU: 0.02, Nu: 50, ValidUntil: "2040-01-01T00:00:00Z"}},
	}
	out, probs := Run(spec, false)
	if probs != nil {
		t.Fatal(probs)
	}
	r := out.Results["cal"]
	approxEq(t, r.DisplayValue, 0.01, 1e-12, "value")
	approxEq(t, r.DisplayU, math.Sqrt(0.001*0.001*0.01*0.01+0.02*0.02), 1e-10, "cert param u")
}

func TestTQuantile(t *testing.T) {
	approxEq(t, StudentsTQuantile(0.95, 1e9), 1.9599639845, 1e-6, "normal 95%")
	approxEq(t, StudentsTQuantile(0.95, 10), 2.22813885, 1e-6, "t10 95%")
	approxEq(t, StudentsTQuantile(0.95, 1), 12.7062047, 1e-5, "t1 95%")
}

func TestUnitConversionPaths(t *testing.T) {
	cp, err := conversionPath("mg")
	if err != nil {
		t.Fatal(err)
	}
	approxEq(t, cp.apply(1), 1e-6, 1e-18, "1mg in kg")
	if len(cp.Steps) == 0 {
		t.Fatal("path must document steps")
	}
	if _, err := conversionPath("bogus"); err == nil {
		t.Fatal("unknown unit must fail")
	}
	// m/s^2 compound
	cp2, err := conversionPath("m/s^2")
	if err != nil {
		t.Fatal(err)
	}
	if cp2.Dim != (Dim{0: 0, 1: 1, 2: -2}) {
		t.Fatalf("dim wrong: %v", cp2.Dim)
	}
}

func ptr[T any](v T) *T { return &v }
