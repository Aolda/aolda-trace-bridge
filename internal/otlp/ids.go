package otlp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func TraceIDFromBaseID(baseID string) ([]byte, error) {
	if raw, err := parseUUID(baseID); err == nil {
		return raw, nil
	}
	sum := sha256.Sum256([]byte(baseID))
	return nonZero(sum[:16]), nil
}

func SpanIDFromString(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return nonZero(sum[:8])
}

func parseUUID(value string) ([]byte, error) {
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return nil, fmt.Errorf("uuid must have 32 hex chars")
	}
	raw, err := hex.DecodeString(compact)
	if err != nil {
		return nil, err
	}
	if len(raw) != 16 {
		return nil, fmt.Errorf("uuid decoded to %d bytes", len(raw))
	}
	return nonZero(raw), nil
}

func nonZero(raw []byte) []byte {
	out := append([]byte(nil), raw...)
	for _, b := range out {
		if b != 0 {
			return out
		}
	}
	if len(out) > 0 {
		out[len(out)-1] = 1
	}
	return out
}
