package importer

import (
	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// promoteStaged registers every staged artifact across every tool on one
// rewrite.SafeRenamePromoter and promotes them as a single all-or-nothing
// batch: a failure partway through rolls back every already-promoted
// rename, restoring pre-import content across every tool, not just the one
// that failed.
func promoteStaged(stagedSet *archive.StagedSet) error {
	promoter := rewrite.NewSafeRenamePromoter()
	for _, staged := range stagedSet.All() {
		promoter.StageFile(staged.Temp, staged.Final)
	}
	return promoter.Promote()
}
