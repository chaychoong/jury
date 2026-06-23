// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Score is a rating for one slot: an int 0..3, or nil for null (abstain/fail).
// We model null distinctly from 0 so the dashboard can separate participation
// from quality (§7).
type Score = *int

// ScoreRecord is one line in the JSONL log.
type ScoreRecord struct {
	RunID  string           `json:"run_id"`
	TS     string           `json:"ts"` // RFC3339 UTC
	Scope  string           `json:"scope"`
	Scores map[string]Score `json:"scores"`
	Note   string           `json:"note,omitempty"`
}

// SlotScore is a parsed "<slot>=<value>" argument. Value nil means null.
type SlotScore struct {
	Slot  int
	Value Score
}

// BuildScoreRecord resolves slot scores against the run's slot→model map and
// produces the record to append. This resolution (the de-anonymization) happens
// only here, at log-write time — never for an unscored run (§14 blind-rating
// integrity).
//
// Every enabled slot in the run must receive a score; a slot recorded as failed
// must be scored null. Returns an error otherwise.
func BuildScoreRecord(run *Run, slotScores []SlotScore, note string, now time.Time) (*ScoreRecord, error) {
	bySlot := map[int]Score{}
	seen := map[int]bool{}
	for _, ss := range slotScores {
		if _, ok := run.SlotByIndex(ss.Slot); !ok {
			return nil, fmt.Errorf("run %s has no slot %d", run.RunID, ss.Slot)
		}
		if seen[ss.Slot] {
			return nil, fmt.Errorf("slot %d scored more than once", ss.Slot)
		}
		seen[ss.Slot] = true
		if ss.Value != nil {
			v := *ss.Value
			if v < 0 || v > 3 {
				return nil, fmt.Errorf("slot %d score %d out of range 0..3", ss.Slot, v)
			}
		}
		bySlot[ss.Slot] = ss.Value
	}

	scores := map[string]Score{}
	for _, slot := range run.Slots {
		val, ok := bySlot[slot.Slot]
		if !ok {
			return nil, fmt.Errorf("slot %d (juror present in run) was not scored; use null if it abstained/failed", slot.Slot)
		}
		// A failed slot may only be scored null.
		if slot.Status == StatusFailed && val != nil {
			return nil, fmt.Errorf("slot %d failed (JUROR_FAILED) and must be scored null, not %d", slot.Slot, *val)
		}
		scores[slot.Juror] = val
	}

	return &ScoreRecord{
		RunID:  run.RunID,
		TS:     now.UTC().Format(time.RFC3339),
		Scope:  run.Scope,
		Scores: scores,
		Note:   note,
	}, nil
}

// AppendScoreRecord marshals the record to a single line and appends it to the
// log at path. A single O_APPEND write is only guaranteed atomic for writes
// under PIPE_BUF; a record with a long note or many jurors can exceed that, so
// two concurrent `jury score` runs could interleave/tear each other's lines
// (W4). We take an exclusive advisory lock around the open+write so each record
// lands as one intact line.
func AppendScoreRecord(path string, rec *ScoreRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir scores dir: %w", err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal score record: %w", err)
	}
	line = append(line, '\n')

	return withFileLock(path+".lock", func() error {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open scores log: %w", err)
		}
		defer f.Close()
		if _, err := f.Write(line); err != nil {
			return fmt.Errorf("append score record: %w", err)
		}
		return nil
	})
}
