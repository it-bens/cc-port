package archive

import "bytes"

// ClassifyPresentKeys walks every entry, opening each body behind the shared
// per-entry and aggregate caps, and returns the subset of
// candidateKeys that appears as a literal substring in at least one body.
// No body is retained after inspection, so peak memory is bounded by one
// entry, not by the archive's total size.
func ClassifyPresentKeys(entries []RawEntry, candidateKeys []string, maxAggregateBytes int64) (map[string]struct{}, error) {
	present := make(map[string]struct{})
	aggregate := NewAggregateCounter(maxAggregateBytes)
	for _, raw := range entries {
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
	return present, nil
}
