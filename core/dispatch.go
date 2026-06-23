// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// JurorFailedPrefix marks a failed juror reply. The workflow's cli wrapper is
// instructed to emit exactly this on error; run-juror emits it too so the
// failure signal is uniform regardless of where it originates (§14).
const JurorFailedPrefix = "JUROR_FAILED: "

// DefaultJurorTimeout bounds a single juror CLI invocation.
const DefaultJurorTimeout = 10 * time.Minute

// JurorResult is the outcome of dispatching one slot.
type JurorResult struct {
	// Review is the captured stdout (only meaningful when Failed is false).
	Review string
	// Failed is true when the juror CLI itself errored/timed out. In that case
	// Marker holds the "JUROR_FAILED: <reason>" line to relay and the slot is
	// recorded as failed.
	Failed bool
	Marker string
}

// RunJuror resolves slot i in the named run to a cli juror, execs its read-only
// command capturing stdout, and returns the result. It distinguishes two kinds
// of problem:
//
//   - A hard dispatch error (bad run/slot, subagent slot, registry/config
//     issue) → returns a non-nil error and no result. These are operator errors,
//     not juror failures, so they go to stderr and are NOT relayed as a review.
//   - A juror CLI failure (non-zero exit / timeout) → returns a JurorResult with
//     Failed=true and the JUROR_FAILED marker, and records the slot as failed.
//     err is nil because the dispatch itself succeeded.
//
// The slot→juror mapping is used internally only; it is never returned or
// printed (blind-rating integrity, §14).
func RunJuror(runID string, slot int, reg Registry, timeout time.Duration) (JurorResult, error) {
	run, err := ReadRun(runID)
	if err != nil {
		return JurorResult{}, err
	}
	rs, ok := run.SlotByIndex(slot)
	if !ok {
		return JurorResult{}, fmt.Errorf("run %s has no slot %d", runID, slot)
	}
	juror, ok := reg.ByName(rs.Juror)
	if !ok {
		return JurorResult{}, fmt.Errorf("slot %d juror not found in registry", slot)
	}
	if juror.Backend != BackendCLI {
		return JurorResult{}, fmt.Errorf("slot %d is a %s juror, not a cli juror; it is dispatched as a native subagent by the workflow, not via run-juror", slot, juror.Backend)
	}

	// Revalidate the stored material path right before exec (W2). It was
	// validated at start-run, but the run file persists the path and an attacker
	// who can swap a path component for a symlink, or edit the run file, between
	// start-run and dispatch would otherwise aim a read-only juror at an
	// arbitrary file (TOCTOU). Re-canonicalizing here closes that window; the
	// material must still be a real regular file under an allowed root.
	material, err := ValidateMaterial(run.Material)
	if err != nil {
		return JurorResult{}, fmt.Errorf("slot %d material failed revalidation: %w", slot, err)
	}

	out, runErr := execJuror(juror, material, timeout)
	if runErr != nil {
		// Surface a failure to persist the "failed" status rather than swallowing
		// it: a slot left "pending" would still accept a non-null score, letting a
		// juror that actually failed be scored as if it had run (§7).
		if statusErr := SetSlotStatus(runID, slot, StatusFailed); statusErr != nil {
			return JurorResult{}, fmt.Errorf("slot %d juror failed but recording its failed status failed: %w", slot, statusErr)
		}
		// Redact at the marker boundary so every failure origin (timeout, exec
		// setup, non-zero exit) is covered before the reason is relayed off-box —
		// the single chokepoint for §14 blindness on the failure path.
		return JurorResult{Failed: true, Marker: JurorFailedPrefix + redactIdentity(runErr.Error(), juror)}, nil
	}
	if err := SetSlotStatus(runID, slot, StatusRan); err != nil {
		return JurorResult{}, err
	}
	return JurorResult{Review: out}, nil
}

// redactIdentity removes any juror-identifying slug (model, provider) from a
// string before it is relayed off-box. A failed juror's reason is surfaced in
// the JUROR_FAILED marker that the workflow/foreman see, and a CLI commonly
// echoes its `--model`/`--provider` argument in an error — relaying that
// verbatim would leak the slot→model mapping the binary is the sole holder of
// (§14 blindness). It is applied once at the marker boundary in RunJuror so
// every failure origin is covered. We replace any occurrence with a placeholder.
func redactIdentity(s string, j Juror) string {
	for _, secret := range []string{j.Model, j.Provider} {
		if strings.TrimSpace(secret) == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "<redacted>")
	}
	return s
}

// execJuror builds and runs the juror command with a null stdin and a timeout,
// returning captured stdout. On failure it returns an error whose message
// includes stderr context; redaction of any juror-identifying slug happens at
// the marker boundary in RunJuror, so every failure origin is covered once.
func execJuror(j Juror, material string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = DefaultJurorTimeout
	}
	name, args, err := BuildJurorCommand(j, material)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = NullStdin()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("juror timed out after %s", timeout)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "non-zero exit"
			}
			return "", fmt.Errorf("juror cli exited %d: %s", exitErr.ExitCode(), truncate(msg, 500))
		}
		return "", fmt.Errorf("juror cli failed to run: %w", runErr)
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
