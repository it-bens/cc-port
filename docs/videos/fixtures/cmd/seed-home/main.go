// Command seed-home builds synthetic Claude Code and Codex home directories for
// VHS demo recordings without touching an existing populated home.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/tool"
)

const (
	roleSource = "source"
	roleTarget = "target"
)

func main() {
	var homePath string
	var projectPath string
	var role string
	var codexStateDB bool

	flag.StringVar(&homePath, "home", "", "existing empty synthetic home directory")
	flag.StringVar(&projectPath, "project", "", "synthetic demo project path")
	flag.StringVar(&role, "role", "", "fixture role: source or target")
	flag.BoolVar(&codexStateDB, "codex-state-db", true,
		"build the source Codex state database (the rebuildable cache); set false to model a home whose cache is rebuilt after import")
	flag.Parse()

	if err := seedHome(homePath, projectPath, role, codexStateDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seedHome(homePath, projectPath, role string, codexStateDB bool) (returnError error) {
	if homePath == "" {
		return fmt.Errorf("home is required")
	}
	if role != roleSource && role != roleTarget {
		return fmt.Errorf("invalid role %q: must be source or target", role)
	}
	if role == roleSource && projectPath == "" {
		return fmt.Errorf("project is required for source role")
	}
	if role == roleTarget && projectPath != "" {
		return fmt.Errorf("project must not be set for target role")
	}
	if role == roleSource {
		resolvedProjectPath, err := tool.ResolveProjectPath(projectPath)
		if err != nil {
			return fmt.Errorf("resolve project path %q: %w", projectPath, err)
		}
		projectPath = resolvedProjectPath
	}

	info, err := os.Stat(homePath)
	if err != nil {
		return fmt.Errorf("stat home %q: %w", homePath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("home %q is not a directory", homePath)
	}
	entries, err := os.ReadDir(homePath)
	if err != nil {
		return fmt.Errorf("read home %q: %w", homePath, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("home %q must be empty", homePath)
	}

	stagingPath, err := os.MkdirTemp(homePath, ".seed-home-")
	if err != nil {
		return fmt.Errorf("create staging directory in home %q: %w", homePath, err)
	}
	defer func() {
		if removeError := os.RemoveAll(stagingPath); removeError != nil {
			returnError = errors.Join(
				returnError,
				fmt.Errorf("remove staging directory %q: %w", stagingPath, removeError),
			)
		}
	}()

	if err := seedClaude(stagingPath, projectPath, role); err != nil {
		return fmt.Errorf("seed Claude home: %w", err)
	}
	if err := seedCodex(stagingPath, projectPath, role, codexStateDB); err != nil {
		return fmt.Errorf("seed Codex home: %w", err)
	}
	if err := installStagedHome(stagingPath, homePath); err != nil {
		return fmt.Errorf("install staged home: %w", err)
	}
	return nil
}

func installStagedHome(stagingPath, homePath string) (returnError error) {
	paths := []string{".claude", ".claude.json", ".codex"}
	installed := make([]string, 0, len(paths))
	defer func() {
		if returnError == nil {
			return
		}
		for _, relativePath := range installed {
			if removeError := os.RemoveAll(filepath.Join(homePath, relativePath)); removeError != nil {
				returnError = errors.Join(
					returnError,
					fmt.Errorf("remove partial fixture %q: %w", relativePath, removeError),
				)
			}
		}
	}()

	for _, relativePath := range paths {
		if err := os.Rename(filepath.Join(stagingPath, relativePath), filepath.Join(homePath, relativePath)); err != nil {
			return fmt.Errorf("install fixture path %q: %w", relativePath, err)
		}
		installed = append(installed, relativePath)
	}
	return nil
}
