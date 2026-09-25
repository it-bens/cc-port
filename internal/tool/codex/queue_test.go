package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/codex/codexschema"
)

const queuedThreadID = "00000000-0000-4000-8000-000000000001"

type queuedPayload struct {
	UserInput struct {
		Content []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			Path         string `json:"path"`
			TextElements []struct {
				ByteRange struct {
					Start int `json:"start"`
					End   int `json:"end"`
				} `json:"byte_range"`
			} `json:"text_elements"`
		} `json:"content"`
	} `json:"UserInput"`
}

// newQueueHome writes queue_1.sqlite with the upstream DDL and one
// queued_items row per payload, keyed by the map key.
func newQueueHome(t *testing.T, payloads map[string]string) *Home {
	t.Helper()
	dir := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(dir, codexschema.QueueDBFileName))
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	_, err = database.ExecContext(context.Background(), codexschema.QueueDBSchema)
	require.NoError(t, err)
	order := 0
	for id, payload := range payloads {
		_, err = database.ExecContext(context.Background(),
			`INSERT INTO queued_items (id, thread_id, payload_json, queue_order, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?, 0, 0)`,
			id, queuedThreadID, payload, order)
		require.NoError(t, err)
		order++
	}
	return &Home{Dir: dir, SQLiteDir: dir}
}

func readQueuedPayloadByID(t *testing.T, home *Home, id string) string {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.QueueDBFileName))
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	var payload string
	require.NoError(t, database.QueryRowContext(context.Background(),
		`SELECT payload_json FROM queued_items WHERE id = ?`, id).Scan(&payload))
	return payload
}

func decodeQueuedPayload(t *testing.T, payload string) queuedPayload {
	t.Helper()
	var decoded queuedPayload
	require.NoError(t, json.Unmarshal([]byte(payload), &decoded), "payload_json must stay valid JSON")
	return decoded
}

// applyQueueMove plans and applies the queue-db surface for req and commits it.
func applyQueueMove(t *testing.T, home *Home, req tool.MoveRequest) (planned, applied int) {
	t.Helper()
	plans, err := queueDBRewritePlansForProject(context.Background(), home.SQLiteDir, req.OldPath, req.NewPath)
	require.NoError(t, err)
	workspace := NewWorkspace(home, fakeGetenv(nil), noProcesses)
	pending := &pendingMoveDatabases{}
	surface := workspace.queueDBSurfaceWithPlans(req, pending, plans)
	undo := tool.NewRestorer()

	planResult, err := surface.Plan(context.Background())
	require.NoError(t, err)
	applyResult, err := surface.Apply(context.Background(), undo)
	require.NoError(t, err)
	_, err = pending.commitSurface().Apply(context.Background(), undo)
	require.NoError(t, err)
	undo.Cleanup()
	return planResult.Count, applyResult.Count
}

func TestQueueDBMoveRewritesSkillPath(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	planCounts, applyCounts := planAndApply(t, workspace, req)

	require.Equal(t, 2, planCounts["queue-db"], "the fixture's skill item and its SKILL.md mention item")
	assert.Equal(t, planCounts["queue-db"], applyCounts["queue-db"])
	payload := decodeQueuedPayload(t, readQueuedPayloadByID(t, home, fixtureQueuedItemID))
	require.Len(t, payload.UserInput.Content, 1)
	assert.Equal(t, "/Users/fixture/renamed-project/.codex/skills/deploy/SKILL.md", payload.UserInput.Content[0].Path)
}

func TestQueueDBMoveRewritesSkillFileMentionPath(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	planAndApply(t, workspace, req)

	payload := decodeQueuedPayload(t, readQueuedPayloadByID(t, home, fixtureQueuedMentionItemID))
	require.Len(t, payload.UserInput.Content, 1)
	assert.Equal(t, "/Users/fixture/renamed-project/.codex/skills/release/SKILL.md", payload.UserInput.Content[0].Path)
}

