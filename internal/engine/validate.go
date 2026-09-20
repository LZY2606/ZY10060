package engine

import (
	"fmt"
	"math"
)

func validate(spec Spec) (Problems, map[string]bool, map[string]int) {
	var probs Problems
	isNode := map[string]bool{}
	dup := map[string]bool{}
	leafIndex := map[string]int{}

	reg := func(id string) {
		if id == "" {
			probs = append(probs, Problem{Code: "empty_id", Target: "?", Detail: "identifier is empty"})
			return
		}
		if dup[id] {
			probs = append(probs, Problem{Code: "duplicate_id", Target: id, Detail: "identifier used more than once"})
		}
		dup[id] = true
	}
	for i := range spec.Leaves {
		l := &spec.Leaves[i]
		reg(l.ID)
		leafIndex[l.ID] = i
		if !math.IsNaN(l.StdUncertainty) && l.StdUncertainty < 0 {
			probs = append(probs, Problem{Code: "bad_uncertainty", Target: l.ID, Detail: "standard uncertainty must be >= 0"})
		}
		if math.IsNaN(l.Value) || math.IsInf(l.Value, 0) {
			probs = append(probs, Problem{Code: "bad_value", Target: l.ID, Detail: "value is not finite"})
		}
		if math.IsNaN(l.StdUncertainty) || math.IsInf(l.StdUncertainty, 0) {
			probs = append(probs, Problem{Code: "bad_uncertainty", Target: l.ID, Detail: "uncertainty must be finite"})
		}
		switch l.Distribution {
		case DistNormal, DistRectangular, DistTriangular, DistUshaped:
		case DistT:
			if l.Nu == nil || *l.Nu <= 0 {
				probs = append(probs, Problem{Code: "bad_distribution", Target: l.ID, Detail: "t distribution requires positive nu"})
			}
		default:
			probs = append(probs, Problem{Code: "bad_distribution", Target: l.ID, Detail: fmt.Sprintf("unknown distribution %q", l.Distribution)})
		}
	}
	for i := range spec.Nodes {
		n := &spec.Nodes[i]
		reg(n.ID)
		isNode[n.ID] = true
		switch n.Kind {
		case NodeConvert, NodeWeighted, NodeDiff, NodeCalibration:
		default:
			probs = append(probs, Problem{Code: "bad_node_kind", Target: n.ID, Detail: fmt.Sprintf("unknown node kind %q", n.Kind)})
		}
	}
	groupIDs := map[string]bool{}
	for _, g := range spec.Groups {
		if g.ID == "" {
			probs = append(probs, Problem{Code: "empty_group_id", Target: "groups", Detail: "group id is empty"})
			continue
		}
		groupIDs[g.ID] = true
		if g.Correlation < -1 || g.Correlation > 1 {
			probs = append(probs, Problem{Code: "bad_correlation", Target: "group:" + g.ID, Detail: "correlation must be within [-1,1]"})
		}
	}
	certIDs := map[string]bool{}
	for _, c := range spec.Certificates {
		certIDs[c.ID] = true
	}
	for _, l := range spec.Leaves {
		if l.GroupID != "" && !groupIDs[l.GroupID] {
			probs = append(probs, Problem{Code: "unknown_group", Target: l.ID, Detail: "leaf references undefined group " + l.GroupID})
		}
		if l.CertificateID != "" && !certIDs[l.CertificateID] {
			probs = append(probs, Problem{Code: "unknown_certificate", Target: l.ID, Detail: "leaf references certificate not present in snapshot: " + l.CertificateID})
		}
	}
	for _, pr := range spec.PairRules {
		if _, ok := leafIndex[pr.A]; !ok {
			probs = append(probs, Problem{Code: "unknown_pair_member", Target: "pair:" + pr.A + "~" + pr.B, Detail: pr.A + " is not a leaf"})
		}
		if _, ok := leafIndex[pr.B]; !ok {
			probs = append(probs, Problem{Code: "unknown_pair_member", Target: "pair:" + pr.A + "~" + pr.B, Detail: pr.B + " is not a leaf"})
		}
		if pr.A == pr.B {
			probs = append(probs, Problem{Code: "bad_correlation", Target: "pair:" + pr.A + "~" + pr.B, Detail: "pair members must differ"})
		}
		if pr.Correlation < -1 || pr.Correlation > 1 {
			probs = append(probs, Problem{Code: "bad_correlation", Target: "pair:" + pr.A + "~" + pr.B, Detail: "correlation must be within [-1,1]"})
		}
	}

	in := map[string][]EdgeSpec{}
	for _, e := range spec.Edges {
		if !dup[e.From] || !dup[e.To] {
			missing := e.From
			if !dup[e.To] {
				missing = e.To
			}
			probs = append(probs, Problem{Code: "dangling_edge", Target: missing, Edge: e.From + "->" + e.To, Detail: "edge endpoint does not exist"})
			continue
		}
		if !isNode[e.To] {
			probs = append(probs, Problem{Code: "edge_into_leaf", Target: e.To, Edge: e.From + "->" + e.To, Detail: "edges can only enter derived nodes"})
		}
		if e.From == e.To {
			probs = append(probs, Problem{Code: "self_loop", Target: e.To, Edge: e.From + "->" + e.To, Detail: "self dependency"})
		}
		in[e.To] = append(in[e.To], e)
	}
	for _, n := range spec.Nodes {
		ins := in[n.ID]
		switch n.Kind {
		case NodeConvert:
			if len(ins) != 1 {
				probs = append(probs, Problem{Code: "bad_arity", Target: n.ID, Detail: fmt.Sprintf("convert needs exactly 1 input, got %d", len(ins))})
			}
		case NodeWeighted:
			if len(ins) < 2 {
				probs = append(probs, Problem{Code: "bad_arity", Target: n.ID, Detail: fmt.Sprintf("weighted mean needs >=2 inputs, got %d", len(ins))})
			}
			if len(n.Weights) != len(ins) {
				probs = append(probs, Problem{Code: "bad_weights", Target: n.ID, Detail: fmt.Sprintf("weights length %d != inputs %d", len(n.Weights), len(ins))})
			}
			for _, w := range n.Weights {
				if w < 0 {
					probs = append(probs, Problem{Code: "bad_weights", Target: n.ID, Detail: "weights must be >= 0"})
				}
			}
		case NodeDiff:
			roles := map[string]string{}
			for _, e := range ins {
				if e.Role == "a" || e.Role == "b" {
					roles[e.Role] = e.From
				}
			}
			if len(ins) != 2 || roles["a"] == "" || roles["b"] == "" {
				probs = append(probs, Problem{Code: "bad_arity", Target: n.ID, Detail: "diff needs exactly two inputs with roles a and b"})
			}
		case NodeCalibration:
			_, refExists := leafIndex[n.ReferenceLeaf]
			if n.ReferenceLeaf == "" || !refExists {
				probs = append(probs, Problem{Code: "bad_reference", Target: n.ID, Detail: "reference_leaf must identify an existing leaf"})
			}
			if !certIDs[n.CertificateID] {
				probs = append(probs, Problem{Code: "unknown_certificate", Target: n.ID, Detail: "calibration needs a certificate present in the snapshot"})
			}
			var reading string
			for _, e := range ins {
				if e.Role == "reading" {
					reading = e.From
				}
			}
			if reading == "" {
				probs = append(probs, Problem{Code: "bad_arity", Target: n.ID, Detail: "calibration needs one incoming edge with role reading"})
			}
		}
	}

	// Cycle detection runs before arity checks so a cyclic dependency is
	// reported even when node arities are also violated.
	if cyc := detectCycle(spec.Nodes, spec.Edges, in); cyc != nil {
		probs = append(cyc, probs...)
	}
	return probs, isNode, leafIndex
}

