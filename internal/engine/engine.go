package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"time"
)

// Run validates and evaluates a complete scenario snapshot.
// All numeric judgments happen here; callers must never recompute results.
func Run(spec Spec, confirmed bool) (*Output, Problems) {
	probs, _, leafIndex := validate(spec)
	if len(probs) > 0 {
		return nil, probs
	}
	p := coverage(spec.CoverageP)

	// Stable leaf ordering.
	leafIDs := make([]string, len(spec.Leaves))
	for i, l := range spec.Leaves {
		leafIDs[i] = l.ID
	}

	// Certificate parameter rows: two rows per certificate (slope, intercept).
	certByID := map[string]CertInfo{}
	var certOrder []string
	for _, c := range spec.Certificates {
		certByID[c.ID] = c
		certOrder = append(certOrder, c.ID)
	}

	ev := &eval{
		spec:      spec,
		leafIndex: leafIndex,
		leafIDs:   leafIDs,
		certByID:  certByID,
		certOrder: certOrder,
		paths:     map[string]ConversionPath{},
		rows:      map[string]rowInfo{},
		values:    map[string]float64{},
		coeffs:    map[string][]float64{},
		units:     map[string]string{},
		kinds:     map[string]string{},
		p:         p,
	}
	ev.n = len(leafIDs) + 2*len(certOrder)
	for _, c := range certOrder {
		ev.rows[c+":slope"] = rowInfo{index: len(leafIDs) + 2*certPos(certOrder, c), cert: c, param: "slope"}
		ev.rows[c+":intercept"] = rowInfo{index: len(leafIDs) + 2*certPos(certOrder, c) + 1, cert: c, param: "intercept"}
	}
	if cov, prob := ev.buildCovariance(); prob != nil {
		return nil, Problems{*prob}
	} else {
		ev.cov = cov
	}

	topo, probs2 := ev.topoOrder()
	if probs2 != nil {
		return nil, probs2
	}

	status := "ok"
	var expired, revoked []string
	now := timeNow(spec.Now)
	for _, c := range spec.Certificates {
		if c.RevokedAt != "" {
			t, err := time.Parse(time.RFC3339, c.RevokedAt)
			if err == nil && !now.Before(t) {
				revoked = append(revoked, c.ID)
			}
		}
		if c.ValidUntil != "" {
			t, err := time.Parse(time.RFC3339, c.ValidUntil)
			if err == nil && now.After(t) {
				expired = append(expired, c.ID)
			}
		}
	}
	badCerts := map[string]bool{}
	for _, id := range expired {
		badCerts[id] = true
	}
	for _, id := range revoked {
		badCerts[id] = true
	}

	results := map[string]NodeResult{}
	var edgeResults []EdgeResult
	for _, id := range topo {
		if li, ok := leafIndex[id]; ok {
			ev.evalLeaf(spec.Leaves[li])
			continue
		}
		n := nodeByID(spec, id)
		er, prob := ev.evalNode(n, badCerts)
		if prob != nil {
			return nil, Problems{*prob}
		}
		edgeResults = append(edgeResults, er...)
	}

	// Assemble node results in topological order.
	for _, id := range topo {
		if _, ok := leafIndex[id]; ok {
			continue
		}
		n := nodeByID(spec, id)
		nr := ev.resultFor(n, p)
		for _, idb := range expired {
			if ev.nodeUsesCert(n, idb) {
				nr.PendingExpiredCerts = append(nr.PendingExpiredCerts, idb)
			}
		}
		for _, idb := range revoked {
			if ev.nodeUsesCert(n, idb) {
				nr.RevokedCerts = append(nr.RevokedCerts, idb)
			}
		}
		if (len(nr.PendingExpiredCerts) > 0 || len(nr.RevokedCerts) > 0) && !confirmed && !spec.Frozen {
			nr.StatusPending()
			status = "pending"
		}
		results[id] = nr
	}
	// leaf results too (for display)
	for _, l := range spec.Leaves {
		results[l.ID] = ev.resultForLeaf(l, p)
	}

	out := &Output{
		Results:   results,
		Order:     topo,
		Edges:     edgeResults,
		LeafOrder: leafIDs,
		Status:    status,
		RuleVersions: map[string]string{
			"units": VersionUnits, "propagation": VersionGUM, "welch_satterthwaite": VersionWelch,
			"weighted_mean": VersionWeighted, "difference": VersionDiff,
			"calibration": VersionCalibration, "conversion": VersionConvert,
		},
	}
	out.UnitPaths = ev.collectUnitPaths()
	out.CorrelationUsed = ev.covAsMatrix()
	out.ChecksumInputs = ev.inputChecksum()
	return out, nil
}

func coverage(p float64) float64 {
	if p <= 0 || p >= 1 {
		return 0.95
	}
	return p
}

func certPos(order []string, id string) int {
	for i, x := range order {
		if x == id {
			return i
		}
	}
	return -1
}

func nodeByID(spec Spec, id string) NodeSpec {
	for _, n := range spec.Nodes {
		if n.ID == id {
			return n
		}
	}
	return NodeSpec{}
}

func timeNow(override string) time.Time {
	if override != "" {
		if t, err := time.Parse(time.RFC3339, override); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

func checksumOf(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

var _ = sort.Strings
var _ = math.Inf
