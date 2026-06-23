// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Slot statuses recorded in the run file.
const (
	StatusPending = "pending" // dispatched but not yet executed
	StatusRan     = "ran"     // juror executed and returned a review
	StatusFailed  = "failed"  // juror errored / timed out (scored null)
)

// RunSlot binds an opaque slot index to a juror name and tracks its outcome.
// The juror name is the only place the slot→model mapping is persisted; no
// command surfaces it for an unscored run.
type RunSlot struct {
	Slot   int    `json:"slot"`
	Juror  string `json:"juror"`
	Status string `json:"status"`
}

// Run is the on-disk record written by start-run and read by run-juror / score.
type Run struct {
	RunID     string    `json:"run_id"`
	Scope     string    `json:"scope"`
	Material  string    `json:"material"`
	CreatedAt time.Time `json:"created_at"`
	Slots     []RunSlot `json:"slots"`
}

// SlotByIndex returns the slot with the given index.
func (r *Run) SlotByIndex(i int) (*RunSlot, bool) {
	for idx := range r.Slots {
		if r.Slots[idx].Slot == i {
			return &r.Slots[idx], true
		}
	}
	return nil, false
}

const shortRandLen = 5

const shortRandAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// NewRunID returns "<unix-epoch>-<short-random>". The random suffix uses
// crypto/rand and is NOT derived from the timestamp, so it carries no timing
// side-channel (§14 Q3).
func NewRunID(now time.Time) (string, error) {
	suffix, err := shortRandom(shortRandLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%s", now.Unix(), suffix), nil
}

func shortRandom(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(shortRandAlphabet)))
	for i := range out {
		// crypto/rand.Int draws a uniform value in [0, max) using rejection
		// sampling internally, so there is no modulo bias toward early letters
		// (S3 — the previous `byte % 36` over-weighted 'a'..'d').
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("read randomness: %w", err)
		}
		out[i] = shortRandAlphabet[idx.Int64()]
	}
	return string(out), nil
}

// runLockPath returns the advisory-lock file path for a run. It sits beside the
// run file (a sibling ".lock" file) so the lock is per-run, not global —
// different runs never serialize against each other.
func runLockPath(runID string) (string, error) {
	path, err := RunFilePath(runID)
	if err != nil {
		return "", err
	}
	return path + ".lock", nil
}

// WriteRun writes the run file as pretty JSON, creating the runs dir if needed.
// The write is atomic (temp file + rename) so a concurrent reader never sees a
// half-written file and a crash mid-write cannot truncate the run.
func WriteRun(run *Run) error {
	path, err := RunFilePath(run.RunID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir runs dir: %w", err)
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	return atomicWriteFile(path, append(data, '\n'), 0o644)
}

// atomicWriteFile writes data to a temp file in the same directory and renames
// it over path. rename(2) within a directory is atomic, so a reader sees either
// the old or the new file, never a partial one.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp run file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed away
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp run file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp run file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp run file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename run file into place: %w", err)
	}
	return nil
}

// ReadRun loads the run file for the given run id.
func ReadRun(runID string) (*Run, error) {
	path, err := RunFilePath(runID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read run file %s: %w", path, err)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("parse run file %s: %w", path, err)
	}
	return &run, nil
}

// SetSlotStatus updates one slot's status in the run file on disk.
//
// The workflow runs jurors concurrently and each `jury run-juror` invocation
// updates the same shared run file, so this read→mutate→write must be
// serialized: without it, two processes that read the file at the same time
// each write back only their own slot's change, silently dropping the other's
// (C1). We hold an exclusive advisory lock for the whole read-modify-write and
// rewrite the file atomically, so no concurrent update is lost.
func SetSlotStatus(runID string, slot int, status string) error {
	if err := ValidateRunID(runID); err != nil {
		return err
	}
	lockPath, err := runLockPath(runID)
	if err != nil {
		return err
	}
	// Ensure the runs dir exists before creating the lock file beside it.
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("mkdir runs dir: %w", err)
	}
	return withFileLock(lockPath, func() error {
		run, err := ReadRun(runID)
		if err != nil {
			return err
		}
		s, ok := run.SlotByIndex(slot)
		if !ok {
			return fmt.Errorf("run %s has no slot %d", runID, slot)
		}
		s.Status = status
		return WriteRun(run)
	})
}
