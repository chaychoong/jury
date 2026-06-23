// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Standard on-disk locations, all under ~/.claude.
const (
	registryRel = "jury/jurors.toml"
	runsDirRel  = ".jury-runs"
	scoresRel   = "jury-scores.jsonl"
)

// Home resolves the directory holding jury state. It honours JURY_HOME (used by
// tests and for relocation); otherwise it is ~/.claude.
func Home() (string, error) {
	if h := os.Getenv("JURY_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// RegistryPath is ~/.claude/jury/jurors.toml.
func RegistryPath() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, registryRel), nil
}

// RunsDir is ~/.claude/.jury-runs.
func RunsDir() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, runsDirRel), nil
}

// runIDPattern matches the run-id format produced by NewRunID:
// "<unix-epoch>-<short-random>" where the random suffix is lowercase
// alphanumeric. A run id is caller-controlled (it arrives via `--run`), so
// constraining it to this charset both rejects path-traversal payloads like
// "../../x" and guarantees the joined path stays inside the runs dir — a slug
// matching this pattern can contain no separator, dot, or "..".
var runIDPattern = regexp.MustCompile(`^[0-9]+-[a-z0-9]+$`)

// ValidateRunID rejects a run id that does not match the generated format. This
// is the path-traversal guard (W3): without it, `--run ../../etc/x` would join
// to a file outside the runs dir.
func ValidateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("invalid run id %q", runID)
	}
	return nil
}

// RunFilePath is ~/.claude/.jury-runs/<run_id>.json. The run id is validated
// against the generated format so a caller-supplied id cannot escape the runs
// dir via path traversal.
func RunFilePath(runID string) (string, error) {
	if err := ValidateRunID(runID); err != nil {
		return "", err
	}
	d, err := RunsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, runID+".json"), nil
}

// ScoresPath is ~/.claude/jury-scores.jsonl.
func ScoresPath() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, scoresRel), nil
}
