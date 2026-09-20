package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"metrologylab/internal/domain"
	"metrologylab/internal/units"
)

func validateStructure(sc domain.Scenario, st *domain.State) []domain.Issue {
	issues := []domain.Issue{}
	seen := map[string]bool{}
	for _, n := range sc.Nodes {
		if n.ID == "" {
			issues = append(issues, domain.Issue{Code: "INVALID_NODE", Path: "id", Message: "node id is required"})
		}
		if seen[n.ID] {
			issues = append(issues, domain.Issue{Code: "DUPLICATE_NODE", NodeID: n.ID, Message: "duplicate node id"})
		}
		seen[n.ID] = true
	}
	if !seen[sc.OutputNode] {
		issues = append(issues, domain.Issue{Code: "UNKNOWN_OUTPUT", NodeID: sc.OutputNode, Path: "outputNodeId", Message: "output node is absent"})
	}
	for _, n := range sc.Nodes {
		require := func(id string, path string) bool {
			if !seen[id] {
				issues = append(issues, domain.Issue{Code: "UNKNOWN_EDGE", NodeID: n.ID, Edge: id + "->" + n.ID, Path: path, Message: "edge references missing node"})
				return false
			}
			return true
		}
		switch n.Type {
		case "observation":
			o, ok := st.Observations[n.ObservationID]
			if !ok {
				issues = append(issues, domain.Issue{Code: "UNKNOWN_INPUT", NodeID: n.ID, InputID: n.ObservationID, Path: "observationId", Message: "observation is missing"})
			} else if o.StandardUncertainty < 0 || math.IsNaN(o.Value) || math.IsNaN(o.StandardUncertainty) {
				issues = append(issues, domain.Issue{Code: "INVALID_INPUT", NodeID: n.ID, InputID: o.ID, Message: "numeric observation is invalid"})
			}
		case "conversion":
			if require(n.InputID, "inputId") {
				if n.ToUnit == "" {
					issues = append(issues, domain.Issue{Code: "INVALID_UNIT", NodeID: n.ID, Path: "toUnit", Message: "target unit is required"})
				}
				if _, ok := units.Canonical(n.ToUnit); !ok {
					issues = append(issues, domain.Issue{Code: "UNKNOWN_UNIT", NodeID: n.ID, Path: "toUnit", Message: "target unit is unsupported"})
				}
			}
		case "weighted":
			if len(n.InputIDs) < 2 {
				issues = append(issues, domain.Issue{Code: "INVALID_NODE", NodeID: n.ID, Path: "inputIds", Message: "weighted node needs at least two inputs"})
			}
			for _, id := range n.InputIDs {
				require(id, "inputIds")
			}
			if len(n.Weights) != 0 && len(n.Weights) != len(n.InputIDs) {
				issues = append(issues, domain.Issue{Code: "INVALID_WEIGHTS", NodeID: n.ID, Message: "weights length must match inputs"})
			}
		case "difference":
			require(n.LeftID, "leftId")
			require(n.RightID, "rightId")
		case "calibration":
			require(n.InputID, "inputId")
			if _, ok := st.Certificates[n.CertificateID]; !ok {
				issues = append(issues, domain.Issue{Code: "CERTIFICATE_NOT_FOUND", NodeID: n.ID, Path: "certificateId", Message: "certificate does not exist"})
			}
		default:
			issues = append(issues, domain.Issue{Code: "UNKNOWN_NODE_TYPE", NodeID: n.ID, Path: "type", Message: "unsupported node type"})
		}
	}
	return issues
}

func topoOrder(sc domain.Scenario) ([]domain.Node, []domain.Issue) {
	nodes := map[string]domain.Node{}
	indeg := map[string]int{}
	edges := map[string][]string{}
	for _, n := range sc.Nodes {
		nodes[n.ID] = n
		indeg[n.ID] = 0
	}
	add := func(from, to string) {
		if from == "" {
			return
		}
		indeg[to]++
		edges[from] = append(edges[from], to)
	}
	for _, n := range sc.Nodes {
		if n.Type == "conversion" || n.Type == "calibration" {
			add(n.InputID, n.ID)
		}
		if n.Type == "difference" {
			add(n.LeftID, n.ID)
			add(n.RightID, n.ID)
		}
		if n.Type == "weighted" {
			for _, id := range n.InputIDs {
				add(id, n.ID)
			}
		}
	}
	q := []string{}
	for _, n := range sc.Nodes {
		if indeg[n.ID] == 0 {
			q = append(q, n.ID)
		}
	}
	order := []domain.Node{}
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		order = append(order, nodes[id])
		for _, next := range edges[id] {
			indeg[next]--
			if indeg[next] == 0 {
				q = append(q, next)
			}
		}
	}
	if len(order) == len(sc.Nodes) {
		return order, nil
	}
	bad := []string{}
	for id, d := range indeg {
		if d > 0 {
			bad = append(bad, id)
		}
	}
	sort.Strings(bad)
	return nil, []domain.Issue{{Code: "DEPENDENCY_CYCLE", Message: fmt.Sprintf("cycle reaches nodes: %v", bad), Path: "nodes", Edge: join(bad)}}
}
func join(xs []string) string {
	s := ""
	for i, x := range xs {
		if i > 0 {
			s += ","
		}
		s += x
	}
	return s
}