// detectCycle runs Kahn's algorithm over all vertices; leftovers are on a cycle.
func detectCycle(nodes []NodeSpec, edges []EdgeSpec, in map[string][]EdgeSpec) Problems {
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, n := range nodes {
		indeg[n.ID] = 0
	}
	for _, e := range edges {
		if _, ok := indeg[e.To]; !ok {
			indeg[e.To] = 0
		}
		if _, ok := indeg[e.From]; !ok {
			indeg[e.From] = 0
		}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}
	var queue []string
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	seen := 0
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		seen++
		for _, y := range adj[x] {
			indeg[y]--
			if indeg[y] == 0 {
				queue = append(queue, y)
			}
		}
	}
	if seen == len(indeg) {
		return nil
	}
	var onCycle []string
	for id, d := range indeg {
		if d > 0 {
			onCycle = append(onCycle, id)
		}
	}
	var probs Problems
	for _, e := range edges {
		hit := map[string]bool{}
		for _, id := range onCycle {
			hit[id] = true
		}
		if hit[e.From] && hit[e.To] {
			probs = append(probs, Problem{Code: "dependency_cycle", Target: e.To, Edge: e.From + "->" + e.To, Detail: "edge participates in a dependency cycle: " + joinIDs(onCycle)})
		}
	}
	if len(probs) == 0 {
		probs = append(probs, Problem{Code: "dependency_cycle", Target: joinIDs(onCycle), Detail: "dependency graph contains a cycle"})
	}
	return probs
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " -> "
		}
		out += id
	}
	return out
}
