// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Backend identifies how a juror is dispatched.
const (
	BackendCLI      = "cli"
	BackendSubagent = "subagent"
)

// Juror is one entry in the registry. It is the single source of truth shared
// by both the dispatch path (start-run / run-juror) and the score path.
type Juror struct {
	// Name is the stable id, e.g. "codex", "glm", "claude".
	Name string `toml:"name" json:"name"`
	// Backend is "cli" or "subagent".
	Backend string `toml:"backend" json:"backend"`
	// Model is the model slug, e.g. "zai-org/GLM-5.2", "gpt-5.5", "opus".
	Model string `toml:"model" json:"model"`
	// Provider is set for pi-backed jurors, e.g. "featherless".
	Provider string `toml:"provider" json:"provider,omitempty"`
	// Family groups jurors for the dashboard, e.g. "glm", "openai", "anthropic".
	Family string `toml:"family" json:"family"`
	// Enabled toggles a juror without removing it.
	Enabled bool `toml:"enabled" json:"enabled"`

	// Tool is the CLI executable for cli backends ("codex" or "pi"). It selects
	// the read-only command template in run-juror. Unused for subagent backends.
	Tool string `toml:"tool" json:"tool,omitempty"`
}

// Registry is the parsed jurors.toml.
type Registry struct {
	Jurors []Juror `toml:"juror"`
}

// Enabled returns the enabled jurors in registry order.
func (r Registry) Enabled() []Juror {
	var out []Juror
	for _, j := range r.Jurors {
		if j.Enabled {
			out = append(out, j)
		}
	}
	return out
}

// ByName looks up a juror by its stable name.
func (r Registry) ByName(name string) (Juror, bool) {
	for _, j := range r.Jurors {
		if j.Name == name {
			return j, true
		}
	}
	return Juror{}, false
}

// LoadRegistry reads and parses the registry TOML at path.
func LoadRegistry(path string) (Registry, error) {
	var reg Registry
	data, err := os.ReadFile(path)
	if err != nil {
		return reg, fmt.Errorf("read registry %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &reg); err != nil {
		return reg, fmt.Errorf("parse registry %s: %w", path, err)
	}
	if len(reg.Jurors) == 0 {
		return reg, fmt.Errorf("registry %s has no jurors", path)
	}
	// Validate basic invariants.
	seen := map[string]bool{}
	for i, j := range reg.Jurors {
		if strings.TrimSpace(j.Name) == "" {
			return reg, fmt.Errorf("registry juror #%d has empty name", i)
		}
		if seen[j.Name] {
			return reg, fmt.Errorf("registry has duplicate juror name %q", j.Name)
		}
		seen[j.Name] = true
		switch j.Backend {
		case BackendCLI:
			// A cli juror is dispatched by exec'ing its tool; validate the roster
			// up front so a bad entry fails at load, not late at dispatch (S2).
			switch j.Tool {
			case "codex":
			case "pi":
				if strings.TrimSpace(j.Provider) == "" {
					return reg, fmt.Errorf("juror %q (tool pi) has no provider", j.Name)
				}
			case "":
				return reg, fmt.Errorf("juror %q (cli) has no tool", j.Name)
			default:
				return reg, fmt.Errorf("juror %q has unsupported cli tool %q (want codex or pi)", j.Name, j.Tool)
			}
		case BackendSubagent:
		default:
			return reg, fmt.Errorf("juror %q has invalid backend %q", j.Name, j.Backend)
		}
	}
	return reg, nil
}

// SeedRegistry creates the registry dir+file with the starting roster if it
// does not already exist. It is a no-op when the file is present.
func SeedRegistry(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat registry %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir registry dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(seedTOML), 0o644); err != nil {
		return fmt.Errorf("write seed registry: %w", err)
	}
	return nil
}

// seedTOML is the starting roster (§2 / §8).
const seedTOML = `# jury registry — single source of truth for jurors.
#
# Each [[juror]] entry is consumed by BOTH the dispatch path and the score path.
# Adding / removing / disabling a juror is a one-line edit (+ go build).
#
# Fields:
#   name     stable id used in scores and slot resolution
#   backend  "cli" (run-juror execs a read-only CLI) or "subagent" (native Claude)
#   tool     for cli backends: which CLI to exec ("codex" or "pi")
#   model    model slug
#   provider for pi-backed jurors: "featherless"
#   family   dashboard grouping
#   enabled  toggle without removing

[[juror]]
name     = "claude"
backend  = "subagent"
model    = "opus"
family   = "anthropic"
enabled  = true

[[juror]]
name     = "codex"
backend  = "cli"
tool     = "codex"
model    = "gpt-5.5"
family   = "openai"
enabled  = true

[[juror]]
name     = "glm"
backend  = "cli"
tool     = "pi"
model    = "zai-org/GLM-5.2"
provider = "featherless"
family   = "glm"
enabled  = true

[[juror]]
name     = "minimax"
backend  = "cli"
tool     = "pi"
model    = "MiniMaxAI/MiniMax-M3"
provider = "featherless"
family   = "minimax"
enabled  = true

[[juror]]
name     = "kimi"
backend  = "cli"
tool     = "pi"
model    = "moonshotai/Kimi-K2.7-Code"
provider = "featherless"
family   = "moonshot"
enabled  = true
`
