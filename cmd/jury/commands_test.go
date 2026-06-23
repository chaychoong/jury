// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chaychoong/jury/core"
)

// TestRunJurorReturnsSentinelOnFailure asserts the W5 fix: a juror failure makes
// run-juror's RunE print the raw "JUROR_FAILED: <reason>" marker to its output
// writer and RETURN a *jurorFailedError (so a non-zero exit code is set by
// fang) instead of calling os.Exit inside RunE. The marker and the non-zero
// outcome — the workflow's contract — are both preserved, but the path is now
// testable and runs deferred cleanup.
func TestRunJurorReturnsSentinelOnFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JURY_HOME", home)
	// Seed registry so loadRegistry succeeds; override with our own roster file.
	regPath := filepath.Join(home, "jury", "jurors.toml")
	if err := os.MkdirAll(filepath.Dir(regPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte(
		"[[juror]]\nname='codex'\nbackend='cli'\ntool='codex'\nmodel='gpt-5.5'\nfamily='openai'\nenabled=true\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	// Real material under an allowed root (RunJuror revalidates it — W2).
	matRoot := t.TempDir()
	t.Setenv("JURY_MATERIAL_ROOT", matRoot)
	material := filepath.Join(matRoot, "material.md")
	if err := os.WriteFile(material, []byte("review me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Make the codex binary unresolvable so the juror CLI "fails".
	t.Setenv("PATH", t.TempDir())

	run := &core.Run{
		RunID:    "1718901780-cmdfl",
		Material: material,
		Slots:    []core.RunSlot{{Slot: 0, Juror: "codex", Status: core.StatusPending}},
	}
	if err := core.WriteRun(run); err != nil {
		t.Fatal(err)
	}

	cmd := newRunJurorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--run", run.RunID, "--slot", "0"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error so fang sets a non-zero exit code")
	}
	var jf *jurorFailedError
	if !errors.As(err, &jf) {
		t.Fatalf("expected *jurorFailedError, got %T: %v", err, err)
	}
	if !strings.Contains(out.String(), core.JurorFailedPrefix) {
		t.Errorf("marker not printed to stdout; got %q", out.String())
	}
}

// TestRunJurorSuccessRelaysRaw asserts the success path still relays raw stdout
// and returns nil. It uses a fake "codex" on PATH that prints a review.
func TestRunJurorSuccessRelaysRaw(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JURY_HOME", home)
	regPath := filepath.Join(home, "jury", "jurors.toml")
	if err := os.MkdirAll(filepath.Dir(regPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte(
		"[[juror]]\nname='codex'\nbackend='cli'\ntool='codex'\nmodel='gpt-5.5'\nfamily='openai'\nenabled=true\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	matRoot := t.TempDir()
	t.Setenv("JURY_MATERIAL_ROOT", matRoot)
	material := filepath.Join(matRoot, "material.md")
	if err := os.WriteFile(material, []byte("review me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake codex on PATH that emits a deterministic review and exits 0.
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'FINDING: looks fine'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	run := &core.Run{
		RunID:    "1718901780-cmdok",
		Material: material,
		Slots:    []core.RunSlot{{Slot: 0, Juror: "codex", Status: core.StatusPending}},
	}
	if err := core.WriteRun(run); err != nil {
		t.Fatal(err)
	}

	cmd := newRunJurorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--run", run.RunID, "--slot", "0"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("success path returned error: %v", err)
	}
	if !strings.Contains(out.String(), "FINDING: looks fine") {
		t.Errorf("review not relayed; got %q", out.String())
	}
	if containsANSI(out.String()) {
		t.Errorf("relayed review must be raw/unstyled; got ANSI in %q", out.String())
	}
}
