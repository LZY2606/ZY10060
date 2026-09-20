// Package service holds all business judgments: validation status codes,
// idempotency, freeze semantics, certificate lifecycle and audit bundles.
package service

import (
	"metrolab/internal/engine"
	"time"
)

// Observation is one original input record. Inputs are versioned; updating an
// observation creates a new revision and never rewrites history.
type Observation struct {
	ID             string              `json:"id"`
	Revision       int                 `json:"revision"`
	Value          float64             `json:"value"`
	Unit           string              `json:"unit"`
	StdUncertainty float64             `json:"std_uncertainty"`
	Distribution   engine.Distribution `json:"distribution"`
	Nu             *float64            `json:"nu,omitempty"`
	CreatedAt      string              `json:"created_at"`
	RetiredAt      string              `json:"retired_at,omitempty"`
}

// ScenarioNode mirrors engine.NodeSpec at the service boundary.
type ScenarioNode struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Unit          string    `json:"unit"`
	Weights       []float64 `json:"weights,omitempty"`
	ReferenceLeaf string    `json:"reference_leaf,omitempty"`
	CertificateID string    `json:"certificate_id,omitempty"`
}

// ScenarioEdge mirrors engine.EdgeSpec.
type ScenarioEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Role   string  `json:"role,omitempty"`
	Weight float64 `json:"weight,omitempty"`
}

type CorrGroup struct {
	ID          string   `json:"id"`
	Correlation float64  `json:"correlation"`
	Members     []string `json:"members,omitempty"`
}

type CorrPair struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Correlation float64 `json:"correlation"`
}

// Scenario is one candidate chain built on top of observations.
type Scenario struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Frozen         bool           `json:"frozen"`
	Nodes          []ScenarioNode `json:"nodes"`
	Edges          []ScenarioEdge `json:"edges"`
	Groups         []CorrGroup    `json:"groups"`
	Pairs          []CorrPair     `json:"pairs"`
	CoverageP      float64        `json:"coverage_p"`
	CreatedAt      string         `json:"created_at"`
	FrozenAt       string         `json:"frozen_at,omitempty"`
	LatestResultID string         `json:"latest_result_id,omitempty"`
	SourceOf       string         `json:"source_of,omitempty"` // copied-from scenario id
}

// Certificate is a calibration certificate. Certificates are immutable while
// active; replacement adds a new revision pointing at the previous one.
type Certificate struct {
	ID              string  `json:"id"`
	Revision        int     `json:"revision"`
	Supersedes      string  `json:"supersedes,omitempty"`
	Slope           float64 `json:"slope"`
	Intercept       float64 `json:"intercept"`
	SlopeU          float64 `json:"slope_u"`
	InterceptU      float64 `json:"intercept_u"`
	SlopeInterceptR float64 `json:"slope_intercept_r"`
	Nu              float64 `json:"nu"`
	ValidFrom       string  `json:"valid_from"`
	ValidUntil      string  `json:"valid_until"`
	RevokedAt       string  `json:"revoked_at,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// Result is one derived calculation outcome (separate collection from inputs).
type Result struct {
	ID           string         `json:"id"`
	ScenarioID   string         `json:"scenario_id"`
	Status       string         `json:"status"` // ok | pending
	Confirmed    bool           `json:"confirmed"`
	Spec         engine.Spec    `json:"spec"`
	Output       *engine.Output `json:"output,omitempty"`
	TargetNodeID string         `json:"target_node_id,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	CreatedAt    string         `json:"created_at"`
	AuditDigest  string         `json:"audit_digest"`
}

// Event is one recorded operation (separate append-only collection).
type Event struct {
	Seq       int64          `json:"seq"`
	Event     string         `json:"event"`
	ID        string         `json:"id"`
	RequestID string         `json:"request_id,omitempty"`
	Time      string         `json:"time"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// AffectedResult pairs a result with the reason it is impacted by a change.
type AffectedResult struct {
	ResultID   string `json:"result_id"`
	ScenarioID string `json:"scenario_id"`
	Status     string `json:"status"`
	Frozen     bool   `json:"frozen"`
	Reason     string `json:"reason"`
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

type batchIndex struct {
	ResultIDs []string `json:"result_ids"`
}
