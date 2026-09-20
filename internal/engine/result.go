package engine

import (
	"math"
	"sort"
)

func (e *eval) variance(c []float64) float64 {
	v := 0.0
	for i := 0; i < e.n; i++ {
		for j := 0; j < e.n; j++ {
			v += c[i] * e.cov.at(i, j) * c[j]
		}
	}
	return v
}

func (e *eval) sourceNames() []string {
	names := make([]string, 0, e.n)
	names = append(names, e.leafIDs...)
	for _, cid := range e.certOrder {
		names = append(names, "cert:"+cid+":slope", "cert:"+cid+":intercept")
	}
	return names
}

func (e *eval) nuOf(row int) float64 {
	if row < len(e.leafIDs) {
		l := e.spec.Leaves[row]
		if l.Distribution == DistT && l.Nu != nil {
			return *l.Nu
		}
		return math.Inf(1) // type-B bounded shapes: infinite nu per GUM
	}
	k := row - len(e.leafIDs)
	return e.certByID[e.certOrder[k/2]].Nu
}

func (e *eval) stdAt(row int) float64 { return math.Sqrt(math.Max(0, e.cov.at(row, row))) }

func (e *eval) contributionsAndNu(c []float64) ([]Contribution, []SensitivityItem, float64, float64) {
	names := e.sourceNames()
	variance := e.variance(c)
	var contrib []Contribution
	sum4, num := 0.0, 0.0
	for i := 0; i < e.n; i++ {
		if c[i] == 0 {
			continue
		}
		ui := e.stdAt(i)
		self := c[i] * c[i] * e.cov.at(i, i)
		share := 0.0
		if variance > 0 {
			share = self / variance
		}
		contrib = append(contrib, Contribution{Source: names[i], Kind: "self", Sensitivity: c[i], StdUncertainty: ui, VarianceTerm: self, Share: share})
		nu := e.nuOf(i)
		sum4 += self * self
		if math.IsInf(nu, 1) {
			num += 0
		} else {
			num += self * self / nu
		}
	}
	for i := 0; i < e.n; i++ {
		if c[i] == 0 {
			continue
		}
		for j := i + 1; j < e.n; j++ {
			if c[j] == 0 || e.cov.at(i, j) == 0 {
				continue
			}
			term := 2 * c[i] * c[j] * e.cov.at(i, j)
			if math.Abs(term) < 1e-300 {
				continue
			}
			share := 0.0
			if variance > 0 {
				share = term / variance
			}
			contrib = append(contrib, Contribution{Source: names[i], Kind: "pair", With: names[j], Sensitivity: c[i], VarianceTerm: term, Share: share})
		}
	}
	sort.SliceStable(contrib, func(a, b int) bool { return math.Abs(contrib[a].Share) > math.Abs(contrib[b].Share) })

	var sens []SensitivityItem
	for i := 0; i < e.n; i++ {
		if c[i] != 0 {
			sens = append(sens, SensitivityItem{Source: names[i], Sensitivity: c[i]})
		}
	}
	sort.SliceStable(sens, func(a, b int) bool { return math.Abs(sens[a].Sensitivity) > math.Abs(sens[b].Sensitivity) })

	nuEff := math.Inf(1)
	if num > 0 {
		nuEff = sum4 / num
	}
	return contrib, sens, variance, nuEff
}

func displayBack(cp ConversionPath, base float64) float64 { return (base - cp.Intercept) / cp.Slope }

func (e *eval) resultFor(n NodeSpec, p float64) NodeResult {
	cp := e.paths["node:"+n.ID]
	c := e.coeffs[n.ID]
	contrib, sens, variance, nuEff := e.contributionsAndNu(c)
	uBase := math.Sqrt(math.Max(0, variance))
	val := e.values[n.ID]
	nr := NodeResult{
		ID: n.ID, Kind: n.Kind, Value: val, DisplayValue: displayBack(cp, val),
		Unit: n.Unit, BaseUnit: dimName(cp.Dim),
		StdUncertainty: uBase, DisplayU: uBase / cp.Slope,
		NuEff: nuEff, CoverageP: p,
		Contributions: contrib, Sensitivities: sens,
		Formula: formulaFor(n.Kind),
	}
	k := StudentsTQuantile(p, nuEff)
	nr.K = k
	half := k * uBase / cp.Slope
	nr.IntervalLow = nr.DisplayValue - half
	nr.IntervalHigh = nr.DisplayValue + half
	return nr
}

