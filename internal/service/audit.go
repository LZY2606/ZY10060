package service

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"metrolab/internal/engine"
	"sort"
	"time"
)

// BundleManifest is metrolab-bundle.json at the root of the audit tar.
type BundleManifest struct {
	BundleVersion string            `json:"bundle_version"`
	CreatedAt     string            `json:"created_at"`
	RuleVersions  map[string]string `json:"rule_versions"`
	Files         []BundleFile      `json:"files"`
}

type BundleFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

const BundleVersion = "metrolab-bundle-v1"

// ExportAudit builds a deterministic tar stream covering one result and all
// inputs/rules needed to reproduce it.
func (s *Service) ExportAudit(resultID string) (io.Reader, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.results[resultID]
	if r == nil {
		return nil, invalid("not_found", "no such result: "+resultID)
	}
	sc := s.scenarios[r.ScenarioID]
	if sc == nil {
		return nil, internalErr("result missing scenario")
	}
	var files []bundleEntry
	add := func(path string, v any) *Error {
		b, err := engine.MarshalCanonical(v)
		if err != nil {
			return internalErr(err.Error())
		}
		files = append(files, bundleEntry{path: path, data: b})
		return nil
	}
	if e := add("inputs/observations.json", r.Spec.Leaves); e != nil {
		return nil, e
	}
	if e := add("inputs/certificates.json", r.Spec.Certificates); e != nil {
		return nil, e
	}
	if e := add("inputs/scenario.json", sc); e != nil {
		return nil, e
	}
	if e := add("rules/versions.json", r.Output.RuleVersions); e != nil {
		return nil, e
	}
	paths := r.Output.UnitPaths
	if e := add("work/unit_paths.json", paths); e != nil {
		return nil, e
	}
	if e := add("work/spec.json", r.Spec); e != nil {
		return nil, e
	}
	if e := add("result/output.json", r.Output); e != nil {
		return nil, e
	}
	checks := map[string]string{
		"spec_sha256": r.Output.ChecksumInputs,
		"bundle":      BundleVersion,
	}
	if e := add("checks/hashes.json", checks); e != nil {
		return nil, e
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	var mfiles []BundleFile
	for _, f := range files {
		h := sha256.Sum256(f.data)
		mfiles = append(mfiles, BundleFile{Path: f.path, SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(f.data))})
	}
	manifest := BundleManifest{BundleVersion: BundleVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		RuleVersions: r.Output.RuleVersions, Files: mfiles}
	mb, err := engine.MarshalCanonical(manifest)
	if err != nil {
		return nil, internalErr(err.Error())
	}
	files = append(files, bundleEntry{path: "metrolab-bundle.json", data: mb})

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		hdr := &tar.Header{Name: f.path, Mode: 0o644, Size: int64(len(f.data)), Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, internalErr(err.Error())
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, internalErr(err.Error())
		}
	}
	if err := tw.Close(); err != nil {
		return nil, internalErr(err.Error())
	}
	return &buf, nil
}

type bundleEntry struct {
	path string
	data []byte
}

// ImportReport summarizes a re-import verification.
type ImportReport struct {
	SpecChecksumOK bool          `json:"spec_checksum_ok"`
	SpecChecksum   string        `json:"spec_checksum"`
	NumericDiffs   []NumericDiff `json:"numeric_diffs"`
	AllMatch       bool          `json:"all_match"`
	ResultID       string        `json:"result_id"`
	Stored         bool          `json:"stored"`
	FileHashes     []BundleFile  `json:"file_hashes"`
	HashOK         bool          `json:"hash_ok"`
}

type NumericDiff struct {
	Node   string  `json:"node"`
	Field  string  `json:"field"`
	Expect float64 `json:"expect"`
	Got    float64 `json:"got"`
	AbsErr float64 `json:"abs_err"`
}

