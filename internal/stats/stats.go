// Package stats computes a project's footprint in ~/.claude: how many times its
// path is referenced across shared files, and how much disk its owned data uses.
// It is read-only and reuses the claude enumeration and rewrite count primitives
// rather than the move dry-run, which counts a rename's replacements instead.
package stats

import (
	"context"
	"fmt"
	"sort"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
)

// DiskUsage is the file count and total byte size of one category's owned data.
type DiskUsage struct {
	Category string `json:"category"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"bytes"`
}

// ReferenceCount is the path-reference occurrence count for one shared surface.
type ReferenceCount struct {
	Surface string `json:"surface"`
	Count   int    `json:"count"`
}

// Footprint is a single project's full footprint: per-surface path-reference
// occurrence counts (the "what would a move touch" lens), per-category disk
// usage, and the structured counts LocateProject already computed.
type Footprint struct {
	ProjectPath       string           `json:"projectPath"`
	ProjectDir        string           `json:"projectDir"`
	References        []ReferenceCount `json:"references"`
	ReferenceTotal    int              `json:"referenceTotal"`
	Disk              []DiskUsage      `json:"disk"`
	DiskFiles         int              `json:"diskFiles"`
	DiskBytes         int64            `json:"diskBytes"`
	HistoryEntryCount int              `json:"historyEntryCount"`
	SessionFileCount  int              `json:"sessionFileCount"`
}

// ProjectFootprint is one project's disk footprint in all-projects mode: a
// display label (the witness-resolved real path, or the encoded directory name
// when no witness exists), per-category disk usage, and the byte total the
// ranking sorts on.
type ProjectFootprint struct {
	Label      string      `json:"label"`
	EncodedDir string      `json:"encodedDir"`
	Resolved   bool        `json:"resolved"`
	Disk       []DiskUsage `json:"disk"`
	Files      int         `json:"files"`
	Bytes      int64       `json:"bytes"`
}

// ComputeFootprint reports the full footprint of a single project. It surfaces
// LocateProject's not-found and identity-mismatch errors verbatim — no
// zero-footprint result is fabricated for a project that cannot be located.
func ComputeFootprint(ctx context.Context, claudeHome *claude.Home, projectPath string) (*Footprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	locations, err := claude.LocateProject(claudeHome, projectPath)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	references, err := countReferences(ctx, claudeHome, locations)
	if err != nil {
		return nil, err
	}

	diskByCategory, err := computeDisk(ctx, locations)
	if err != nil {
		return nil, err
	}

	footprint := &Footprint{
		ProjectPath:       locations.ProjectPath,
		ProjectDir:        locations.ProjectDir,
		References:        orderReferences(references),
		Disk:              orderDisk(diskByCategory),
		HistoryEntryCount: locations.HistoryEntryCount,
		SessionFileCount:  len(locations.SessionFiles),
	}
	for _, reference := range footprint.References {
		footprint.ReferenceTotal += reference.Count
	}
	for _, usage := range footprint.Disk {
		footprint.DiskFiles += usage.Files
		footprint.DiskBytes += usage.Bytes
	}
	return footprint, nil
}

// ComputeAllFootprints reports every project's disk footprint, ranked by total
// bytes descending. References are out of scope here (see the package doc and
// claude.EnumerateProjects): an arbitrary encoded directory has no confirmed
// real path to scan shared files for.
func ComputeAllFootprints(ctx context.Context, claudeHome *claude.Home) ([]ProjectFootprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	enumerations, err := claude.EnumerateProjects(claudeHome)
	if err != nil {
		return nil, fmt.Errorf("enumerate projects: %w", err)
	}

	footprints := make([]ProjectFootprint, 0, len(enumerations))
	for _, enumeration := range enumerations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		diskByCategory, err := computeDisk(ctx, enumeration.Locations)
		if err != nil {
			return nil, err
		}

		footprint := ProjectFootprint{
			Label:      enumeration.ResolvedPath,
			EncodedDir: enumeration.EncodedName,
			Resolved:   enumeration.ResolvedPath != "",
			Disk:       orderDisk(diskByCategory),
		}
		if footprint.Label == "" {
			footprint.Label = enumeration.EncodedName
		}
		for _, usage := range footprint.Disk {
			footprint.Files += usage.Files
			footprint.Bytes += usage.Bytes
		}
		footprints = append(footprints, footprint)
	}

	// Rank by bytes descending; tie-break on label so the order is stable
	// regardless of directory-read order.
	sort.SliceStable(footprints, func(first, second int) bool {
		if footprints[first].Bytes != footprints[second].Bytes {
			return footprints[first].Bytes > footprints[second].Bytes
		}
		return footprints[first].Label < footprints[second].Label
	})
	return footprints, nil
}

// orderDisk projects the per-category size map onto manifest.AllCategories
// order, emitting every category (history and config included at zero) so the
// result always carries the full registry in its canonical order.
func orderDisk(byCategory map[string]DiskUsage) []DiskUsage {
	ordered := make([]DiskUsage, 0, len(manifest.AllCategories))
	for _, spec := range manifest.AllCategories {
		usage := byCategory[spec.Name]
		usage.Category = spec.Name
		ordered = append(ordered, usage)
	}
	return ordered
}

// orderReferences projects the per-surface count map onto referenceSurfaces
// order, emitting every surface (zero counts included) so the result is
// deterministic and complete.
func orderReferences(bySurface map[string]int) []ReferenceCount {
	surfaces := referenceSurfaces()
	ordered := make([]ReferenceCount, 0, len(surfaces))
	for _, surface := range surfaces {
		ordered = append(ordered, ReferenceCount{Surface: surface, Count: bySurface[surface]})
	}
	return ordered
}
