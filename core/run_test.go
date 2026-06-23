// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeFile is a small test helper (shared across the core test files).
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestNewRunIDShape(t *testing.T) {
	now := time.Unix(1718901780, 0)
	id, err := NewRunID(now)
	if err != nil {
		t.Fatal(err)
	}
	// "<epoch>-<5 chars>"
	const wantPrefix = "1718901780-"
	if len(id) != len(wantPrefix)+shortRandLen {
		t.Fatalf("unexpected run id %q", id)
	}
	if id[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("run id %q missing epoch prefix", id)
	}
	// Two ids in the same second must differ (random suffix).
	id2, _ := NewRunID(now)
	if id == id2 {
		t.Errorf("two run ids in same second collided: %s", id)
	}
}

func TestRunFileRoundTrip(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())

	orig := &Run{
		RunID:     "1718901780-abcde",
		Scope:     "auth refactor review",
		Material:  "/some/where/material.md",
		CreatedAt: time.Unix(1718901780, 0).UTC(),
		Slots: []RunSlot{
			{Slot: 0, Juror: "kimi", Status: StatusPending},
			{Slot: 1, Juror: "claude", Status: StatusPending},
			{Slot: 2, Juror: "codex", Status: StatusPending},
		},
	}
	if err := WriteRun(orig); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadRun(orig.RunID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.RunID != orig.RunID || got.Scope != orig.Scope || got.Material != orig.Material {
		t.Errorf("scalar mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(orig.CreatedAt) {
		t.Errorf("created_at mismatch: %v != %v", got.CreatedAt, orig.CreatedAt)
	}
	if len(got.Slots) != 3 {
		t.Fatalf("want 3 slots, got %d", len(got.Slots))
	}
	for i, s := range got.Slots {
		if s.Slot != orig.Slots[i].Slot || s.Juror != orig.Slots[i].Juror {
			t.Errorf("slot %d mismatch: %+v", i, s)
		}
	}

	// SlotByIndex + SetSlotStatus.
	if err := SetSlotStatus(orig.RunID, 1, StatusFailed); err != nil {
		t.Fatalf("set status: %v", err)
	}
	reread, _ := ReadRun(orig.RunID)
	s, ok := reread.SlotByIndex(1)
	if !ok || s.Status != StatusFailed {
		t.Errorf("slot 1 status not persisted: %+v", s)
	}
}

func TestStartRunWritesRunAndPlan(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())
	reg := Registry{Jurors: sampleJurors()}

	run, plan, err := StartRun(reg, "scope text", "/abs/material.md", time.Unix(1718901780, 0))
	if err != nil {
		t.Fatalf("start-run: %v", err)
	}
	if plan.Count != 5 || len(plan.Slots) != 5 || len(run.Slots) != 5 {
		t.Fatalf("expected 5 slots, run=%d plan=%d", len(run.Slots), len(plan.Slots))
	}
	if run.RunID != plan.RunID {
		t.Errorf("run/plan id mismatch")
	}

	// Run file must be persisted and round-trip.
	loaded, err := ReadRun(run.RunID)
	if err != nil {
		t.Fatalf("run file not written: %v", err)
	}
	if loaded.Scope != "scope text" {
		t.Errorf("scope not persisted")
	}

	// Plan must never carry juror names; subagent slot carries model+prompt,
	// cli slots carry exec only.
	subagentSlots := 0
	for i, ps := range plan.Slots {
		if ps.Slot != i {
			t.Errorf("plan slot index mismatch at %d", i)
		}
		switch ps.Mode {
		case "subagent":
			subagentSlots++
			if ps.Model == "" || ps.Prompt == "" {
				t.Errorf("subagent slot %d missing model/prompt", i)
			}
			if ps.Exec != "" {
				t.Errorf("subagent slot %d leaked exec", i)
			}
		case "cli":
			if ps.Exec == "" {
				t.Errorf("cli slot %d missing exec", i)
			}
			if ps.Model != "" || ps.Prompt != "" {
				t.Errorf("cli slot %d leaked model/prompt", i)
			}
		default:
			t.Errorf("slot %d unknown mode %q", i, ps.Mode)
		}
	}
	if subagentSlots != 1 {
		t.Errorf("want exactly 1 subagent slot (claude), got %d", subagentSlots)
	}

	// The plan must not contain any juror name string.
	planJurorNames := []string{"claude", "codex", "glm", "minimax", "kimi"}
	for _, ps := range plan.Slots {
		if ps.Mode != "cli" {
			continue
		}
		for _, n := range planJurorNames {
			if containsWord(ps.Exec, n) {
				t.Errorf("plan exec %q leaked juror name %q", ps.Exec, n)
			}
		}
	}
}

