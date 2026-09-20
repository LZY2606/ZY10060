package engine

import (
	"fmt"
	"math"
)

func (e *eval) coeff(id string) []float64 {
	if c, ok := e.coeffs[id]; ok {
		return c
	}
	c := make([]float64, e.n)
	e.coeffs[id] = c
	return c
}

func (e *eval) evalLeaf(l LeafSpec) {
	cp := e.paths[l.ID]
	e.values[l.ID] = cp.apply(l.Value)
	c := e.coeff(l.ID)
	c[e.rows[l.ID].index] = 1
	e.units[l.ID] = l.Unit
	e.kinds[l.ID] = "leaf"
}

func (e *eval) inEdges(nodeID string) []EdgeSpec {
	var out []EdgeSpec
	for _, ed := range e.spec.Edges {
		if ed.To == nodeID {
			out = append(out, ed)
		}
	}
	return out
}

func (e *eval) outPath(n NodeSpec) (ConversionPath, *Problem) {
	cp, err := conversionPath(n.Unit)
	if err != nil {
		return cp, &Problem{Code: "bad_unit", Target: n.ID, Detail: err.Error()}
	}
	e.paths["node:"+n.ID] = cp
	return cp, nil
}

func (e *eval) evalNode(n NodeSpec, badCerts map[string]bool) ([]EdgeResult, *Problem) {
	outCP, prob := e.outPath(n)
	if prob != nil {
		return nil, prob
	}
	e.units[n.ID] = n.Unit
	e.kinds[n.ID] = n.Kind
	edges := e.inEdges(n.ID)
	var er []EdgeResult

	edgeInfo := func(ed EdgeSpec) (float64, []float64, ConversionPath) {
		return e.values[ed.From], e.coeffs[ed.From], e.paths[ed.From]
	}
	checkDim := func(ed EdgeSpec) *Problem {
		inPath, ok := e.paths[ed.From]
		if !ok {
			inPath = e.paths["node:"+ed.From]
		}
		if inPath.Dim != outCP.Dim {
			return &Problem{Code: "dimension_mismatch", Target: n.ID, Edge: ed.From + "->" + ed.To,
				Detail: fmt.Sprintf("cannot connect %s (%s) to %s (%s)", ed.From, dimName(inPath.Dim), n.ID, dimName(outCP.Dim))}
		}
		return nil
	}

	c := e.coeff(n.ID)
	var value float64

	switch n.Kind {
	case NodeConvert:
		ed := edges[0]
		if p := checkDim(ed); p != nil {
			return nil, p
		}
		v, cin, inPath := edgeInfo(ed)
		value = v
		scaleInto := 1.0
		if !inPath.Linear {
			// values already in base units; affine cancellation leaves slope only.
		}
		for i := range c {
			c[i] += cin[i] * scaleInto
		}
		er = append(er, EdgeResult{From: ed.From, To: n.ID, Role: ed.Role, FromUnit: e.units[ed.From], ToInputUnit: n.Unit, DimMatch: true, Sensitivity: scaleInto})

	case NodeWeighted:
		wsum := 0.0
		for _, w := range n.Weights {
			wsum += w
		}
		if wsum == 0 {
			return nil, &Problem{Code: "bad_weights", Target: n.ID, Detail: "weights sum to zero"}
		}
		for i, ed := range edges {
			if p := checkDim(ed); p != nil {
				return nil, p
			}
			v, cin, _ := edgeInfo(ed)
			wi := n.Weights[i] / wsum
			value += wi * v
			for k := range c {
				c[k] += wi * cin[k]
			}
			er = append(er, EdgeResult{From: ed.From, To: n.ID, Role: ed.Role, FromUnit: e.units[ed.From], ToInputUnit: n.Unit, DimMatch: true, Sensitivity: wi})
		}

	case NodeDiff:
		var aPath, bPath ConversionPath
		av, bv := 0.0, 0.0
		var ca, cb []float64
		for _, ed := range edges {
			if p := checkDim(ed); p != nil {
				return nil, p
			}
			v, cin, inPath := edgeInfo(ed)
			switch ed.Role {
			case "a":
				av, ca, aPath = v, cin, inPath
			case "b":
				bv, cb, bPath = v, cin, inPath
			}
			er = append(er, EdgeResult{From: ed.From, To: n.ID, Role: ed.Role, FromUnit: e.units[ed.From], ToInputUnit: n.Unit, DimMatch: true,
				Sensitivity: map[bool]float64{true: 1, false: -1}[ed.Role == "a"]})
		}
		_ = aPath
		_ = bPath
		value = av - bv
		for k := range c {
			c[k] += ca[k] - cb[k]
		}

	case NodeCalibration:
		cert := e.certByID[n.CertificateID]
		var readingV float64
		var readingC []float64
		var readingPath ConversionPath
		var readingUnit string
		for _, ed := range edges {
			if ed.Role != "reading" {
				continue
			}
			if p := checkDim(ed); p != nil {
				return nil, p
			}
			readingV, readingC, readingPath = edgeInfo(ed)
			readingUnit = e.units[ed.From]
			er = append(er, EdgeResult{From: ed.From, To: n.ID, Role: "reading", FromUnit: e.units[ed.From], ToInputUnit: n.Unit, DimMatch: true, Sensitivity: 1 + cert.Slope})
		}
		refPath, err := conversionPath(e.spec.Leaves[e.leafIndex[n.ReferenceLeaf]].Unit)
		if err != nil {
			return nil, &Problem{Code: "bad_unit", Target: n.ReferenceLeaf, Detail: err.Error()}
		}
		if refPath.Dim != readingPath.Dim {
			return nil, &Problem{Code: "dimension_mismatch", Target: n.ID,
				Edge:   "reference:" + n.ReferenceLeaf,
				Detail: fmt.Sprintf("reference %s (%s) and reading (%s) dimensions differ", n.ReferenceLeaf, dimName(refPath.Dim), dimName(readingPath.Dim))}
		}
		x0 := refPath.apply(e.spec.Leaves[e.leafIndex[n.ReferenceLeaf]].Value)
		x0c := e.coeffs[n.ReferenceLeaf]
		is := e.rows[n.CertificateID+":slope"].index
		ii := e.rows[n.CertificateID+":intercept"].index
		d := readingV - x0
		value = readingV + cert.Intercept + cert.Slope*d
		for k := range c {
			c[k] += (1+cert.Slope)*readingC[k] - cert.Slope*x0c[k]
		}
		c[is] = d
		c[ii] = 1
		er = append(er, EdgeResult{From: n.ReferenceLeaf, To: n.ID, Role: "reference", FromUnit: e.units[n.ReferenceLeaf], ToInputUnit: n.Unit, DimMatch: true, Sensitivity: -cert.Slope})
		_ = readingUnit
	}

	e.values[n.ID] = value
	return er, nil
}

var _ = math.Sqrt
