package archive

import (
	"bytes"
	"context"
)

// ClassifyPresentKeys walks every entry, opening each body behind the shared
// per-entry and aggregate caps, and returns the subset of
// candidateKeys that appears as a literal substring in at least one body.
// No body is retained after inspection, so peak memory is bounded by one
// entry, not by the archive's total size.
func ClassifyPresentKeys(ctx context.Context, entries []RawEntry, candidateKeys []string, maxAggregateBytes int64) (map[string]struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	present := make(map[string]struct{})
	aggregate := NewAggregateCounter(maxAggregateBytes)
	for _, raw := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := raw.Entry.WithAggregateCounter(aggregate).ReadAll()
		if err != nil {
			return nil, err
		}
		for _, key := range candidateKeys {
			if _, already := present[key]; already {
				continue
			}
			if bytes.Contains(data, []byte(key)) {
				present[key] = struct{}{}
			}
		}
	}
	// An empty (or already-filtered-to-empty) entries list makes the loop
	// above a no-op, so a canceled context must still be surfaced here
	// rather than the caller receiving a plausible-looking empty result.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return present, nil
}
