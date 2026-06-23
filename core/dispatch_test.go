// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunJurorRejectsSubagentSlot(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())
	reg := Registry{Jurors: sampleJurors()}

	run := &Run{
		RunID: "1718901780-zzzzz",
		Scope: "s",
		Slots: []RunSlot{
			{Slot: 0, Juror: "claude", Status: StatusPending}, // subagent
			{Slot: 1, Juror: "codex", Status: StatusPending},
		},
	}
	if err := WriteRun(run); err != nil {
		t.Fatal(err)
	}

	_, err := RunJuror(run.RunID, 0, reg, time.Second)
	if err == nil {
		t.Fatal("expected dispatch error for subagent slot")
	}
	if !strings.Contains(err.Error(), "not a cli juror") {
		t.Errorf("unexpected error: %v", err)
	}
	// The error must NOT leak the juror name (blind-rating integrity).
	if strings.Contains(err.Error(), "claude") {
		t.Errorf("dispatch error leaked juror name: %v", err)
	}
}

func TestRunJurorUnknownSlot(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())
	reg := Registry{Jurors: sampleJurors()}
	run := &Run{RunID: "1718901780-yyyyy", Slots: []RunSlot{{Slot: 0, Juror: "codex"}}}
	if err := WriteRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := RunJuror(run.RunID, 9, reg, time.Second); err == nil {
		t.Error("expected error for unknown slot index")
	}
}

func TestRunJurorRecordsFailure(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())
	// A cli juror pointing at a tool that resolves to a real exec recipe but a
	// binary that does not exist → execJuror fails → slot recorded failed.
	reg := Registry{Jurors: []Juror{
		{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "definitely-not-a-real-binary-name-xyz"},
	}}
	// Make the codex executable resolution fail by clearing PATH so "codex"
	// cannot be found.
	t.Setenv("PATH", t.TempDir())

	// RunJuror revalidates the stored material before exec (W2), so it must be a
	// real regular file under an allowed root.
	matRoot := t.TempDir()
	t.Setenv("JURY_MATERIAL_ROOT", matRoot)
	material := filepath.Join(matRoot, "material.md")
	if err := os.WriteFile(material, []byte("review me"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := &Run{
		RunID:    "1718901780-wwwww",
		Material: material,
		Slots:    []RunSlot{{Slot: 0, Juror: "codex", Status: StatusPending}},
	}
	if err := WriteRun(run); err != nil {
		t.Fatal(err)
	}

	res, err := RunJuror(run.RunID, 0, reg, 5*time.Second)
	if err != nil {
		t.Fatalf("dispatch should not hard-error on juror cli failure: %v", err)
	}
	if !res.Failed {
		t.Fatal("expected Failed=true when juror binary cannot run")
	}
	if !strings.HasPrefix(res.Marker, JurorFailedPrefix) {
		t.Errorf("marker = %q, want JUROR_FAILED prefix", res.Marker)
	}
	// The relayed marker must never carry the juror's model slug — redaction is
	// applied at the marker boundary so every failure origin is covered (§14).
	if strings.Contains(res.Marker, "definitely-not-a-real-binary-name-xyz") {
		t.Errorf("marker leaked model identity: %q", res.Marker)
	}

	reread, _ := ReadRun(run.RunID)
	s, _ := reread.SlotByIndex(0)
	if s.Status != StatusFailed {
		t.Errorf("slot status = %q, want failed", s.Status)
	}
}

// TestRunJurorRevalidatesMaterial asserts the W2 fix: a stored material path
// that no longer resolves to a real file under an allowed root is rejected at
// dispatch (the TOCTOU window), as a hard dispatch error, not a juror failure.
func TestRunJurorRevalidatesMaterial(t *testing.T) {
	t.Setenv("JURY_HOME", t.TempDir())
	matRoot := t.TempDir()
	t.Setenv("JURY_MATERIAL_ROOT", matRoot)

	material := filepath.Join(matRoot, "material.md")
	if err := os.WriteFile(material, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := Registry{Jurors: []Juror{
		{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "gpt-5.5"},
	}}
	run := &Run{
		RunID:    "1718901780-revld",
		Material: material,
		Slots:    []RunSlot{{Slot: 0, Juror: "codex", Status: StatusPending}},
	}
	if err := WriteRun(run); err != nil {
		t.Fatal(err)
	}

	// Simulate the TOCTOU: the material disappears (or is swapped) after start-run.
	if err := os.Remove(material); err != nil {
		t.Fatal(err)
	}

	_, err := RunJuror(run.RunID, 0, reg, time.Second)
	if err == nil {
		t.Fatal("expected a hard dispatch error when material no longer revalidates")
	}
	if !strings.Contains(err.Error(), "revalidation") {
		t.Errorf("error %q should mention revalidation", err)
	}
}

// TestRedactIdentity asserts the W1-adjacent guard: a model/provider slug must
// never appear in a string relayed off-box (e.g. a failed juror's stderr).
func TestRedactIdentity(t *testing.T) {
	j := Juror{Name: "glm", Model: "zai-org/GLM-5.2", Provider: "featherless"}
	in := "error: provider featherless rejected model zai-org/GLM-5.2"
	out := redactIdentity(in, j)
	if strings.Contains(out, "GLM-5.2") || strings.Contains(out, "featherless") {
		t.Errorf("redactIdentity leaked identity: %q", out)
	}
	// An empty model/provider must not redact arbitrary substrings.
	if got := redactIdentity("hello", Juror{}); got != "hello" {
		t.Errorf("empty juror should not alter the string, got %q", got)
	}
}