func TestQueueDBMoveMatchesMentionFileNameLikeCodex(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		mentionPath   string
		wantRewritten string
	}{
		{
			name:          "file name differing only in ASCII case is a skill",
			mentionPath:   "/Users/test/Projects/app/.codex/skills/deploy/skill.md",
			wantRewritten: "/Users/test/Projects/renamed/.codex/skills/deploy/skill.md",
		},
		{name: "file name other than SKILL.md is not a skill", mentionPath: "/Users/test/Projects/app/docs/notes.md"},
		{name: "Kelvin sign folds to k only outside ASCII", mentionPath: "/Users/test/Projects/app/.codex/skills/deploy/S\u212aILL.md"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			payload := `{"UserInput":{"content":[{"type":"mention","name":"deploy","path":"` + testCase.mentionPath + `"}],"client_id":"c"}}`
			home := newQueueHome(t, map[string]string{"mention-item": payload})
			req := tool.MoveRequest{OldPath: "/Users/test/Projects/app", NewPath: "/Users/test/Projects/renamed"}

			planned, applied := applyQueueMove(t, home, req)

			assert.Equal(t, planned, applied)
			if testCase.wantRewritten == "" {
				assert.Zero(t, planned)
				assert.JSONEq(t, payload, readQueuedPayloadByID(t, home, "mention-item"))
				return
			}
			assert.Equal(t, 1, planned)
			decoded := decodeQueuedPayload(t, readQueuedPayloadByID(t, home, "mention-item"))
			require.Len(t, decoded.UserInput.Content, 1)
			assert.Equal(t, testCase.wantRewritten, decoded.UserInput.Content[0].Path)
		})
	}
}

func TestQueueDBMoveRewritesSkillPathContainingQuote(t *testing.T) {
	home := newQueueHome(t, map[string]string{
		"quoted-skill": `{"UserInput":{"content":[{"type":"skill","name":"deploy",` +
			`"path":"/Users/test/Projects/say\"hi/.codex/skills/deploy/SKILL.md"}],"client_id":"c"}}`,
	})
	req := tool.MoveRequest{OldPath: `/Users/test/Projects/say"hi`, NewPath: `/Users/test/Projects/re"named`}

	planned, applied := applyQueueMove(t, home, req)

	assert.Equal(t, 1, planned)
	assert.Equal(t, planned, applied)
	payload := decodeQueuedPayload(t, readQueuedPayloadByID(t, home, "quoted-skill"))
	require.Len(t, payload.UserInput.Content, 1)
	assert.Equal(t, `/Users/test/Projects/re"named/.codex/skills/deploy/SKILL.md`, payload.UserInput.Content[0].Path)
}

func TestQueueDBMoveLeavesTextItemAndByteRangeUntouched(t *testing.T) {
	const text = "see /Users/test/Projects/app/main.go [image]"
	home := newQueueHome(t, map[string]string{
		"text-and-skill": `{"UserInput":{"content":[` +
			`{"type":"text","text":"` + text + `","text_elements":[{"byte_range":{"start":37,"end":44},"placeholder":"[image]"}]},` +
			`{"type":"skill","name":"deploy","path":"/Users/test/Projects/app/.codex/skills/deploy/SKILL.md"}` +
			`],"client_id":"c"}}`,
	})
	req := tool.MoveRequest{OldPath: "/Users/test/Projects/app", NewPath: "/Users/test/Projects/renamed-application"}

	planned, applied := applyQueueMove(t, home, req)

	assert.Equal(t, 1, planned)
	assert.Equal(t, planned, applied)
	payload := decodeQueuedPayload(t, readQueuedPayloadByID(t, home, "text-and-skill"))
	require.Len(t, payload.UserInput.Content, 2)
	textItem := payload.UserInput.Content[0]
	assert.Equal(t, text, textItem.Text)
	require.Len(t, textItem.TextElements, 1)
	assert.Equal(t, 37, textItem.TextElements[0].ByteRange.Start)
	assert.Equal(t, 44, textItem.TextElements[0].ByteRange.End)
	assert.Equal(t, "/Users/test/Projects/renamed-application/.codex/skills/deploy/SKILL.md", payload.UserInput.Content[1].Path)
}

