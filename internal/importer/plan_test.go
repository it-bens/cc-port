package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

func TestDryRun_LeavesTheDestinationUntouched(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	before := treeSnapshot(t, filepath.Dir(home.Dir))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	assert.Equal(t, before, treeSnapshot(t, filepath.Dir(home.Dir)),
		"a plan must write nothing, including staging temps")
}

func TestDryRun_ReportsWhatEachToolsArchiveBlockHolds(t *testing.T) {
	body, projectPath := buildArchive(t)

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	require.Len(t, plan.Tools, 1)
	assert.Equal(t, projectPath, plan.TargetPath)
	assert.Equal(t, "claude", plan.Tools[0].Name)
	assert.Contains(t, plan.Tools[0].Categories, "sessions")
	assert.Positive(t, plan.Tools[0].Entries)
}

// The rendered plan is what an operator reads before passing --apply, so it
// must carry the plan's own data rather than a header alone.
func TestPlanRender_ShowsWhatAnApplyWouldCommit(t *testing.T) {
	body, projectPath := buildArchive(t)
	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, plan.Render(&out))

	rendered := out.String()
	assert.Contains(t, rendered, "Target: "+projectPath)
	assert.Contains(t, rendered, "[claude]")
	assert.Regexp(t, `Categories:.*\bsessions\b`, rendered)
	assert.Regexp(t, `Entries:\s+\d+`, rendered)
	assert.Regexp(t, `\bfs\s+node `+regexp.QuoteMeta(projectPath), rendered)
}

// The archive's project block carries the fixture project's own mcpServers.
// Those launch with any session opened on the imported project, so a
// destination that declares none must see them listed before it applies.
func TestDryRun_ReportsProjectBlockMCPServersAbsentFromTheDestination(t *testing.T) {
	body, projectPath := buildArchive(t)

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	require.Len(t, plan.NewMCPServers, 1)
	assert.Equal(t, "claude", plan.NewMCPServers[0].Tool)
	require.Len(t, plan.NewMCPServers[0].Servers, 1)
	assert.Equal(t, "fs", plan.NewMCPServers[0].Servers[0].Name)
	assert.Equal(t, "node", plan.NewMCPServers[0].Servers[0].Command)
}

func TestDryRun_OmitsMCPServersTheDestinationAlreadyDeclares(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	// Byte-identical to what the archive's project block resolves to on this
	// destination, so the omission cannot be read as a near-match slipping
	// through.
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(
		`{"mcpServers":{"fs":{"command":"node","args":["`+projectPath+`/mcp/fs-server.js"]}},"projects":{}}`,
	), 0o600))

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	assert.Empty(t, plan.NewMCPServers,
		"a definition the destination already declares under the same name is not newly arriving")
}

// The claude fixture declares stdio servers only, so the HTTP transport's
// path from an archive entry to the rendered plan needs its own archive.
func TestDryRun_CarriesAnHTTPTransportDefinitionFromArchiveToRenderedPlan(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{
		"claude/config.json": `{"mcpServers":{"docs":{"url":"https://mcp.example.invalid/docs"}}}`,
	})

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/http-transport",
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, plan.Render(&out))
	assert.Regexp(t, `docs\s+https://mcp\.example\.invalid/docs`, out.String())
}

func TestDryRun_FailsWhenTheDestinationConfigCannotBeRead(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(`{"mcpServers":`), 0o600))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read destination MCP servers for claude")
}

// Every present target contributes its own plan entry and its own MCP
// comparison. The archive's Claude config entry drives Claude's destination
// read through the production plan path; Codex's destination read stays
// adapter-unit-covered only, because no Codex archive entry is recognized
// until parts 3/4 give it an archive source.
func TestDryRun_ReportsEveryPresentTool(t *testing.T) {
	body, projectPath := buildMultiToolArchive(t)
	claudeTool, codexTool := claude.New(), codex.New()
	codexDestination := codexHomeWithConfig(t, "model = \"gpt-5-fixture\"\n")

	plan, err := importer.DryRun(t.Context(), tool.NewSet(claudeTool, codexTool), []tool.Target{
		{Tool: claudeTool, Workspace: claude.NewWorkspace(blankHome(t))},
		{Tool: codexTool, Workspace: quietCodexWorkspace(codexDestination)},
	}, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	names := make([]string, 0, len(plan.Tools))
	for _, toolPlan := range plan.Tools {
		names = append(names, toolPlan.Name)
	}
	assert.Equal(t, []string{"claude", "codex"}, names)
	assert.Empty(t, plan.SkippedTools)
	require.Len(t, plan.NewMCPServers, 1,
		"the Claude config entry's MCP comparison ran against the destination")
	assert.Equal(t, "claude", plan.NewMCPServers[0].Tool)
}

// Derived by inversion from the review's corrupt-destination probe. The
// archive's config entry carries no mcpServers key at all — the default
// export shape for a project configuring no MCP servers — yet applying it
// still parses the destination configuration in the finalize splice, after
// promotion. The plan runs the same read, so the corruption surfaces before
// anything is written, with the error the finalize itself would raise.
func TestDryRun_FailsOnACorruptDestinationConfigWhenTheArchiveCarriesAConfigEntry(t *testing.T) {
	body, projectPath := buildArchiveWithoutMCPServers(t)
	home := blankHome(t)
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(`{"mcpServers":`), 0o600))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})

	var invalidConfig *claude.InvalidConfigJSONError
	require.ErrorAs(t, err, &invalidConfig,
		"the plan fails with the same error the apply's finalize would raise after promotion")
}

