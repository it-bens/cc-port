package importer

import (
	"context"
	"fmt"
	"sort"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// Plan is the read-only result of DryRun: what Run would commit, with no
// destination touched. Render writes it for the operator to approve.
type Plan struct {
	TargetPath string

	// Tools carries one entry per target the archive has data for, in
	// registry order. SkippedTools names the rest.
	Tools        []ToolPlan
	SkippedTools []string

	// NewMCPServers lists, per tool, the archive's MCP server definitions the
	// destination does not already declare. These start launching with every
	// session once the import applies, which is what the plan exists to show.
	NewMCPServers []MCPServerSet
}

// ToolPlan is one tool's share of an import plan.
type ToolPlan struct {
	Name       string
	Categories []string
	Entries    int
}

// MCPServerSet pairs one tool's wire name with MCP server definitions
// attributed to it.
type MCPServerSet struct {
	Tool    string
	Servers []tool.MCPServer
}

// DryRun runs the gates Run runs before its first write — manifest and entry
// tool verification, per-tool category validation, anchor resolution,
// resolution merging, and the unresolved-reference check — then reports what
// an apply would commit. It takes no lock and writes nothing, so it never
// reaches the refusals a tool's own Stage raises.
func DryRun(ctx context.Context, allTools *tool.Set, targets []tool.Target, options *Options) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canceled: %w", err)
	}
	if options.Source == nil {
		return nil, fmt.Errorf("importer: %w", ErrSourceNil)
	}
	if len(targets) == 0 {
		return nil, ErrNoTargets
	}
	if options.Reporter == nil {
		options.Reporter = progress.Noop()
	}

	content, err := readArchive(allTools, options)
	if err != nil {
		return nil, err
	}
	result, present, resolutionsByTool, err := preflightTargets(
		ctx, targets, options, content.blocksByTool, content.entriesByTool,
	)
	if err != nil {
		return nil, err
	}
	if err := preflightStagingDirs(present, options.TargetPath); err != nil {
		return nil, err
	}

	plan := &Plan{TargetPath: options.TargetPath, SkippedTools: result.SkippedTools}
	for _, target := range present {
		name := target.Tool.Name()
		entries := content.entriesByTool[name]
		plan.Tools = append(plan.Tools, ToolPlan{
			Name:       name,
			Categories: includedCategories(target.Tool, content.blocksByTool[name]),
			Entries:    len(entries),
		})
		servers, err := NewMCPServers(ctx, target, entries, resolutionsByTool[name], options.Caps.MaxAggregateBytes)
		if err != nil {
			return nil, err
		}
		if len(servers) > 0 {
			plan.NewMCPServers = append(plan.NewMCPServers, MCPServerSet{Tool: name, Servers: servers})
		}
	}
	return plan, nil
}

// NewMCPServers returns the MCP server definitions target's archive entries
// carry whose name the destination does not already declare, sorted by name.
// The destination read runs whenever any entry was recognized, even one
// carrying no definitions, so a plan fails on the same unreadable or
// unparseable destination configuration the apply's finalize would fail on.
func NewMCPServers(
	ctx context.Context,
	target tool.Target,
	entries []archive.RawEntry,
	resolutions map[string]string,
	maxAggregateBytes int64,
) ([]tool.MCPServer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := target.Tool.Name()

	// One name can arrive from more than one archive scope. Keying by name
	// reports it once, carrying the last entry's definition, which is the one
	// a tool's own last-entry-wins merge leaves on the destination.
	recognized := false
	incoming := make(map[string]tool.MCPServer)
	aggregate := archive.NewAggregateCounter(maxAggregateBytes)
	for _, raw := range lastEntryPerName(entries) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		servers, err := target.Workspace.ArchiveMCPServers(
			raw.Entry.WithAggregateCounter(aggregate), resolutions,
		)
		if err != nil {
			return nil, fmt.Errorf("read archive MCP servers for %s: %w", name, err)
		}
		if servers != nil {
			recognized = true
		}
		for _, server := range servers {
			incoming[server.Name] = server
		}
	}
	// The destination read stays unrun when no entry was recognized: such an
	// archive must not let an unreadable destination configuration fail a
	// plan whose apply would never have parsed that configuration.
	if !recognized {
		return nil, nil
	}

	existing, err := target.Workspace.MCPServers()
	if err != nil {
		return nil, fmt.Errorf("read destination MCP servers for %s: %w", name, err)
	}
	for _, server := range existing {
		delete(incoming, server.Name)
	}
	if len(incoming) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(incoming))
	for serverName := range incoming {
		names = append(names, serverName)
	}
	sort.Strings(names)
	servers := make([]tool.MCPServer, 0, len(names))
	for _, serverName := range names {
		servers = append(servers, incoming[serverName])
	}
	return servers, nil
}

// lastEntryPerName drops every archive entry shadowed by a later entry of
// the same name. A zip may carry duplicate names, and Stage's whole-file
// overwrite lands only the last one, so reading an earlier duplicate would
// report definitions an apply never imports.
func lastEntryPerName(entries []archive.RawEntry) []archive.RawEntry {
	last := make(map[string]int, len(entries))
	for i, raw := range entries {
		last[raw.Entry.Name] = i
	}
	deduped := make([]archive.RawEntry, 0, len(last))
	for i, raw := range entries {
		if last[raw.Entry.Name] == i {
			deduped = append(deduped, raw)
		}
	}
	return deduped
}

// includedCategories returns the names of block's included categories in
// toolImpl's canonical registration order. block has already passed
// manifest.ApplyToolCategories by the time a plan renders it.
func includedCategories(toolImpl tool.Tool, block manifest.Tool) []string {
	included := make(map[string]bool, len(block.Categories))
	for _, category := range block.Categories {
		included[category.Name] = category.Included
	}
	names := make([]string, 0, len(block.Categories))
	for _, name := range tool.CategoryNames(toolImpl) {
		if included[name] {
			names = append(names, name)
		}
	}
	return names
}
