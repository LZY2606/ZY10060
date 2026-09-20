package engine

// Formula/rule versions retained with every produced result.
const (
	VersionUnits       = "UNITS-SI-v2019"
	VersionGUM         = "GUM-JCGM100-2008"
	VersionWelch       = "WS-GUM-G4-2008"
	VersionWeighted    = "WEIGHTED-MEAN-v1"
	VersionDiff        = "LINEAR-DIFFERENCE-v1"
	VersionCalibration = "CALIBRATION-LINEAR-v1"
	VersionConvert     = "UNIT-CONVERT-v1"
	PSDToleranceRel    = 1e-10
)

// Distribution selects how a leaf maps a stated standard uncertainty to a
// (semi)distribution. Normal and t use nu directly; the bounded shapes derive
// infinite effective degrees of freedom per GUM.
type Distribution string

const (
	DistNormal      Distribution = "normal"
	DistT           Distribution = "t"
	DistRectangular Distribution = "rectangular"
	DistTriangular  Distribution = "triangular"
	DistUshaped     Distribution = "u-shaped"
)

// LeafSpec is one original observation (an input quantity of the chain).
type LeafSpec struct {
	ID             string       `json:"id"`
	Value          float64      `json:"value"`
	Unit           string       `json:"unit"`
	StdUncertainty float64      `json:"std_uncertainty"`
	Distribution   Distribution `json:"distribution"`
	Nu             *float64     `json:"nu,omitempty"`
	GroupID        string       `json:"group_id,omitempty"`
	CertificateID  string       `json:"certificate_id,omitempty"`
}

// Node kinds.
const (
	NodeConvert     = "convert"
	NodeWeighted    = "weighted"
	NodeDiff        = "diff"
	NodeCalibration = "calibration"
)

// NodeSpec is one derived step.
type NodeSpec struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Unit          string    `json:"unit"`
	Weights       []float64 `json:"weights,omitempty"`        // weighted: per incoming edge
	ReferenceLeaf string    `json:"reference_leaf,omitempty"` // calibration: x0
	CertificateID string    `json:"certificate_id,omitempty"` // calibration: cert id
}

// EdgeSpec connects an input (leaf or node) into a node.
type EdgeSpec struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Role   string  `json:"role,omitempty"` // diff: a/b ; calibration: ref/corr
	Weight float64 `json:"weight,omitempty"`
}

// GroupSpec declares equal pairwise correlation within a group.
type GroupSpec struct {
	ID          string  `json:"id"`
	Correlation float64 `json:"correlation"`
}

// PairRule declares an explicit pairwise correlation between two leaves.
type PairRule struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Correlation float64 `json:"correlation"`
}

// CertInfo carries only the calibration facts the engine needs.
type CertInfo struct {
	ID              string  `json:"id"`
	Slope           float64 `json:"slope"`
	Intercept       float64 `json:"intercept"`
	SlopeU          float64 `json:"slope_u"`
	InterceptU      float64 `json:"intercept_u"`
	SlopeInterceptR float64 `json:"slope_intercept_r"`
	Nu              float64 `json:"nu"`
	ValidUntil      string  `json:"valid_until"` // RFC3339
	RevokedAt       string  `json:"revoked_at,omitempty"`
}

// Spec is one complete, self-contained calculation scenario snapshot.
type Spec struct {
	Leaves       []LeafSpec  `json:"leaves"`
	Nodes        []NodeSpec  `json:"nodes"`
	Edges        []EdgeSpec  `json:"edges"`
	Groups       []GroupSpec `json:"groups"`
	PairRules    []PairRule  `json:"pairs"`
	Certificates []CertInfo  `json:"certificates"`
	Now          string      `json:"now,omitempty"` // RFC3339; overrides clock
	Frozen       bool        `json:"frozen,omitempty"`
	CoverageP    float64     `json:"coverage_p,omitempty"`
}

// Problem locates one validation defect on a specific edge or input.
type Problem struct {
	Code   string `json:"code"`
	Target string `json:"target"`
	Edge   string `json:"edge,omitempty"`
	Detail string `json:"detail"`
}

func (p Problem) Error() string {
	if p.Edge != "" {
		return p.Code + " at " + p.Target + " edge " + p.Edge + ": " + p.Detail
	}
	return p.Code + " at " + p.Target + ": " + p.Detail
}

// Problems is the ordered list of defects found in one run.
type Problems []Problem

func (ps Problems) Error() string {
	if len(ps) == 0 {
		return "problems"
	}
	return ps[0].Error() + fmtCount(len(ps))
}

func fmtCount(n int) string {
	if n == 1 {
		return ""
	}
	return " (+" + itoa(n-1) + " more)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