// The opt-in config-grants entry carries no MCP definitions, yet applying it
// still parses the destination configuration in the finalize grants-merge.
// A grants-only archive therefore preflights the same read.
func TestDryRun_FailsOnACorruptDestinationConfigWhenTheArchiveCarriesOnlyAGrantsEntry(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{
		"claude/config-grants.json": `{"allowedTools":["Bash"]}`,
	})
	home := blankHome(t)
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(`{"mcpServers":`), 0o600))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/grants-only",
		Caps:       archive.DefaultCaps(),
	})

	var invalidConfig *claude.InvalidConfigJSONError
	require.ErrorAs(t, err, &invalidConfig,
		"the plan fails with the same error the apply's grants merge would raise after promotion")
}

// An archive with no recognized entry leaves the destination configuration
// unexamined, because its apply would never parse that configuration either.
func TestDryRun_SessionsOnlyArchivePlansCleanlyAgainstACorruptDestinationConfig(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{
		"claude/sessions/11111111-1111-4111-8111-111111111111.jsonl": "{}\n",
	})
	home := blankHome(t)
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(`{"mcpServers":`), 0o600))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/sessions-only",
		Caps:       archive.DefaultCaps(),
	})

	require.NoError(t, err,
		"no recognized entry, so the corrupt destination configuration is never read")
}

// The README's Refused bullet promises a non-JSON config.json archive entry
// is refused by the plan, not only by the apply's staging.
func TestDryRun_RefusesAMalformedConfigEntry(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{"claude/config.json": `{"setting":`})

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/malformed",
		Caps:       archive.DefaultCaps(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON in archive entry")
}

// A zip may carry duplicate entry names, and Stage's whole-file overwrite
// lands only the last config.json. Reporting an earlier duplicate's servers
// would name a definition the apply never imports.
func TestDryRun_DuplicateConfigEntriesReportOnlyTheLastEntrysDefinitions(t *testing.T) {
	body := buildClaudeArchiveFromEntries(t, []archiveEntry{
		{name: "claude/config.json", content: `{"mcpServers":{"shadowed":{"command":"never-imported"}}}`},
		{name: "claude/config.json", content: `{"mcpServers":{"kept":{"command":"imported"}}}`},
	}, nil)

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/duplicate-entries",
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	require.Len(t, plan.NewMCPServers, 1)
	require.Len(t, plan.NewMCPServers[0].Servers, 1)
	assert.Equal(t, "kept", plan.NewMCPServers[0].Servers[0].Name,
		"only the last duplicate's definitions reach the plan")
}

// buildArchiveWithoutMCPServers exports the fixture project after stripping
// every mcpServers key from the staged home's .claude.json, so the archive's
// config.json entry carries a project block with no MCP definitions.
func buildArchiveWithoutMCPServers(t *testing.T) (body []byte, projectPath string) {
	t.Helper()
	home := testutil.SetupFixture(t)
	stripMCPServersKeys(t, home.ConfigFile)
	claudeTool := claude.New()

	var buf bytes.Buffer
	_, err := export.Run(t.Context(), []tool.Target{
		{Tool: claudeTool, Workspace: claude.NewWorkspace(home)},
	}, &export.Options{
		ProjectPath: testutil.FixtureProjectPath(),
		Output:      &buf,
		Selected:    map[string]map[string]bool{claudeTool.Name(): allSelected(claudeTool)},
	})
	require.NoError(t, err)
	return buf.Bytes(), testutil.FixtureProjectPath()
}

// stripMCPServersKeys removes the top-level mcpServers key and every project
// block's mcpServers key from the staged fixture config.
func stripMCPServersKeys(t *testing.T, configFile string) {
	t.Helper()
	data, err := os.ReadFile(configFile) //nolint:gosec // G304: path from the test's own temp tree
	require.NoError(t, err)
	var config map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &config))
	delete(config, "mcpServers")

	var projects map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(config["projects"], &projects))
	for _, block := range projects {
		delete(block, "mcpServers")
	}
	raw, err := json.Marshal(projects)
	require.NoError(t, err)
	config["projects"] = raw

	rewritten, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile, rewritten, 0o600))
}

func codexHomeWithConfig(t *testing.T, config string) *codex.Home {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600))
	return &codex.Home{Dir: dir, SQLiteDir: dir}
}

