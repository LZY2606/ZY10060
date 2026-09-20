package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// specForHash strips clock-dependent fields so identical inputs hash alike.
func specForHash(s Spec) Spec {
	c := s
	c.Now = ""
	return c
}

func checksumBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// MarshalCanonical returns deterministic JSON (sorted object keys).
func MarshalCanonical(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tmp any
	if err := json.Unmarshal(b, &tmp); err != nil {
		return nil, err
	}
	return json.MarshalIndent(tmp, "", "  ")
}