func TestQueueDBMoveLeavesPrefixSharingSkillPathUntouched(t *testing.T) {
	const sibling = `{"UserInput":{"content":[{"type":"skill","name":"deploy",` +
		`"path":"/Users/test/Projects/app-extras/.codex/skills/deploy/SKILL.md"}],"client_id":"c"}}`
	home := newQueueHome(t, map[string]string{"sibling-skill": sibling})
	req := tool.MoveRequest{OldPath: "/Users/test/Projects/app", NewPath: "/Users/test/Projects/renamed"}

	planned, applied := applyQueueMove(t, home, req)

	assert.Zero(t, planned)
	assert.Zero(t, applied)
	assert.JSONEq(t, sibling, readQueuedPayloadByID(t, home, "sibling-skill"))
}

func TestQueueDBApplyFailsWhenPlannedPayloadChangedAfterPlan(t *testing.T) {
	const deploy = `{"UserInput":{"content":[{"type":"skill","name":"deploy",` +
		`"path":"/Users/test/Projects/app/.codex/skills/deploy/SKILL.md"}],"client_id":"c"}}`
	const release = `{"UserInput":{"content":[{"type":"skill","name":"release",` +
		`"path":"/Users/test/Projects/app/.codex/skills/release/SKILL.md"}],"client_id":"c"}}`
	home := newQueueHome(t, map[string]string{"planned-skill": deploy})
	queuePath := filepath.Join(home.SQLiteDir, codexschema.QueueDBFileName)
	req := tool.MoveRequest{OldPath: "/Users/test/Projects/app", NewPath: "/Users/test/Projects/renamed"}
	plans, err := queueDBRewritePlansForProject(context.Background(), home.SQLiteDir, req.OldPath, req.NewPath)
	require.NoError(t, err)
	database, err := sql.Open("sqlite", queuePath)
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), `UPDATE queued_items SET payload_json = ? WHERE id = ?`, release, "planned-skill")
	require.NoError(t, err)
	require.NoError(t, database.Close())
	workspace := NewWorkspace(home, fakeGetenv(nil), noProcesses)
	undo := tool.NewRestorer()

	_, err = workspace.queueDBSurfaceWithPlans(req, &pendingMoveDatabases{}, plans).Apply(context.Background(), undo)

	require.Error(t, err)
	assert.Equal(t, "rewrite "+queuePath+": rewrite queued_items.payload_json for id planned-skill: "+
		"queue database changed after the plan: no row with that id holds the planned payload any more", err.Error())
	require.NoError(t, undo.Restore())
	assert.JSONEq(t, release, readQueuedPayloadByID(t, home, "planned-skill"))
}

func TestQueueDBRewriteRollsBackBeforeFinalSurface(t *testing.T) {
	_, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	original := readQueuedPayloadByID(t, home, fixtureQueuedItemID)
	plans, err := queueDBRewritePlansForProject(context.Background(), home.SQLiteDir, req.OldPath, req.NewPath)
	require.NoError(t, err)
	workspace := NewWorkspace(home, fakeGetenv(nil), noProcesses)
	undo := tool.NewRestorer()

	count, err := workspace.queueDBSurfaceWithPlans(req, &pendingMoveDatabases{}, plans).Apply(context.Background(), undo)
	require.NoError(t, err)
	require.Equal(t, 2, count.Count)
	require.NoError(t, undo.Restore())

	assert.Equal(t, original, readQueuedPayloadByID(t, home, fixtureQueuedItemID))
}

func TestQueueDBPlanCountsZeroWhenQueueDatabaseAbsent(t *testing.T) {
	_, home := fixtureWorkspace(t)
	require.NoError(t, os.Remove(filepath.Join(home.SQLiteDir, codexschema.QueueDBFileName)))

	plans, err := queueDBRewritePlansForProject(context.Background(), home.SQLiteDir, FixtureProjectPath(), "/Users/fixture/renamed-project")

	require.NoError(t, err)
	assert.Zero(t, plans.valueCount())
}