// Scanning archive bodies for MCP definitions is a decompression read like any
// other, so it counts against the aggregate budget rather than running outside
// it. A small cap here exercises the same branch the production 4 GiB cap
// guards.
func TestDryRun_CountsMCPClassificationReadsAgainstTheAggregateCap(t *testing.T) {
	body, projectPath := buildArchive(t)

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.Caps{MaxEntryBytes: archive.DefaultCaps().MaxEntryBytes, MaxAggregateBytes: 64},
	})

	require.Error(t, err)
	var capErr *archive.AggregateCapError
	assert.ErrorAs(t, err, &capErr)
}

func TestRenderMCPServers_NamesEachDefinitionWithItsLaunchLine(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, []importer.MCPServerSet{{
		Tool: "claude",
		Servers: []tool.MCPServer{
			{Name: "filesystem", Command: "node", Args: []string{"--enable-source-maps", "/opt/fs-server.js"}},
			{Name: "search", URL: "https://mcp.example.invalid/search"},
		},
	}}))

	rendered := out.String()
	assert.Contains(t, rendered, "New MCP servers (claude):")
	assert.Regexp(t, `filesystem\s+node --enable-source-maps /opt/fs-server\.js`, rendered)
	assert.Regexp(t, `search\s+https://mcp\.example\.invalid/search`, rendered)
}

// A crafted archive can put ANSI escapes or a CR/LF forgery inside a server
// name or argument, where a terminal would execute them and erase or forge
// plan lines. The consent surface reveals those bytes as their Go escape
// sequences instead of forwarding them.
func TestRenderMCPServers_RevealsControlBytesAsEscapes(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, []importer.MCPServerSet{{
		Tool: "claude",
		Servers: []tool.MCPServer{{
			Name:    "innocent\x1b[1A\x1b[2K",
			Command: "safe",
			Args:    []string{"--flag\r\n    forged plan line"},
		}},
	}}))

	rendered := out.String()
	assert.NotContains(t, rendered, "\x1b", "no raw ESC byte reaches the terminal")
	assert.NotContains(t, rendered, "\r", "no raw CR byte reaches the terminal")
	assert.Contains(t, rendered, `innocent\x1b[1A\x1b[2K`,
		"the name's control bytes render revealed, not stripped")
	assert.Contains(t, rendered, `safe "--flag\r\n    forged plan line"`,
		"the argument renders quoted with its control bytes revealed")
}

// args:["a b"] and args:["a","b"] launch differently, so the consent surface
// renders them distinguishably.
func TestRenderMCPServers_DistinguishesEmbeddedWhitespaceFromSplitArguments(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, []importer.MCPServerSet{{
		Tool: "claude",
		Servers: []tool.MCPServer{
			{Name: "embedded", Command: "run", Args: []string{"a b"}},
			{Name: "split", Command: "run", Args: []string{"a", "b"}},
		},
	}}))

	rendered := out.String()
	assert.Regexp(t, `embedded\s+run "a b"`, rendered)
	assert.Regexp(t, `split\s+run a b`, rendered)
}

// A server name embedding whitespace could absorb the padded launch column:
// a crafted name ending in "node" paired with command "evil" would render
// byte-identically to an honest name "safe" launching "node evil". An
// ambiguous name therefore quotes like a launch part.
func TestRenderMCPServers_QuotesAServerNameThatEmbedsWhitespace(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, []importer.MCPServerSet{{
		Tool:    "claude",
		Servers: []tool.MCPServer{{Name: "safe                     node", Command: "evil"}},
	}}))

	assert.Regexp(t, `"safe\s+node"\s+evil`, out.String())
}

// A definition naming neither a command nor a URL decodes without error in
// both adapters, so the consent surface labels it rather than rendering a
// blank launch line that would read as a server launching nothing.
func TestRenderMCPServers_LabelsADefinitionWithNoLaunchTarget(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, []importer.MCPServerSet{{
		Tool:    "claude",
		Servers: []tool.MCPServer{{Name: "targetless"}},
	}}))

	assert.Regexp(t, `targetless\s+\(no launch target\)`, out.String())
}

func TestRenderMCPServers_WritesNothingWithoutNewDefinitions(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, nil))

	assert.Empty(t, out.String())
}

func claudeToolSet() *tool.Set { return tool.NewSet(claude.New()) }

func claudeTargets(home *claude.Home) []tool.Target {
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

// treeSnapshot lists every path under root with a digest of its contents, so a
// comparison catches a promoted file, a leftover staging temp, and a
// same-length in-place rewrite alike.
func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			paths = append(paths, relative+" <dir>")
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G304: path from the test's own temp tree
		if readErr != nil {
			return readErr
		}
		paths = append(paths, relative+" "+hex.EncodeToString(sha256Sum(data)))
		return nil
	}))
	sort.Strings(paths)
	return paths
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