func (e *eval) resultForLeaf(l LeafSpec, p float64) NodeResult {
	cp := e.paths[l.ID]
	uBase := l.StdUncertainty * cp.Slope
	nu := math.Inf(1)
	if l.Distribution == DistT && l.Nu != nil {
		nu = *l.Nu
	}
	nr := NodeResult{
		ID: l.ID, Kind: "leaf", Value: cp.apply(l.Value), DisplayValue: displayBack(cp, cp.apply(l.Value)),
		Unit: l.Unit, BaseUnit: dimName(cp.Dim),
		StdUncertainty: uBase, DisplayU: uBase / cp.Slope,
		NuEff: nu, CoverageP: p, Formula: VersionGUM,
	}
	nr.K = StudentsTQuantile(p, nu)
	half := nr.K * uBase / cp.Slope
	nr.IntervalLow = nr.DisplayValue - half
	nr.IntervalHigh = nr.DisplayValue + half
	return nr
}

// StatusPending marks a node result as awaiting explicit user confirmation
// because it depends on an expired/revoked calibration certificate.
func (nr *NodeResult) StatusPending() {
	nr.K = 0
	nr.IntervalLow = math.NaN()
	nr.IntervalHigh = math.NaN()
}

func formulaFor(kind string) string {
	switch kind {
	case NodeConvert:
		return VersionConvert
	case NodeWeighted:
		return VersionWeighted
	case NodeDiff:
		return VersionDiff
	case NodeCalibration:
		return VersionCalibration
	}
	return VersionGUM
}

func (e *eval) nodeUsesCert(n NodeSpec, certID string) bool {
	if n.Kind == NodeCalibration && n.CertificateID == certID {
		return true
	}
	// transitive: any upstream calibration node
	direct := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if direct[id] {
			return false
		}
		direct[id] = true
		for _, ed := range e.spec.Edges {
			if ed.To == id {
				nn := nodeByID(e.spec, ed.From)
				if nn.ID == ed.From && nn.Kind == NodeCalibration && nn.CertificateID == certID {
					return true
				}
				if walk(ed.From) {
					return true
				}
			}
		}
		return false
	}
	return walk(n.ID)
}

func (e *eval) collectUnitPaths() []UnitPath {
	var out []UnitPath
	add := func(subject string, cp ConversionPath) {
		out = append(out, UnitPath{Subject: subject, From: cp.From, To: cp.To, Steps: cp.Steps, Slope: cp.Slope})
	}
	for _, l := range e.spec.Leaves {
		add("leaf:"+l.ID, e.paths[l.ID])
	}
	for _, n := range e.spec.Nodes {
		add("node:"+n.ID, e.paths["node:"+n.ID])
	}
	for _, ed := range e.spec.Edges {
		if cp, ok := e.paths[ed.From]; ok {
			add("edge:"+ed.From+"->"+ed.To, cp)
		} else if cp, ok := e.paths["node:"+ed.From]; ok {
			add("edge:"+ed.From+"->"+ed.To, cp)
		}
	}
	return out
}

func (e *eval) covAsMatrix() [][]float64 {
	out := make([][]float64, e.n)
	for i := range out {
		out[i] = make([]float64, e.n)
		for j := range out[i] {
			out[i][j] = e.cov.at(i, j)
		}
	}
	return out
}

func (e *eval) inputChecksum() string {
	b, _ := MarshalCanonical(specForHash(e.spec))
	return checksumBytes(b)
}