func validateCorrelations(sc domain.Scenario, st *domain.State) []domain.Issue {
	groups := map[string][]domain.Observation{}
	observed := map[string]bool{}
	for _, n := range sc.Nodes {
		if n.Type == "observation" {
			o := st.Observations[n.ObservationID]
			observed[o.ID] = true
			if o.CorrelationGroupID != "" {
				groups[o.CorrelationGroupID] = append(groups[o.CorrelationGroupID], o)
			}
		}
	}
	issues := []domain.Issue{}
	for gid, obs := range groups {
		g, ok := st.Groups[gid]
		if !ok {
			issues = append(issues, domain.Issue{Code: "CORRELATION_GROUP_NOT_FOUND", InputID: gid, Message: "correlation group is missing"})
			continue
		}
		if g.Coefficient < -1 || g.Coefficient > 1 {
			issues = append(issues, domain.Issue{Code: "CORRELATION_NOT_PSD", InputID: gid, Message: "correlation must be in [-1,1]"})
		}
		if len(obs) >= 2 {
			vals := make([]float64, len(obs)*(len(obs)-1)/2)
			for i := range vals {
				vals[i] = g.Coefficient
			}
			if pair, ok := nonPSD(vals); ok {
				issues = append(issues, domain.Issue{Code: "CORRELATION_NOT_PSD", InputID: gid, Edge: obs[pair[0]].ID + "," + obs[pair[1]].ID, Message: "correlation matrix is not positive semidefinite"})
			}
		}
	}
	pairs := map[string]float64{}
	for _, ov := range sc.Overrides {
		key := pairKey(ov.LeftID, ov.RightID)
		pairs[key] = ov.Coefficient
		if !observed[ov.LeftID] || !observed[ov.RightID] {
			issues = append(issues, domain.Issue{Code: "UNKNOWN_INPUT", Path: "correlationOverrides", Edge: ov.LeftID + "," + ov.RightID, Message: "override endpoints must be observation nodes"})
		}
		if ov.Coefficient < -1 || ov.Coefficient > 1 {
			issues = append(issues, domain.Issue{Code: "CORRELATION_NOT_PSD", Path: "correlationOverrides", Edge: key, Message: "correlation must be in [-1,1]"})
		}
	}
	ids := make([]string, 0, len(observed))
	for id := range observed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 1 {
		off := []float64{}
		var bad [2]int
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				rho, has := pairs[pairKey(ids[i], ids[j])]
				if !has {
					a, b := st.Observations[ids[i]], st.Observations[ids[j]]
					if a.CorrelationGroupID != "" && a.CorrelationGroupID == b.CorrelationGroupID {
						rho = st.Groups[a.CorrelationGroupID].Coefficient
					}
				}
				off = append(off, rho)
			}
		}
		if p, ok := nonPSD(off); ok {
			bad = p
			issues = append(issues, domain.Issue{Code: "CORRELATION_NOT_PSD", Path: "correlationOverrides", Edge: ids[bad[0]] + "," + ids[bad[1]], Message: "effective correlation matrix is not positive semidefinite"})
		}
	}
	return issues
}

func pairKey(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
}

func nonPSD(off []float64) ([2]int, bool) {
	n := len(off) + 1
	a := make([][]float64, n)
	for i := range a {
		a[i] = make([]float64, n)
		a[i][i] = 1
	}
	k := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			a[i][j] = off[k]
			a[j][i] = off[k]
			k++
		}
	}
	for col := 0; col < n; col++ {
		sum := a[col][col]
		for p := 0; p < col; p++ {
			sum -= a[col][p] * a[col][p]
		}
		if sum < -1e-10 {
			for i := 0; i < n; i++ {
				for j := i + 1; j < n; j++ {
					if i == col || j == col {
						return [2]int{i, j}, true
					}
				}
			}
			return [2]int{0, col}, true
		}
		if sum < 0 {
			sum = 0
		}
		diag := math.Sqrt(sum)
		a[col][col] = diag
		for row := col + 1; row < n; row++ {
			sum = a[row][col]
			for p := 0; p < col; p++ {
				sum -= a[row][p] * a[col][p]
			}
			if diag == 0 {
				if math.Abs(sum) > 1e-10 {
					return [2]int{col, row}, true
				}
				a[row][col] = 0
			} else {
				a[row][col] = sum / diag
			}
		}
	}
	return [2]int{}, false
}

func overrideCorrelation(sc domain.Scenario, a, b string) (float64, bool) {
	for _, ov := range sc.Overrides {
		if pairKey(ov.LeftID, ov.RightID) == pairKey(a, b) {
			return ov.Coefficient, true
		}
	}
	return 0, false
}