// TestSetSlotStatusConcurrent exercises the C1 fix: many slots updated
// concurrently against the same run file must not lose any update. Before the
// fix, the unlocked read-modify-write let parallel writers clobber each other's
// slot status; with the advisory lock + atomic write, every update survives.
func TestSetSlotStatusConcurrent(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())

	const n = 20
	slots := make([]RunSlot, n)
	for i := range slots {
		slots[i] = RunSlot{Slot: i, Juror: "j", Status: StatusPending}
	}
	run := &Run{RunID: "1718901780-cncrn", Scope: "race", Slots: slots}
	if err := WriteRun(run); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			// Alternate statuses so a lost write would be detectable as the wrong
			// terminal value, not just a missing one.
			status := StatusRan
			if slot%2 == 0 {
				status = StatusFailed
			}
			errs[slot] = SetSlotStatus(run.RunID, slot, status)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("slot %d SetSlotStatus: %v", i, err)
		}
	}

	got, err := ReadRun(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) != n {
		t.Fatalf("run file lost slots: got %d want %d", len(got.Slots), n)
	}
	for i := 0; i < n; i++ {
		s, ok := got.SlotByIndex(i)
		if !ok {
			t.Fatalf("slot %d missing after concurrent updates (lost write)", i)
		}
		want := StatusRan
		if i%2 == 0 {
			want = StatusFailed
		}
		if s.Status != want {
			t.Errorf("slot %d status = %q, want %q (update lost/clobbered)", i, s.Status, want)
		}
	}
}

func TestValidateRunID(t *testing.T) {
	good := []string{"1718901780-abcde", "0-a", "1718901780-abc123xyz"}
	for _, id := range good {
		if err := ValidateRunID(id); err != nil {
			t.Errorf("ValidateRunID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{
		"",
		"../../etc/passwd",
		"../escape",
		"1718901780-../x",
		"1718901780-ABC",  // uppercase not produced by NewRunID
		"1718901780-a.b",  // dot
		"1718901780-a/b",  // separator
		"no-epoch-prefix", // epoch must be digits
		"1718901780",      // no suffix
	}
	for _, id := range bad {
		if err := ValidateRunID(id); err == nil {
			t.Errorf("ValidateRunID(%q) = nil, want error", id)
		}
	}
}

// TestRunFilePathRejectsTraversal asserts a traversal run id never produces a
// path outside the runs dir (W3) — RunFilePath should error rather than join it.
func TestRunFilePathRejectsTraversal(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())
	if _, err := RunFilePath("../../escape"); err == nil {
		t.Fatal("RunFilePath accepted a traversal run id")
	}
	// Read/write paths must refuse it too.
	if _, err := ReadRun("../../escape"); err == nil {
		t.Error("ReadRun accepted a traversal run id")
	}
	if err := SetSlotStatus("../../escape", 0, StatusRan); err == nil {
		t.Error("SetSlotStatus accepted a traversal run id")
	}
}

// TestShortRandomDistribution is a coarse guard for S3: with unbiased sampling,
// the full alphabet should appear across many draws. The old `byte % 36` skewed
// toward early letters; rejection sampling spreads coverage evenly.
func TestShortRandomDistribution(t *testing.T) {
	seen := map[rune]int{}
	const draws = 2000
	for i := 0; i < draws; i++ {
		s, err := shortRandom(shortRandLen)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range s {
			if !strings.ContainsRune(shortRandAlphabet, r) {
				t.Fatalf("char %q not in alphabet", r)
			}
			seen[r]++
		}
	}
	// Every symbol in the alphabet should show up at least once over 10k chars.
	for _, r := range shortRandAlphabet {
		if seen[r] == 0 {
			t.Errorf("symbol %q never drawn — distribution looks biased", r)
		}
	}
}

func containsWord(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] == w {
			return true
		}
	}
	return false
}
