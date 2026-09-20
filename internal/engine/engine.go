package engine

import (
	"math"
	"time"

	"metrologylab/internal/domain"
	"metrologylab/internal/units"
)

const (
	FormulaSource      = "source-input/v1"
	FormulaConvert     = "linear-unit-conversion/v1"
	FormulaWeighted    = "inverse-variance-weighted-mean/v1"
	FormulaDifference  = "linear-difference/v1"
	FormulaCalibration = "additive-calibration/v1"
	FormulaUncertainty = "gum-first-order/v1"
	FormulaCoverage    = "student-t-coverage/v1"
	FormulaPSD         = "psd-cholesky/v1"
)

type Engine struct{ now func() time.Time }

func New(now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	return &Engine{now: now}
}

func (e *Engine) Evaluate(sc domain.Scenario, st *domain.State) domain.Result {
	r := domain.Result{ScenarioID: sc.ID, Status: "ready", ComputedAt: e.now().UTC(), RuleSetVersion: domain.RuleSetVersion, FormulaVersions: map[string]string{"uncertainty": FormulaUncertainty, "coverage": FormulaCoverage, "psd": FormulaPSD}, Nodes: map[string]domain.NodeResult{}, ConversionPaths: map[string][]string{}}
	if issues := validateStructure(sc, st); len(issues) > 0 {
		return pending(r, issues)
	}
	order, issues := topoOrder(sc)
	if len(issues) > 0 {
		return pending(r, issues)
	}
	if issues := validateCorrelations(sc, st); len(issues) > 0 {
		return pending(r, issues)
	}
	for _, n := range order {
		switch n.Type {
		case "observation":
			o := st.Observations[n.ObservationID]
			put(&r, n, o.Value, o.Unit, o.StandardUncertainty, o.DegreesOfFreedom, map[string]float64{o.ID: 1}, FormulaSource)
		case "conversion":
			in := r.Nodes[n.InputID]
			from := nonEmpty(n.FromUnit, in.Unit)
			to := n.ToUnit
			if !units.Compatible(from, to) {
				issues = append(issues, mismatch(n.InputID, n.ID, "conversion units are incompatible"))
				continue
			}
			v, path, err := units.Convert(in.Value, from, to)
			if err != nil {
				issues = append(issues, domain.Issue{Code: "UNIT_CONVERSION_FAILED", Message: err.Error(), NodeID: n.ID, Edge: n.InputID + "->" + n.ID})
				continue
			}
			df, _ := units.Canonical(from)
			dt, _ := units.Canonical(to)
			f := df.Scale / dt.Scale
			c := scale(in.Coefficients, f)
			su := math.Sqrt(variance(c, sc, st, "", nil))
			put(&r, n, v, to, su, welchOne(in.StandardUncertainty*f, in.DegreesOfFreedom), c, FormulaConvert)
			r.ConversionPaths[n.ID] = path
		case "weighted":
			ins := getIns(r, n.InputIDs)
			if !sameUnits(ins) {
				issues = append(issues, mismatch(n.ID, n.ID, "weighted inputs are incompatible"))
				continue
			}
			ws, wi := weights(n, ins)
			if wi != nil {
				issues = append(issues, wi...)
				continue
			}
			u := ins[0].Unit
			val := 0.0
			coef := map[string]float64{}
			quartSum := 0.0
			for i, in := range ins {
				cv, f, err := aligned(in, u)
				if err != nil {
					issues = append(issues, domain.Issue{Code: "UNIT_CONVERSION_FAILED", Message: err.Error(), NodeID: n.ID, Edge: in.ID + "->" + n.ID})
					continue
				}
				val += ws[i] * cv
				for s, x := range in.Coefficients {
					coef[s] += ws[i] * f * x
				}
				quartSum += math.Pow(ws[i]*in.StandardUncertainty*f, 4) / maxFloat(in.DegreesOfFreedom, 0)
			}
			vr := variance(coef, sc, st, "", nil)
			nu := 0.0
			if quartSum > 0 {
				nu = vr * vr / quartSum
			}
			put(&r, n, val, u, math.Sqrt(vr), nu, coef, FormulaWeighted)
		case "difference":
			l, rr := r.Nodes[n.LeftID], r.Nodes[n.RightID]
			if !units.Compatible(l.Unit, rr.Unit) {
				issues = append(issues, mismatch(n.RightID, n.ID, "difference units are incompatible"))
				continue
			}
			rv, f, err := aligned(rr, l.Unit)
			if err != nil {
				issues = append(issues, domain.Issue{Code: "UNIT_CONVERSION_FAILED", Message: err.Error(), NodeID: n.ID})
				continue
			}
			coef := map[string]float64{}
			for s, x := range l.Coefficients {
				coef[s] += x
			}
			for s, x := range rr.Coefficients {
				coef[s] -= f * x
			}
			vr := variance(coef, sc, st, "", nil)
			q := math.Pow(l.StandardUncertainty, 4)/maxFloat(l.DegreesOfFreedom, 0) + math.Pow(rr.StandardUncertainty*f, 4)/maxFloat(rr.DegreesOfFreedom, 0)
			put(&r, n, l.Value-rv, l.Unit, math.Sqrt(vr), vr*vr/maxFloat(q, 0), coef, FormulaDifference)
		case "calibration":
			in := r.Nodes[n.InputID]
			cert, ok := resolveCertificate(st, n.CertificateID)
			if !ok {
				issues = append(issues, domain.Issue{Code: "CERTIFICATE_NOT_FOUND", NodeID: n.ID, Path: "certificateId", Message: "certificate does not exist"})
				continue
			}
			now := e.now().UTC()
			if cert.ReplacedBy != "" || now.Before(cert.ValidFrom.UTC()) || !now.Before(cert.ExpiresAt.UTC()) {
				issues = append(issues, domain.Issue{Code: "CERTIFICATE_EXPIRED", NodeID: n.ID, Path: "certificateId", Message: "certificate is not currently valid"})
			}
			if !units.Compatible(in.Unit, cert.Unit) {
				issues = append(issues, mismatch(n.InputID, n.ID, "certificate unit is incompatible"))
				continue
			}
			cv, f, err := aligned(domain.NodeResult{Value: cert.Correction, Unit: cert.Unit}, in.Unit)
			if err != nil {
				issues = append(issues, domain.Issue{Code: "UNIT_CONVERSION_FAILED", NodeID: n.ID, Message: err.Error()})
				continue
			}
			coef := clone(in.Coefficients)
			key := "certificate:" + cert.ID
			n.CertificateID = cert.ID
			coef[key] = f
			vr := variance(coef, sc, st, key, &cert)
			q := math.Pow(in.StandardUncertainty, 4)/maxFloat(in.DegreesOfFreedom, 0) + math.Pow(cert.StandardUncertainty*f, 4)/maxFloat(cert.DegreesOfFreedom, 0)
			put(&r, n, in.Value+cv, in.Unit, math.Sqrt(vr), vr*vr/maxFloat(q, 0), coef, FormulaCalibration)
		default:
			issues = append(issues, domain.Issue{Code: "UNKNOWN_NODE_TYPE", NodeID: n.ID, Path: "type", Message: "unsupported node type"})
		}
	}
	if len(issues) > 0 {
		return pending(r, issues)
	}
	out, ok := r.Nodes[sc.OutputNode]
	if !ok {
		return pending(r, []domain.Issue{{Code: "OUTPUT_NOT_COMPUTED", NodeID: sc.OutputNode, Path: "outputNodeId", Message: "output node was not computed"}})
	}
	fillContributions(&r, sc, st)
	r.Value = out.Value
	r.Unit = out.Unit
	r.StandardUncertainty = out.StandardUncertainty
	r.DegreesOfFreedom = out.DegreesOfFreedom
	r.CoverageFactor = tQuantile975(out.DegreesOfFreedom)
	r.LowerBound = out.Value - r.CoverageFactor*out.StandardUncertainty
	r.UpperBound = out.Value + r.CoverageFactor*out.StandardUncertainty
	r.FormulaVersions["outputNode"] = out.FormulaVersion
	return r
}