// ImportAudit verifies a bundle: file hashes, spec checksum, and reruns the
// engine comparing every numeric field. When store=true the reproduced result
// is persisted; a repeated import of the same bundle is idempotent.
func (s *Service) ImportAudit(r io.Reader, storeResult bool, requestID string) (*ImportReport, *Error) {
	content := map[string][]byte{}
	tr := tar.NewReader(r)
	var manifestBytes []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalid("bad_bundle", "tar read failed: "+err.Error())
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, invalid("bad_bundle", "tar member read failed")
		}
		content[hdr.Name] = b
		if hdr.Name == "metrolab-bundle.json" {
			manifestBytes = b
		}
	}
	if manifestBytes == nil {
		return nil, invalid("bad_bundle", "manifest missing")
	}
	var manifest BundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, invalid("bad_bundle", "manifest invalid: "+err.Error())
	}
	rep := &ImportReport{HashOK: true}
	for _, mf := range manifest.Files {
		b, ok := content[mf.Path]
		if !ok {
			return nil, invalid("bad_bundle", "missing file: "+mf.Path)
		}
		h := sha256.Sum256(b)
		got := hex.EncodeToString(h[:])
		rep.FileHashes = append(rep.FileHashes, mf)
		if got != mf.SHA256 {
			rep.HashOK = false
			return nil, invalid("hash_mismatch", "sha256 mismatch for "+mf.Path)
		}
	}
	var spec engine.Spec
	if e := json.Unmarshal(content["work/spec.json"], &spec); e != nil {
		return nil, invalid("bad_bundle", "spec invalid: "+e.Error())
	}
	var oldOut engine.Output
	if e := json.Unmarshal(content["result/output.json"], &oldOut); e != nil {
		return nil, invalid("bad_bundle", "output invalid: "+e.Error())
	}
	out, probs := engine.Run(spec, oldOut.Status == "pending")
	if probs != nil {
		return nil, invalid("recompute_rejected", "re-imported spec fails validation", probs...)
	}
	rep.SpecChecksum = specChecksum(spec)
	rep.SpecChecksumOK = rep.SpecChecksum == oldOut.ChecksumInputs
	const tol = 1e-12
	compare := func(node, field string, want, got float64) {
		if mathIsNaN(want) || mathIsNaN(got) {
			if mathIsNaN(want) != mathIsNaN(got) {
				rep.NumericDiffs = append(rep.NumericDiffs, NumericDiff{node, field, want, got, math.Abs(want - got)})
			}
			return
		}
		if math.Abs(want-got) > tol*math.Max(1, math.Abs(want)) {
			rep.NumericDiffs = append(rep.NumericDiffs, NumericDiff{node, field, want, got, math.Abs(want - got)})
		}
	}
	ids := append([]string{}, oldOut.Order...)
	for _, id := range ids {
		a, b := oldOut.Results[id], out.Results[id]
		compare(id, "display_value", a.DisplayValue, b.DisplayValue)
		compare(id, "std_uncertainty", a.DisplayU, b.DisplayU)
		compare(id, "k", a.K, b.K)
		compare(id, "interval_low", a.IntervalLow, b.IntervalLow)
		compare(id, "interval_high", a.IntervalHigh, b.IntervalHigh)
	}
	rep.AllMatch = rep.SpecChecksumOK && len(rep.NumericDiffs) == 0
	if storeResult {
		if !rep.AllMatch {
			return rep, conflict("import_verification_failed", "refusing to store: hash or numeric mismatch")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		key := "import:" + rep.SpecChecksum
		if existingID, ok := s.idem[key]; ok {
			rep.Stored = true
			rep.ResultID = existingID
			return rep, nil
		}
		var sc Scenario
		_ = json.Unmarshal(content["inputs/scenario.json"], &sc)
		newID := s.newID("scn")
		sc.ID = newID
		sc.Frozen = true
		sc.CreatedAt = nowUTC()
		sc.Name = "imported:" + newID
		sc.LatestResultID = ""
		if _, e := s.emit("scenario.put", newID, "", map[string]any{"object": sc}); e != nil {
			return nil, e
		}
		rid := s.newID("res")
		r := &Result{ID: rid, ScenarioID: newID, Spec: spec, Output: out, Status: out.Status,
			Confirmed: true, CreatedAt: nowUTC(), AuditDigest: rep.SpecChecksum}
		if _, e := s.emit("result.put", rid, requestID, map[string]any{"object": r, "imported": true}); e != nil {
			return nil, e
		}
		s.idem[key] = rid
		rep.Stored = true
		rep.ResultID = rid
	}
	return rep, nil
}

func specChecksum(spec engine.Spec) string {
	b, _ := engine.MarshalCanonical(spec)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// DiffResults compares two results node by node.
type ResultDiff struct {
	A               string              `json:"a"`
	B               string              `json:"b"`
	Nodes           []NodeDiff          `json:"nodes"`
	ContributionTop map[string][]string `json:"contribution_top,omitempty"`
}

type NodeDiff struct {
	Node    string  `json:"node"`
	ValueA  float64 `json:"value_a"`
	ValueB  float64 `json:"value_b"`
	Delta   float64 `json:"delta"`
	UA      float64 `json:"u_a"`
	UB      float64 `json:"u_b"`
	StatusA string  `json:"status_a,omitempty"`
	StatusB string  `json:"status_b,omitempty"`
}

func (s *Service) DiffResults(aID, bID string) (*ResultDiff, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ra, rb := s.results[aID], s.results[bID]
	if ra == nil || rb == nil {
		return nil, invalid("not_found", fmt.Sprintf("result %s or %s missing", aID, bID))
	}
	d := &ResultDiff{A: aID, B: bID}
	seen := map[string]bool{}
	for _, id := range ra.Output.Order {
		if _, ok := rb.Output.Results[id]; !ok {
			continue
		}
		na, nb := ra.Output.Results[id], rb.Output.Results[id]
		if na.Kind == "leaf" {
			continue
		}
		d.Nodes = append(d.Nodes, NodeDiff{Node: id, ValueA: na.DisplayValue, ValueB: nb.DisplayValue,
			Delta: nb.DisplayValue - na.DisplayValue, UA: na.DisplayU, UB: nb.DisplayU,
			StatusA: ra.Status, StatusB: rb.Status})
		seen[id] = true
	}
	sort.Slice(d.Nodes, func(i, j int) bool { return d.Nodes[i].Node < d.Nodes[j].Node })
	return d, nil
}
