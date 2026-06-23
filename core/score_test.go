// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func scoreRun() *Run {
	return &Run{
		RunID:    "1718901780-abcde",
		Scope:    "auth refactor review",
		Material: "/x/material.md",
		Slots: []RunSlot{
			{Slot: 0, Juror: "kimi", Status: StatusRan},
			{Slot: 1, Juror: "claude", Status: StatusRan},
			{Slot: 2, Juror: "glm", Status: StatusFailed},
			{Slot: 3, Juror: "codex", Status: StatusRan},
			{Slot: 4, Juror: "minimax", Status: StatusRan},
		},
	}
}

func TestBuildScoreRecordResolvesAndHandlesNull(t *testing.T) {
	run := scoreRun()
	now := time.Date(2026, 6, 22, 14, 30, 0, 0, time.UTC)

	rec, err := BuildScoreRecord(run, []SlotScore{
		{Slot: 0, Value: intp(1)}, // kimi
		{Slot: 1, Value: intp(3)}, // claude
		{Slot: 2, Value: nil},     // glm — failed, must be null
		{Slot: 3, Value: intp(3)}, // codex
		{Slot: 4, Value: intp(2)}, // minimax
	}, "codex caught the TOCTOU others missed", now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if rec.RunID != run.RunID || rec.Scope != run.Scope {
		t.Errorf("run_id/scope not threaded from run file")
	}
	if rec.TS != "2026-06-22T14:30:00Z" {
		t.Errorf("ts = %q, want RFC3339 UTC", rec.TS)
	}

	// Slot→model resolution.
	want := map[string]*int{
		"kimi":    intp(1),
		"claude":  intp(3),
		"glm":     nil,
		"codex":   intp(3),
		"minimax": intp(2),
	}
	for model, exp := range want {
		got, ok := rec.Scores[model]
		if !ok {
			t.Errorf("missing model %q in scores", model)
			continue
		}
		if exp == nil {
			if got != nil {
				t.Errorf("%q = %d, want null", model, *got)
			}
			continue
		}
		if got == nil || *got != *exp {
			t.Errorf("%q = %v, want %d", model, got, *exp)
		}
	}

	// JSON encodes a null score as literal null, distinct from 0.
	b, _ := json.Marshal(rec)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	scores := raw["scores"].(map[string]any)
	if scores["glm"] != nil {
		t.Errorf("glm should encode as JSON null, got %v", scores["glm"])
	}
	if scores["kimi"].(float64) != 1 {
		t.Errorf("kimi should encode as 1")
	}
}

func TestBuildScoreRecordValidations(t *testing.T) {
	run := scoreRun()
	now := time.Now()

	// Out-of-range score.
	if _, err := BuildScoreRecord(run, []SlotScore{
		{0, intp(1)}, {1, intp(3)}, {2, nil}, {3, intp(3)}, {4, intp(7)},
	}, "", now); err == nil {
		t.Error("expected out-of-range error")
	}

	// Failed slot scored non-null.
	if _, err := BuildScoreRecord(run, []SlotScore{
		{0, intp(1)}, {1, intp(3)}, {2, intp(0)}, {3, intp(3)}, {4, intp(2)},
	}, "", now); err == nil {
		t.Error("expected error scoring a failed slot non-null")
	}

	// Missing slot.
	if _, err := BuildScoreRecord(run, []SlotScore{
		{0, intp(1)}, {1, intp(3)},
	}, "", now); err == nil {
		t.Error("expected error for unscored slots")
	}

	// Unknown slot index.
	if _, err := BuildScoreRecord(run, []SlotScore{
		{0, intp(1)}, {1, intp(3)}, {2, nil}, {3, intp(3)}, {4, intp(2)}, {9, intp(1)},
	}, "", now); err == nil {
		t.Error("expected error for unknown slot")
	}
}

func TestAppendScoreRecordAtomicLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jury-scores.jsonl")

	now := time.Date(2026, 6, 22, 14, 30, 0, 0, time.UTC)
	run := scoreRun()
	rec1, _ := BuildScoreRecord(run, []SlotScore{
		{0, intp(1)}, {1, intp(3)}, {2, nil}, {3, intp(3)}, {4, intp(2)},
	}, "first", now)
	rec2, _ := BuildScoreRecord(run, []SlotScore{
		{0, intp(0)}, {1, intp(2)}, {2, nil}, {3, intp(1)}, {4, intp(2)},
	}, "", now)

	if err := AppendScoreRecord(path, rec1); err != nil {
		t.Fatal(err)
	}
	if err := AppendScoreRecord(path, rec2); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines++
		var r ScoreRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Errorf("line %d is not valid JSON: %v", lines, err)
		}
	}
	if lines != 2 {
		t.Errorf("want 2 lines (one per record), got %d", lines)
	}
}

// TestAppendScoreRecordConcurrent exercises the W4 fix: concurrent appends of
// large records (a long note pushes a line past PIPE_BUF, where a bare O_APPEND
// write is no longer atomic) must each land as one intact, parseable line under
// the advisory lock — no interleaving/tearing.
func TestAppendScoreRecordConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jury-scores.jsonl")

	now := time.Date(2026, 6, 22, 14, 30, 0, 0, time.UTC)
	run := scoreRun()
	bigNote := strings.Repeat("x", 200*1024) // > typical PIPE_BUF (4 KiB)

	const writers = 16
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, _ := BuildScoreRecord(run, []SlotScore{
				{0, intp(1)}, {1, intp(3)}, {2, nil}, {3, intp(3)}, {4, intp(2)},
			}, bigNote, now)
			errs[i] = AppendScoreRecord(path, rec)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		lines++
		var r ScoreRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Errorf("line %d not intact JSON (torn write): %v", lines, err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != writers {
		t.Errorf("want %d lines (one per writer), got %d", writers, lines)
	}
}
