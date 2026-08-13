package api

import (
	"crypto/sha256"
	"fmt"

	"google.golang.org/protobuf/proto"
)

// canonicalHash returns the sha256 of the deterministic serialization of a
// request with its idempotency key cleared. This is the "canonical request
// hash" the envelope compares: same key + same hash → original
// operation; same key + different hash → FAILED_PRECONDITION.
//
// Determinism note: proto.MarshalOptions{Deterministic: true} guarantees
// stable map ordering within one binary version, which is sufficient here —
// the hash never crosses binaries without the API version string that is
// also stored in the envelope.
func canonicalHash(req proto.Message, clearKey func(m proto.Message)) ([]byte, error) {
	clone := proto.Clone(req)
	clearKey(clone)
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("canonical marshal: %w", err)
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}
