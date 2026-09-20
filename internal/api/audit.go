package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"metrologylab/internal/domain"
)

type AuditBundle struct {
	BundleType     string                         `json:"bundleType"`
	BundleVersion  string                         `json:"bundleVersion"`
	SchemaVersion  string                         `json:"schemaVersion"`
	RuleSetVersion string                         `json:"ruleSetVersion"`
	GeneratedAt    time.Time                      `json:"generatedAt"`
	Inputs         AuditInputs                    `json:"inputs"`
	FrozenResults  map[string]domain.FrozenResult `json:"frozenResults"`
	Events         []domain.Event                 `json:"events"`
	Files          map[string]string              `json:"files"`
	Checksum       string                         `json:"checksum"`
}
type AuditInputs struct {
	Observations  map[string]domain.Observation      `json:"observations"`
	Groups        map[string]domain.CorrelationGroup `json:"groups"`
	Certificates  map[string]domain.Certificate      `json:"certificates"`
	Scenarios     map[string]domain.Scenario         `json:"scenarios"`
	ActiveResults map[string]domain.Result           `json:"activeResults"`
}

func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ScenarioIDs []string `json:"scenarioIds"`
	}
	if body, ok := requestBody(r.Context()); ok && len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}
	st := s.st.State()
	ids := req.ScenarioIDs
	if len(ids) == 0 {
		for id := range st.Scenarios {
			ids = append(ids, id)
		}
	}
	b := AuditBundle{BundleType: "metrology-lab-audit", BundleVersion: "audit/v1", SchemaVersion: domain.SchemaVersion, RuleSetVersion: domain.RuleSetVersion, GeneratedAt: s.now().UTC(), Inputs: AuditInputs{Observations: map[string]domain.Observation{}, Groups: map[string]domain.CorrelationGroup{}, Certificates: map[string]domain.Certificate{}, Scenarios: map[string]domain.Scenario{}, ActiveResults: map[string]domain.Result{}}, FrozenResults: map[string]domain.FrozenResult{}, Events: []domain.Event{}}
	for _, id := range ids {
		if sc, ok := st.Scenarios[id]; ok {
			b.Inputs.Scenarios[id] = sc
			b.Inputs.ActiveResults[id] = st.Results[id]
			if sc.FrozenResultID != "" {
				b.FrozenResults[sc.FrozenResultID] = st.FrozenResults[sc.FrozenResultID]
			}
			for _, n := range sc.Nodes {
				if n.Type == "observation" {
					oid := n.ObservationID
					if o, ok := st.Observations[oid]; ok {
						b.Inputs.Observations[oid] = o
						if o.CorrelationGroupID != "" {
							b.Inputs.Groups[o.CorrelationGroupID] = st.Groups[o.CorrelationGroupID]
						}
					}
					if n.CertificateID != "" {
						if c, ok := st.Certificates[n.CertificateID]; ok {
							b.Inputs.Certificates[n.CertificateID] = c
						}
					}
				}
			}
		}
	}
	b.Events = eventsFor(st, ids)
	b.Files = fileHashes(b)
	sum, err := canonicalHash(withoutChecksum(b))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	b.Checksum = sum
	writeJSON(w, 200, b)
}

func (s *Server) importAudit(w http.ResponseWriter, r *http.Request) {
	var b AuditBundle
	if !decode(w, r, &b) {
		return
	}
	if err := verifyBundle(b); err != nil {
		writeError(w, 400, &ValidationErrors{[]domain.Issue{{Code: "AUDIT_CHECKSUM_MISMATCH", Message: err.Error()}}})
		return
	}
	body, code, err := s.mutate(r, "audit.imported", "/api/audits/import", map[string]any{"checksum": b.Checksum, "bundleVersion": b.BundleVersion}, func(st *domain.State) ([]byte, error) {
		conflicts := []string{}
		for id := range b.Inputs.Observations {
			if _, ok := st.Observations[id]; ok {
				conflicts = append(conflicts, id)
			}
		}
		if len(conflicts) > 0 {
			return nil, &ConflictError{Code: "AUDIT_IMPORT_CONFLICT", Message: "audit package contains existing resource ids", Body: conflicts}
		}
		for k, v := range b.Inputs.Observations {
			st.Observations[k] = v
		}
		for k, v := range b.Inputs.Groups {
			st.Groups[k] = v
		}
		for k, v := range b.Inputs.Certificates {
			st.Certificates[k] = v
		}
		for k, v := range b.Inputs.Scenarios {
			st.Scenarios[k] = v
		}
		for k, v := range b.Inputs.ActiveResults {
			st.Results[k] = v
		}
		for k, v := range b.FrozenResults {
			st.FrozenResults[k] = v
		}
		return jsonMust(map[string]any{"imported": true, "checksum": b.Checksum, "scenarioCount": len(b.Inputs.Scenarios), "frozenCount": len(b.FrozenResults)})
	})
	if err != nil {
		writeStoreError(w, code, err, nil)
		return
	}
	writeJSON(w, 200, raw(body))
}

func verifyBundle(b AuditBundle) error {
	if b.BundleType != "metrology-lab-audit" || b.BundleVersion != "audit/v1" {
		return errInvalid("unsupported audit bundle")
	}
	if b.RuleSetVersion != domain.RuleSetVersion {
		return errInvalid("rule set version mismatch")
	}
	got := fileHashes(b)
	for name, want := range b.Files {
		if got[name] != want {
			return errInvalid("file checksum mismatch: " + name)
		}
	}
	sum, err := canonicalHash(withoutChecksum(b))
	if err != nil {
		return err
	}
	if sum != b.Checksum {
		return errInvalid("audit checksum mismatch")
	}
	return nil
}
func withoutChecksum(b AuditBundle) AuditBundle {
	b.Files = map[string]string{}
	b.Checksum = ""
	return b
}
func fileHashes(b AuditBundle) map[string]string {
	files := map[string]string{}
	add := func(name string, v any) {
		sum, err := canonicalHash(v)
		if err == nil {
			files[name] = sum
		}
	}
	add("inputs/observations.json", b.Inputs.Observations)
	add("inputs/groups.json", b.Inputs.Groups)
	add("inputs/certificates.json", b.Inputs.Certificates)
	add("inputs/scenarios.json", b.Inputs.Scenarios)
	add("results/active.json", b.Inputs.ActiveResults)
	add("results/frozen.json", b.FrozenResults)
	add("events.json", b.Events)
	add("ruleset.json", map[string]string{domain.SchemaVersion: domain.RuleSetVersion})
	return files
}
func canonicalHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func eventsFor(st *domain.State, ids []string) []domain.Event { return []domain.Event{} }

type simpleError string

func (e simpleError) Error() string { return string(e) }
func errInvalid(s string) error     { return simpleError(s) }

var _ = bytes.NewBuffer