func covariance(i, j string, sc domain.Scenario, st *domain.State) float64 {
	oi, ok1 := st.Observations[i]
	oj, ok2 := st.Observations[j]
	if !ok1 || !ok2 {
		return 0
	}
	rho, has := overrideCorrelation(sc, i, j)
	if has {
		return rho * oi.StandardUncertainty * oj.StandardUncertainty
	}
	if has {
		return rho * oi.StandardUncertainty * oj.StandardUncertainty
	}
	if oi.CorrelationGroupID != "" && oi.CorrelationGroupID == oj.CorrelationGroupID {
		if g, ok := st.Groups[oi.CorrelationGroupID]; ok {
			return g.Coefficient * oi.StandardUncertainty * oj.StandardUncertainty
		}
	}
	return 0
}

func variance(coef map[string]float64, sc domain.Scenario, st *domain.State, certKey string, cert *domain.Certificate) float64 {
	ids := []string{}
	for k := range coef {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	v := 0.0
	for _, id := range ids {
		c := coef[id]
		if id == certKey {
			v += c * c * cert.StandardUncertainty * cert.StandardUncertainty
			continue
		}
		if o, ok := st.Observations[id]; ok {
			v += c * c * o.StandardUncertainty * o.StandardUncertainty
		}
	}
	for ai := 0; ai < len(ids); ai++ {
		for bi := ai + 1; bi < len(ids); bi++ {
			a, b := ids[ai], ids[bi]
			if strings.HasPrefix(a, "certificate:") || strings.HasPrefix(b, "certificate:") {
				continue
			}
			v += 2 * coef[a] * coef[b] * covariance(a, b, sc, st)
		}
	}
	return v
}

func fillContributions(r *domain.Result, sc domain.Scenario, st *domain.State) {
	out := r.Nodes[sc.OutputNode]
	total := variance(out.Coefficients, sc, st, "", nil)
	if total <= 0 {
		return
	}
	contribs := []domain.Contribution{}
	sens := []domain.Sensitivity{}
	ids := []string{}
	for k := range out.Coefficients {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := out.Coefficients[id]
		var su, diag float64
		kind := "observation"
		nodeID := ""
		if len(id) > 12 && id[:12] == "certificate:" {
			kind = "certificate"
			cid := id[12:]
			cert := st.Certificates[cid]
			su = cert.StandardUncertainty
			diag = c * c * su * su
		} else {
			o := st.Observations[id]
			su = o.StandardUncertainty
			diag = c * c * su * su
			for _, n := range sc.Nodes {
				if n.Type == "observation" && n.ObservationID == id {
					nodeID = n.ID
				}
			}
		}
		cross := 0.0
		for _, j := range ids {
			if j == id || strings.HasPrefix(j, "certificate:") || strings.HasPrefix(id, "certificate:") {
				continue
			}
			cross += c * out.Coefficients[j] * covariance(id, j, sc, st)
		}
		contribs = append(contribs, domain.Contribution{SourceID: id, Kind: kind, NodeID: nodeID, VarianceContribution: diag + cross, Fraction: (diag + cross) / total})
		sens = append(sens, domain.Sensitivity{SourceID: id, Kind: kind, Coefficient: c, Effect: math.Abs(c) * su})
	}
	sort.SliceStable(contribs, func(i, j int) bool {
		return math.Abs(contribs[i].VarianceContribution) > math.Abs(contribs[j].VarianceContribution)
	})
	sort.SliceStable(sens, func(i, j int) bool { return sens[i].Effect > sens[j].Effect })
	for i := range contribs {
		contribs[i].Rank = i + 1
	}
	for i := range sens {
		sens[i].Rank = i + 1
	}
	r.Contributions = contribs
	r.Sensitivities = sens
	for id, nr := range r.Nodes {
		tv := variance(nr.Coefficients, sc, st, "", nil)
		if tv <= 0 {
			continue
		}
		cs := []domain.Contribution{}
		for sid, c := range nr.Coefficients {
			if strings.HasPrefix(sid, "certificate:") {
				continue
			}
			o := st.Observations[sid]
			x := c * c * o.StandardUncertainty * o.StandardUncertainty
			cs = append(cs, domain.Contribution{SourceID: sid, Kind: "observation", NodeID: sid, VarianceContribution: x, Fraction: x / tv})
		}
		sort.SliceStable(cs, func(i, j int) bool {
			return math.Abs(cs[i].VarianceContribution) > math.Abs(cs[j].VarianceContribution)
		})
		for i := range cs {
			cs[i].Rank = i + 1
		}
		nr.Contributions = cs
		r.Nodes[id] = nr
	}
}

func resolveCertificate(st *domain.State, id string) (domain.Certificate, bool) {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		cert, ok := st.Certificates[id]
		if !ok {
			return domain.Certificate{}, false
		}
		if cert.ReplacedBy == "" {
			return cert, true
		}
		id = cert.ReplacedBy
	}
	return domain.Certificate{}, false
}

func mixedCovariance(a, b string, sc domain.Scenario, st *domain.State) float64 {
	if strings.HasPrefix(a, "certificate:") || strings.HasPrefix(b, "certificate:") {
		return 0
	}
	return covariance(a, b, sc, st)
}
