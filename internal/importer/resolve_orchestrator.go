package importer

import (
	"errors"

	"github.com/it-bens/cc-port/internal/manifest"
)

// Prompter is the interactive UX seam. The cmd layer constructs one that
// delegates to internal/ui; internal/importer never imports a UI library.
// The prompter receives only keys the orchestrator could not resolve
// from manifest known values, and never sees implicit keys.
type Prompter func(unresolved []string) (map[string]string, error)

// ResolvePlaceholders takes the plan's raw unresolved placeholder keys
// (callers do not pre-filter), optional manifest metadata from
// --from-manifest, and a prompter. It returns a fully-merged resolution
// map ready for stage execution.
//
// The orchestrator owns three rules:
//   - implicit keys (see IsImplicitKey) are filtered out of the working
//     set before any merge or prompt;
//   - manifest-derived non-empty Resolve values are merged first;
//   - remaining unresolved keys go to the prompter.
//
// The prompter never sees implicit keys; the returned map contains only
// caller-resolvable values.
func ResolvePlaceholders(
	unresolved []string,
	fromManifest *manifest.Metadata,
	prompter Prompter,
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

	if len(stillUnresolved) == 0 {
		return resolutions, nil
	}
	if prompter == nil {
		return nil, errors.New(
			"importer.ResolvePlaceholders: prompter required for unresolved keys")
	}

	prompted, err := prompter(stillUnresolved)
	if err != nil {
		return nil, err
	}
	for key, value := range prompted {
		resolutions[key] = value
	}
	return resolutions, nil
}
