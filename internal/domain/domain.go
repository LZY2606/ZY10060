package domain

import "time"

const SchemaVersion = "metrology-lab/v1"
const RuleSetVersion = "ruleset/2026-09/v1"

type State struct {
	SchemaVersion  string                      `json:"schemaVersion"`
	RuleSetVersion string                      `json:"ruleSetVersion"`
	Observations   map[string]Observation      `json:"observations"`
	Groups         map[string]CorrelationGroup `json:"groups"`
	Certificates   map[string]Certificate      `json:"certificates"`
	Scenarios      map[string]Scenario         `json:"scenarios"`
	Results        map[string]Result           `json:"activeResults"`
	FrozenResults  map[string]FrozenResult     `json:"frozenResults"`
	Requests       map[string]RequestRecord    `json:"requests"`
	EventSeq       int64                       `json:"eventSeq"`
}

type Observation struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Value               float64   `json:"value"`
	Unit                string    `json:"unit"`
	StandardUncertainty float64   `json:"standardUncertainty"`
	Distribution        string    `json:"distribution"`
	DegreesOfFreedom    float64   `json:"degreesOfFreedom,omitempty"`
	CorrelationGroupID  string    `json:"correlationGroupId,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

type CorrelationGroup struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Coefficient float64   `json:"coefficient"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Certificate struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Correction          float64   `json:"correction"`
	Unit                string    `json:"unit"`
	StandardUncertainty float64   `json:"standardUncertainty"`
	DegreesOfFreedom    float64   `json:"degreesOfFreedom"`
	ValidFrom           time.Time `json:"validFrom"`
	ExpiresAt           time.Time `json:"expiresAt"`
	ReplacedBy          string    `json:"replacedBy,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

type Scenario struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	OutputNode     string                `json:"outputNodeId"`
	Nodes          []Node                `json:"nodes"`
	Overrides      []CorrelationOverride `json:"correlationOverrides,omitempty"`
	FrozenResultID string                `json:"frozenResultId,omitempty"`
	CreatedAt      time.Time             `json:"createdAt"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

type CorrelationOverride struct {
	LeftID      string  `json:"leftId"`
	RightID     string  `json:"rightId"`
	Coefficient float64 `json:"coefficient"`
}

type Node struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	Type          string    `json:"type"`
	ObservationID string    `json:"observationId,omitempty"`
	InputID       string    `json:"inputId,omitempty"`
	LeftID        string    `json:"leftId,omitempty"`
	RightID       string    `json:"rightId,omitempty"`
	InputIDs      []string  `json:"inputIds,omitempty"`
	Weights       []float64 `json:"weights,omitempty"`
	FromUnit      string    `json:"fromUnit,omitempty"`
	ToUnit        string    `json:"toUnit,omitempty"`
	CertificateID string    `json:"certificateId,omitempty"`
}

type Result struct {
	ScenarioID          string                `json:"scenarioId"`
	Status              string                `json:"status"`
	Value               float64               `json:"value,omitempty"`
	Unit                string                `json:"unit,omitempty"`
	StandardUncertainty float64               `json:"standardUncertainty,omitempty"`
	CoverageFactor      float64               `json:"coverageFactor,omitempty"`
	LowerBound          float64               `json:"lowerBound,omitempty"`
	UpperBound          float64               `json:"upperBound,omitempty"`
	DegreesOfFreedom    float64               `json:"degreesOfFreedom,omitempty"`
	Nodes               map[string]NodeResult `json:"nodes,omitempty"`
	Contributions       []Contribution        `json:"contributions,omitempty"`
	Sensitivities       []Sensitivity         `json:"sensitivities,omitempty"`
	FormulaVersions     map[string]string     `json:"formulaVersions,omitempty"`
	ConversionPaths     map[string][]string   `json:"conversionPaths,omitempty"`
	BlockingIssues      []Issue               `json:"blockingIssues,omitempty"`
	ComputedAt          time.Time             `json:"computedAt"`
	RuleSetVersion      string                `json:"ruleSetVersion"`
}

type FrozenResult struct {
	ID         string    `json:"id"`
	ScenarioID string    `json:"scenarioId"`
	Result     Result    `json:"result"`
	FrozenAt   time.Time `json:"frozenAt"`
	Note       string    `json:"note,omitempty"`
}

type NodeResult struct {
	ID                  string             `json:"id"`
	Type                string             `json:"type"`
	CertificateID       string             `json:"certificateId,omitempty"`
	Value               float64            `json:"value"`
	Unit                string             `json:"unit"`
	StandardUncertainty float64            `json:"standardUncertainty"`
	DegreesOfFreedom    float64            `json:"degreesOfFreedom"`
	Coefficients        map[string]float64 `json:"coefficients"`
	FormulaVersion      string             `json:"formulaVersion"`
	Contributions       []Contribution     `json:"contributions,omitempty"`
	Sensitivities       []Sensitivity      `json:"sensitivities,omitempty"`
}

type Contribution struct {
	SourceID             string  `json:"sourceId"`
	Kind                 string  `json:"kind"`
	NodeID               string  `json:"nodeId,omitempty"`
	VarianceContribution float64 `json:"varianceContribution"`
	Fraction             float64 `json:"fraction"`
	Rank                 int     `json:"rank"`
}

type Sensitivity struct {
	SourceID    string  `json:"sourceId"`
	Kind        string  `json:"kind"`
	Coefficient float64 `json:"coefficient"`
	Effect      float64 `json:"standardUncertaintyEffect"`
	Rank        int     `json:"rank"`
}

type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
	Edge    string `json:"edge,omitempty"`
	InputID string `json:"inputId,omitempty"`
	Path    string `json:"path,omitempty"`
}

type RequestRecord struct {
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Fingerprint string    `json:"fingerprint"`
	StatusCode  int       `json:"statusCode"`
	Response    []byte    `json:"response"`
	CompletedAt time.Time `json:"completedAt"`
}

type Event struct {
	Seq           int64          `json:"seq"`
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	RequestID     string         `json:"requestId,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	SchemaVersion string         `json:"schemaVersion"`
}

func NewState() *State {
	return &State{
		SchemaVersion:  SchemaVersion,
		RuleSetVersion: RuleSetVersion,
		Observations:   map[string]Observation{},
		Groups:         map[string]CorrelationGroup{},
		Certificates:   map[string]Certificate{},
		Scenarios:      map[string]Scenario{},
		Results:        map[string]Result{},
		FrozenResults:  map[string]FrozenResult{},
		Requests:       map[string]RequestRecord{},
	}
}
