package engine

// Contribution attributes combined variance at one output to an input
// quantity. Self terms and correlation cross-terms are reported separately.
type Contribution struct {
	Source         string  `json:"source"`
	Kind           string  `json:"kind"` // self | pair
	With           string  `json:"with,omitempty"`
	Sensitivity    float64 `json:"sensitivity"`
	StdUncertainty float64 `json:"std_uncertainty,omitempty"`
	VarianceTerm   float64 `json:"variance_term"`
	Share          float64 `json:"share"` // signed fraction of combined variance
}

// SensitivityItem ranks one input by absolute sensitivity coefficient.
type SensitivityItem struct {
	Source      string  `json:"source"`
	Sensitivity float64 `json:"sensitivity"`
}

// NodeResult is the computed outcome for one vertex (leaf or node).
type NodeResult struct {
	ID                  string            `json:"id"`
	Kind                string            `json:"kind"`
	Value               float64           `json:"value"`
	DisplayValue        float64           `json:"display_value"`
	Unit                string            `json:"unit"`
	BaseUnit            string            `json:"base_unit"`
	StdUncertainty      float64           `json:"std_uncertainty"`
	DisplayU            float64           `json:"display_std_uncertainty"`
	NuEff               float64           `json:"nu_eff"`
	CoverageP           float64           `json:"coverage_p"`
	K                   float64           `json:"k"`
	IntervalLow         float64           `json:"interval_low"`
	IntervalHigh        float64           `json:"interval_high"`
	Formula             string            `json:"formula_version"`
	Contributions       []Contribution    `json:"contributions"`
	Sensitivities       []SensitivityItem `json:"sensitivities"`
	PendingExpiredCerts []string          `json:"pending_expired_certificates,omitempty"`
	RevokedCerts        []string          `json:"revoked_certificates,omitempty"`
	Redacted            bool              `json:"redacted,omitempty"`
}

// EdgeResult documents unit handling and first-order coefficient per edge.
type EdgeResult struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Role        string  `json:"role,omitempty"`
	FromUnit    string  `json:"from_unit"`
	ToInputUnit string  `json:"to_input_unit"`
	DimMatch    bool    `json:"dim_match"`
	Sensitivity float64 `json:"sensitivity"`
}

// UnitPath documents the conversion path used by every vertex and edge.
type UnitPath struct {
	Subject string           `json:"subject"` // leaf/node/edge id
	From    string           `json:"from"`
	To      string           `json:"to"`
	Steps   []ConversionStep `json:"steps"`
	Slope   float64          `json:"slope"`
}

// Output is everything produced by one deterministic evaluation.
type Output struct {
	Results         map[string]NodeResult `json:"results"`
	Order           []string              `json:"order"`
	Edges           []EdgeResult          `json:"edges"`
	UnitPaths       []UnitPath            `json:"unit_paths"`
	CorrelationUsed [][]float64           `json:"correlation_used"`
	LeafOrder       []string              `json:"leaf_order"`
	Status          string                `json:"status"` // ok | pending
	RuleVersions    map[string]string     `json:"rule_versions"`
	ChecksumInputs  string                `json:"checksum_inputs"`
}

// MarshalJSON renders non-finite floats (nu_eff=+Inf for type-B inputs, NaN
// pending intervals) as JSON null instead of failing serialization.
func (nr NodeResult) MarshalJSON() ([]byte, error) {
	type alias NodeResult
	return jsonMarshal(struct {
		alias
		NuEff        any `json:"nu_eff"`
		IntervalLow  any `json:"interval_low"`
		IntervalHigh any `json:"interval_high"`
		K            any `json:"k"`
	}{
		alias:        alias(nr),
		NuEff:        numField(nr.NuEff),
		IntervalLow:  lowField(nr),
		IntervalHigh: highField(nr),
		K:            kField(nr),
	})
}

// RedactConfidence removes coverage numbers from nodes that must not present
// them (pending expired/revoked certificates). Safe to call repeatedly and on
// outputs restored from disk, where nulls have become Go zeros.
func (o *Output) RedactConfidence() {
	for id, nr := range o.Results {
		if len(nr.PendingExpiredCerts) > 0 || len(nr.RevokedCerts) > 0 {
			nr.K = 0
			nr.IntervalLow = 0
			nr.IntervalHigh = 0
			nr.Redacted = true
			o.Results[id] = nr
		}
	}
}
