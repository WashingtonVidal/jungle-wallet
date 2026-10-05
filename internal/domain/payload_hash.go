package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalPayloadHash sorts business object keys without interpreting monetary
// strings as numeric values. legacy preserves the earlier fixed-field encoding.
func CanonicalPayloadHash(raw []byte) (canonical, legacy string, err error) {
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return "", "", err
	}
	ordered, err := json.Marshal(fields)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(ordered)
	prior := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), hex.EncodeToString(prior[:]), nil
}
