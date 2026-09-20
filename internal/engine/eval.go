package engine

import (
	"fmt"
	"math"
)

type rowInfo struct {
	index int
	cert  string
	param string
}

type eval struct {
	spec      Spec
	leafIndex map[string]int
	leafIDs   []string
	certByID  map[string]CertInfo
	certOrder []string
	n         int

	cov    sym
	paths  map[string]ConversionPath
	rows   map[string]rowInfo
	values map[string]float64
	coeffs map[string][]float64
	units  map[string]string // declared unit per vertex
	kinds  map[string]string
	p      float64
}

func (e *eval) rowName(i int) string {
	if i < len(e.leafIDs) {
		return e.leafIDs[i]
	}
	k := i - len(e.leafIDs)
	c := e.certOrder[k/2]
	if k%2 == 0 {
		return "cert:" + c + ":slope"
	}
	return "cert:" + c + ":intercept"
}

func (e *eval) buildCovariance() (sym, *Problem) {
	m := newSym(e.n)
	scale := 0.0
	for i, l := range e.spec.Leaves {
		v := l.StdUncertainty
		cp, err := conversionPath(l.Unit)
		if err != nil {
			return m, &Problem{Code: "bad_unit", Target: l.ID, Detail: err.Error()}
		}
		vb := v * cp.Slope
		m.set(i, i, vb*vb)
		scale = math.Max(scale, vb*vb)
		e.paths[l.ID] = cp
		e.rows[l.ID] = rowInfo{index: i}
		e.units[l.ID] = l.Unit
		e.kinds[l.ID] = "leaf"
	}
	for _, c := range e.spec.Certificates {
		is := e.rows[c.ID+":slope"].index
		ii := e.rows[c.ID+":intercept"].index
		m.set(is, is, c.SlopeU*c.SlopeU)
		m.set(ii, ii, c.InterceptU*c.InterceptU)
		m.set(is, ii, c.SlopeInterceptR*c.SlopeU*c.InterceptU)
		scale = math.Max(scale, math.Max(c.SlopeU*c.SlopeU, c.InterceptU*c.InterceptU))
	}
	put := func(a, b string, r float64) bool {
		ia, oka := e.rows[a]
		ib, okb := e.rows[b]
		if !oka || !okb {
			return false
		}
		va := math.Sqrt(m.at(ia.index, ia.index))
		vb := math.Sqrt(m.at(ib.index, ib.index))
		m.set(ia.index, ib.index, r*va*vb)
		return true
	}
	groupOf := map[string]string{}
	for _, l := range e.spec.Leaves {
		if l.GroupID != "" {
			groupOf[l.ID] = l.GroupID
		}
	}
	groupR := map[string]float64{}
	for _, g := range e.spec.Groups {
		groupR[g.ID] = g.Correlation
	}
	for i := 0; i < len(e.leafIDs); i++ {
		for j := i + 1; j < len(e.leafIDs); j++ {
			a, b := e.leafIDs[i], e.leafIDs[j]
			if ga, gb := groupOf[a], groupOf[b]; ga != "" && ga == gb {
				put(a, b, groupR[ga])
			}
		}
	}
	for _, pr := range e.spec.PairRules {
		put(pr.A, pr.B, pr.Correlation)
	}
	tol := PSDToleranceRel * math.Max(scale, 0)
	if f := checkPSD(m, tol); f != nil {
		var members []string
		for _, mi := range f.members {
			members = append(members, e.rowName(mi))
		}
		return m, &Problem{Code: "correlation_not_psd", Target: "correlation_matrix",
			Detail: fmt.Sprintf("covariance matrix is not positive semidefinite (eigenvalue %.6g); dominant inputs: %v", f.value, members)}
	}
	return m, nil
}

func (e *eval) topoOrder() ([]string, Problems) {
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, l := range e.spec.Leaves {
		indeg[l.ID] = 0
	}
	for _, n := range e.spec.Nodes {
		indeg[n.ID] = 0
	}
	for _, ed := range e.spec.Edges {
		adj[ed.From] = append(adj[ed.From], ed.To)
		indeg[ed.To]++
	}
	var q []string
	emit := func(cand map[string]int) {
	}
	_ = emit
	// deterministic: leaves first in spec order, then nodes in spec order.
	var ready []string
	for _, l := range e.spec.Leaves {
		if indeg[l.ID] == 0 {
			ready = append(ready, l.ID)
		}
	}
	for {
		picked := ""
		for _, id := range ready {
			if indeg[id] == 0 {
				picked = id
				break
			}
		}
		if picked == "" {
			// nodes may become ready in spec order
			for _, n := range e.spec.Nodes {
				if indeg[n.ID] == 0 {
					picked = n.ID
					break
				}
			}
		}
		if picked == "" {
			break
		}
		indeg[picked] = -1
		q = append(q, picked)
		// remove from ready
		nr := ready[:0]
		for _, x := range ready {
			if x != picked {
				nr = append(nr, x)
			}
		}
		ready = nr
		for _, y := range adj[picked] {
			indeg[y]--
			if indeg[y] == 0 {
				ready = append(ready, y)
			}
		}
	}
	if len(q) != len(e.spec.Leaves)+len(e.spec.Nodes) {
		return nil, Problems{{Code: "dependency_cycle", Target: "graph", Detail: "cycle survived topological sort"}}
	}
	return q, nil
}