func TestQueueDBRefusesSchemaWithoutIDKeyOrPayload(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name:   "payload_json missing",
			schema: `CREATE TABLE queued_items (id TEXT PRIMARY KEY NOT NULL, thread_id TEXT NOT NULL);`,
			want:   `unexpected schema for table "queued_items": missing column "payload_json"; observed id TEXT primary-key-1, thread_id TEXT`,
		},
		{
			name:   "id is not the primary key",
			schema: `CREATE TABLE queued_items (id TEXT NOT NULL, payload_json TEXT NOT NULL);`,
			want: `unexpected schema for table "queued_items": primary key column "id" is required; ` +
				`observed id TEXT, payload_json TEXT`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			queuePath := writeQueueSchema(t, testCase.schema)

			_, err := queueDBRewritePlansForProject(context.Background(), filepath.Dir(queuePath), "/Users/test/Projects/app", "/Users/test/Projects/renamed")

			require.Error(t, err)
			assert.Equal(t, queuePath+": "+testCase.want, err.Error())
		})
	}
}

func writeQueueSchema(t *testing.T, schema string) string {
	t.Helper()
	queuePath := filepath.Join(t.TempDir(), codexschema.QueueDBFileName)
	database, err := sql.Open("sqlite", queuePath)
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), schema)
	require.NoError(t, err)
	require.NoError(t, database.Close())
	return queuePath
}

func TestQueueDBApplyFailsWhenQueueDatabasesChangedAfterPlan(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		addedFile       string
		removePlanned   bool
		wantAdded       string
		wantRemovedFile string
	}{
		{name: "database added", addedFile: "queue_2.sqlite", wantAdded: "queue_2.sqlite"},
		{name: "database removed", removePlanned: true, wantRemovedFile: codexschema.QueueDBFileName},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, home := fixtureWorkspace(t)
			req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
			plans, err := queueDBRewritePlansForProject(context.Background(), home.SQLiteDir, req.OldPath, req.NewPath)
			require.NoError(t, err)
			if testCase.addedFile != "" {
				writeQueueDatabase(t, filepath.Join(home.SQLiteDir, testCase.addedFile))
			}
			if testCase.removePlanned {
				require.NoError(t, os.Remove(filepath.Join(home.SQLiteDir, codexschema.QueueDBFileName)))
			}
			workspace := NewWorkspace(home, fakeGetenv(nil), noProcesses)
			undo := tool.NewRestorer()
			defer func() { _ = undo.Restore() }()

			_, err = workspace.queueDBSurfaceWithPlans(req, &pendingMoveDatabases{}, plans).Apply(context.Background(), undo)

			require.Error(t, err)
			added, removed := "", ""
			if testCase.wantAdded != "" {
				added = filepath.Join(home.SQLiteDir, testCase.wantAdded)
			}
			if testCase.wantRemovedFile != "" {
				removed = filepath.Join(home.SQLiteDir, testCase.wantRemovedFile)
			}
			assert.Equal(t, "queue databases changed after the plan: added ["+added+"], removed ["+removed+"]", err.Error())
		})
	}
}

func writeQueueDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), codexschema.QueueDBSchema)
	require.NoError(t, err)
	require.NoError(t, database.Close())
}

func TestQueueDBRefusesPayloadWithoutContentArray(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		payload string
	}{
		{name: "content missing", payload: `{"UserInput":{"client_id":"c"}}`},
		{name: "content not an array", payload: `{"UserInput":{"content":"text","client_id":"c"}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := newQueueHome(t, map[string]string{"malformed-item": testCase.payload})
			queuePath := filepath.Join(home.SQLiteDir, codexschema.QueueDBFileName)

			_, err := queueDBRewritePlansForProject(context.Background(), home.SQLiteDir, "/Users/test/Projects/app", "/Users/test/Projects/renamed")

			require.Error(t, err)
			assert.Equal(t, queuePath+": queued_items malformed-item: payload_json has no UserInput.content array", err.Error())
		})
	}
}
