package codex

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// Depended-on queued_items columns (state/queue_migrations/0001_queued_items.sql).
// payload_json is serde_json of a queued TurnInput (ext/queue/src/service.rs),
// an externally tagged enum whose UserInput variant holds a content array of
// internally tagged UserInput items (protocol/src/turn_input.rs,
// protocol/src/user_input.rs).
const (
	queuedItemsTable         = "queued_items"
	queuedItemsIDColumn      = "id"
	queuedItemsPayloadColumn = "payload_json"
	turnInputContentPath     = "UserInput.content"
	userInputSkillType       = "skill"
	userInputMentionType     = "mention"
	skillFileName            = "SKILL.md"
)

// queuedItemRewrite is one queued_items row whose payload carries skill paths
// under the moved project: the payload the plan read, the payload Apply
// writes, and how many skill paths the rewrite changes.
type queuedItemRewrite struct {
	id         string
	oldPayload string
	newPayload string
	pathCount  int
}

// queueDBRewritePlans maps each discovered queue_*.sqlite path to its rows.
type queueDBRewritePlans map[string][]queuedItemRewrite

// valueCount is the queue-db surface's count: every skill path the captured
// plan rewrites. Apply writes each planned row or fails, so a
// successful Apply reports the same count.
func (plans queueDBRewritePlans) valueCount() int {
	total := 0
	for _, rewrites := range plans {
		for _, itemRewrite := range rewrites {
			total += itemRewrite.pathCount
		}
	}
	return total
}

func queueDBRewritePlansForProject(ctx context.Context, sqliteDir, oldPath, newPath string) (queueDBRewritePlans, error) {
	paths, err := discoverDatabases(sqliteDir, queueDBGlob)
	if err != nil {
		return nil, err
	}
	plans := make(queueDBRewritePlans, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rewrites, err := matchingQueuedItems(ctx, path, oldPath, newPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		plans[path] = rewrites
	}
	return plans, nil
}

// matchingQueuedItems is the queue scan the plan captures: every
// queued_items row, ordered by id, with at least one skill path that is
// oldPath or a path-boundary descendant of it.
func matchingQueuedItems(ctx context.Context, path, oldPath, newPath string) ([]queuedItemRewrite, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()

	if err := sqlrewrite.RequirePrimaryKeyAndColumns(database, queuedItemsTable, queuedItemsIDColumn, queuedItemsPayloadColumn); err != nil {
		return nil, err
	}
	// #nosec G201 -- table and column names are adapter constants, not user input.
	query := fmt.Sprintf(`SELECT %q, %q FROM %q ORDER BY %q`,
		queuedItemsIDColumn, queuedItemsPayloadColumn, queuedItemsTable, queuedItemsIDColumn)
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read queued_items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var rewrites []queuedItemRewrite
	for rows.Next() {
		var id, payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, fmt.Errorf("read queued_items row: %w", err)
		}
		newPayload, pathCount, err := rewriteQueuedSkillPaths(payload, oldPath, newPath)
		if err != nil {
			return nil, fmt.Errorf("queued_items %s: %w", id, err)
		}
		if pathCount > 0 {
			rewrites = append(rewrites, queuedItemRewrite{id: id, oldPayload: payload, newPayload: newPayload, pathCount: pathCount})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read queued_items: %w", err)
	}
	return rewrites, nil
}

// rewriteQueuedSkillPaths rewrites every skill path in payload that is
// oldPath or a path-boundary descendant of it: each UserInput::Skill path, and
// each UserInput::Mention path Codex treats as a skill because its file name
// is SKILL.md. Text items and every other Mention are left alone: see README
// §Queue database.
func rewriteQueuedSkillPaths(payload, oldPath, newPath string) (rewritten string, pathCount int, err error) {
	if !gjson.Valid(payload) {
		return "", 0, errors.New("payload_json is not valid JSON")
	}
	// The queue holds only TurnInput::UserInput, whose content array is
	// required (protocol/src/turn_input.rs:32-36, ext/queue/src/service.rs:493-499).
	content := gjson.Get(payload, turnInputContentPath)
	if !content.IsArray() {
		return "", 0, errors.New("payload_json has no UserInput.content array")
	}
	rewritten = payload
	for index, item := range content.Array() {
		itemType := item.Get("type").String()
		if itemType != userInputSkillType && itemType != userInputMentionType {
			continue
		}
		itemPath := item.Get("path")
		if itemPath.Type != gjson.String {
			return "", 0, fmt.Errorf("%s item %d has a non-string path", itemType, index)
		}
		value := itemPath.String()
		if itemType == userInputMentionType && !hasSkillFileName(value) {
			continue
		}
		newValue, ok := rewrite.ReplaceBoundedPrefix(value, oldPath, newPath)
		if !ok {
			continue
		}
		fieldPath := turnInputContentPath + "." + strconv.Itoa(index) + ".path"
		rewritten, err = sjson.Set(rewritten, fieldPath, newValue)
		if err != nil {
			return "", 0, fmt.Errorf("set %s: %w", fieldPath, err)
		}
		pathCount++
	}
	return rewritten, pathCount, nil
}

// hasSkillFileName reports whether path's last `/`- or `\`-separated
// segment is SKILL.md, ignoring ASCII case: the file-name half of upstream's
// path_is_skill (ext/skills/src/selection.rs:116-122).
func hasSkillFileName(path string) bool {
	name := path[strings.LastIndexAny(path, `/\`)+1:]
	// Equal byte lengths confine strings.EqualFold to ASCII folding, as
	// upstream's eq_ignore_ascii_case: a multi-byte rune that folds to an
	// ASCII letter (U+017F to s, U+212A to k) changes the byte length.
	return len(name) == len(skillFileName) && strings.EqualFold(name, skillFileName)
}

// startQueueDBRewritesWithPlan applies plans, captured in MoveSurfaces'
// preflight, to the queue databases.
func startQueueDBRewritesWithPlan(
	ctx context.Context, sqliteDir, oldPath, newPath string, plans queueDBRewritePlans, undo *tool.Restorer,
) (databaseRewrites, int, error) {
	planned := &plannedDatabases{surface: "queue", paths: slices.Collect(maps.Keys(plans))}
	return startDatabaseRewrites(ctx, sqliteDir, queueDBGlob, planned, oldPath, newPath,
		func(ctx context.Context, path string, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, _, _ string) (int, error) {
			return rewriteQueuedItemsWithPlan(ctx, database, transaction, plans[path])
		}, undo)
}

// rewriteQueuedItemsWithPlan writes each planned payload by id, only while
// the row still holds the payload the plan read. A planned row that updates
// nothing fails the apply: the queue database changed after the plan.
func rewriteQueuedItemsWithPlan(
	ctx context.Context, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, rewrites []queuedItemRewrite,
) (int, error) {
	total := 0
	for _, itemRewrite := range rewrites {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		updated, err := database.UpdateColumnsByKey(transaction, queuedItemsTable, queuedItemsIDColumn, itemRewrite.id,
			map[string]any{queuedItemsPayloadColumn: itemRewrite.newPayload},
			map[string]any{queuedItemsPayloadColumn: itemRewrite.oldPayload})
		if err != nil {
			return 0, fmt.Errorf("rewrite queued_items.payload_json for id %s: %w", itemRewrite.id, err)
		}
		if updated == 0 {
			return 0, fmt.Errorf(
				"rewrite queued_items.payload_json for id %s: queue database changed after the plan: no row with that id holds the planned payload any more",
				itemRewrite.id,
			)
		}
		total += itemRewrite.pathCount
	}
	return total, nil
}
