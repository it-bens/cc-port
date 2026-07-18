package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/tool"
)

// toolFlags holds the parsed --tool / --<name>-home flag locals shared by
// every data command.
type toolFlags struct {
	selected      []string
	homeOverrides map[string]*string
}

// registerToolFlags registers the repeatable --tool flag and one generated
// --<name>-home flag per registered tool on cmd's persistent flags.
func registerToolFlags(cmd *cobra.Command, toolSet *tool.Set) *toolFlags {
	flags := &toolFlags{homeOverrides: make(map[string]*string, len(toolSet.All()))}
	cmd.PersistentFlags().StringArrayVar(
		&flags.selected, "tool", nil,
		"limit to the named tool(s) (repeatable); default is every detected tool",
	)
	for _, registered := range toolSet.All() {
		var override string
		cmd.PersistentFlags().StringVar(
			&override, registered.Name()+"-home", "",
			fmt.Sprintf("override the default %s home location", registered.DisplayName()),
		)
		flags.homeOverrides[registered.Name()] = &override
	}
	return flags
}

// resolveTargets opens a Workspace for every selected tool: the tools named
// by repeated --tool flags (each must be registered), or every
// tool.Set.Detected() tool when --tool was not given at all. A
// --<name>-home override for a tool that is not selected this run is an
// error.
func resolveTargets(toolSet *tool.Set, flags *toolFlags) ([]tool.Target, error) {
	selectedTools, err := selectTools(toolSet, flags.selected)
	if err != nil {
		return nil, err
	}

	selectedNames := make(map[string]bool, len(selectedTools))
	for _, selectedTool := range selectedTools {
		selectedNames[selectedTool.Name()] = true
	}
	for _, registered := range toolSet.All() {
		if selectedNames[registered.Name()] {
			continue
		}
		if override := flags.homeOverrides[registered.Name()]; override != nil && *override != "" {
			return nil, fmt.Errorf(
				"--%s-home given but %s is not selected for this run (pass --tool %s or drop the override)",
				registered.Name(), registered.DisplayName(), registered.Name(),
			)
		}
	}

	targets := make([]tool.Target, 0, len(selectedTools))
	explicitSelection := len(flags.selected) > 0
	for _, selectedTool := range selectedTools {
		workspace, err := selectedTool.Open(*flags.homeOverrides[selectedTool.Name()])
		if err != nil {
			if !explicitSelection && errors.Is(err, tool.ErrToolAbsent) {
				continue
			}
			return nil, fmt.Errorf("open %s: %w", selectedTool.Name(), err)
		}
		targets = append(targets, tool.Target{Tool: selectedTool, Workspace: workspace})
	}
	return targets, nil
}

func selectTools(toolSet *tool.Set, names []string) ([]tool.Tool, error) {
	if len(names) == 0 {
		detected, err := toolSet.Detected()
		if err != nil {
			return nil, fmt.Errorf("detect tools: %w", err)
		}
		if len(detected) == 0 {
			return nil, fmt.Errorf("no supported tool detected on this machine; use --tool to specify one")
		}
		return detected, nil
	}

	selectedNames := make(map[string]bool, len(names))
	for _, name := range names {
		if selectedNames[name] {
			continue
		}
		matched, ok := toolSet.ByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown --tool %q", name)
		}
		selectedNames[matched.Name()] = true
	}

	selected := make([]tool.Tool, 0, len(selectedNames))
	for _, registered := range toolSet.All() {
		if selectedNames[registered.Name()] {
			selected = append(selected, registered)
		}
	}
	return selected, nil
}