func pending(r domain.Result, is []domain.Issue) domain.Result {
	r.Status = "waiting_confirmation"
	r.BlockingIssues = is
	return r
}
func put(r *domain.Result, n domain.Node, v float64, u string, su, nu float64, c map[string]float64, fv string) {
	certID := ""
	if n.Type == "calibration" {
		certID = n.CertificateID
	}
	r.Nodes[n.ID] = domain.NodeResult{ID: n.ID, Type: n.Type, CertificateID: certID, Value: v, Unit: u, StandardUncertainty: su, DegreesOfFreedom: nu, Coefficients: c, FormulaVersion: fv}
}
func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func dim(u string) units.Dimension { d, _ := units.Canonical(u); return d.Dimension }
func mismatch(a, b, msg string) domain.Issue {
	return domain.Issue{Code: "UNIT_DIMENSION_MISMATCH", Message: msg, NodeID: b, Edge: a + "->" + b}
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func clone(c map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range c {
		out[k] = v
	}
	return out
}
func scale(c map[string]float64, f float64) map[string]float64 {
	out := clone(c)
	for k := range out {
		out[k] *= f
	}
	return out
}
func getIns(r domain.Result, ids []string) []domain.NodeResult {
	out := []domain.NodeResult{}
	for _, id := range ids {
		out = append(out, r.Nodes[id])
	}
	return out
}
func sameUnits(ins []domain.NodeResult) bool {
	if len(ins) == 0 {
		return false
	}
	for _, x := range ins {
		if !units.Compatible(ins[0].Unit, x.Unit) {
			return false
		}
	}
	return true
}
func aligned(in domain.NodeResult, to string) (float64, float64, error) {
	v, _, err := units.Convert(in.Value, in.Unit, to)
	if err != nil {
		return 0, 0, err
	}
	f, _, err := units.Convert(1, in.Unit, to)
	if err != nil {
		return 0, 0, err
	}
	return v, f, nil
}
func welchOne(u, nu float64) float64 { return nu }
func weights(n domain.Node, ins []domain.NodeResult) ([]float64, []domain.Issue) {
	ws := make([]float64, len(ins))
	if len(n.Weights) == len(ins) {
		s := 0.0
		for i, w := range n.Weights {
			if w < 0 {
				return nil, []domain.Issue{{Code: "INVALID_WEIGHTS", NodeID: n.ID, Message: "weights must not be negative"}}
			}
			ws[i] = w
			s += w
		}
		if s <= 0 {
			return nil, []domain.Issue{{Code: "INVALID_WEIGHTS", NodeID: n.ID, Message: "weight sum must be positive"}}
		}
		for i := range ws {
			ws[i] /= s
		}
		return ws, nil
	}
	denom := 0.0
	for _, in := range ins {
		if in.StandardUncertainty <= 0 {
			return nil, []domain.Issue{{Code: "INVALID_UNCERTAINTY", NodeID: n.ID, Message: "inverse variance weights require positive uncertainty"}}
		}
		denom += 1 / (in.StandardUncertainty * in.StandardUncertainty)
	}
	for i, in := range ins {
		ws[i] = (1 / (in.StandardUncertainty * in.StandardUncertainty)) / denom
	}
	return ws, nil
}
