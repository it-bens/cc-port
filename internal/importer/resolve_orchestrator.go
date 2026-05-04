package importer

import (
	"sort"

	"github.com/it-bens/cc-port/internal/manifest"
)

// ResolvePlaceholders takes the plan's raw unresolved placeholder keys
// (callers do not pre-filter) and optional manifest metadata from
// --from-manifest. It returns a fully-merged resolution map.
//
// Rules:
//   - implicit keys (see IsImplicitKey) are filtered from the working
//     set before any merge;
//   - manifest-derived non-empty Resolve values are merged for
//     non-implicit keys;
//   - any non-implicit key still unresolved produces a
//     MissingResolutionsError.
func ResolvePlaceholders(
	unresolved []string,
	fromManifest *manifest.Metadata,
) (map[string]string, error) {
	resolutions := make(map[string]string, len(unresolved))

	if fromManifest != nil {
		for _, placeholder := range fromManifest.Placeholders {
			if IsImplicitKey(placeholder.Key) {
				continue
			}
			if placeholder.Resolve == "" {
				continue
			}
			resolutions[placeholder.Key] = placeholder.Resolve
		}
	}

	var stillUnresolved []string
	for _, key := range unresolved {
		if IsImplicitKey(key) {
			continue
		}
		if _, ok := resolutions[key]; ok {
			continue
		}
		stillUnresolved = append(stillUnresolved, key)
	}

	if len(stillUnresolved) > 0 {
		sort.Strings(stillUnresolved)
		return nil, &MissingResolutionsError{Keys: stillUnresolved}
	}
	return resolutions, nil
}
