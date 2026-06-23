// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"errors"
	"fmt"
	"time"
)

// PlanSlot is one opaque slot in the instruction plan (§3). It carries a mode
// and either an exec string (cli) or a model+prompt (subagent) — never the
// juror name.
type PlanSlot struct {
	Slot int    `json:"slot"`
	Mode string `json:"mode"` // "cli" or "subagent"

	// cli only:
	Exec string `json:"exec,omitempty"`

	// subagent only:
	Model  string `json:"model,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

// Plan is the render plan handed to the workflow.
type Plan struct {
	RunID string     `json:"run_id"`
	Count int        `json:"count"`
	Slots []PlanSlot `json:"slots"`
}

// StartRun shuffles the enabled jurors into slots, builds and persists the run
// file, and returns the instruction plan. material must already be validated.
func StartRun(reg Registry, scope, material string, now time.Time) (*Run, *Plan, error) {
	enabled := reg.Enabled()
	if len(enabled) == 0 {
		return nil, nil, errors.New("no enabled jurors in registry")
	}

	shuffled, err := Shuffle(enabled)
	if err != nil {
		return nil, nil, err
	}

	runID, err := NewRunID(now)
	if err != nil {
		return nil, nil, err
	}

	run := &Run{
		RunID:     runID,
		Scope:     scope,
		Material:  material,
		CreatedAt: now.UTC(),
		Slots:     make([]RunSlot, len(shuffled)),
	}
	plan := &Plan{
		RunID: runID,
		Count: len(shuffled),
		Slots: make([]PlanSlot, len(shuffled)),
	}

	for i, j := range shuffled {
		run.Slots[i] = RunSlot{Slot: i, Juror: j.Name, Status: StatusPending}

		switch j.Backend {
		case BackendSubagent:
			plan.Slots[i] = PlanSlot{
				Slot:   i,
				Mode:   "subagent",
				Model:  j.Model,
				Prompt: ReviewPrompt(material),
			}
		case BackendCLI:
			plan.Slots[i] = PlanSlot{
				Slot: i,
				Mode: "cli",
				Exec: fmt.Sprintf("run-juror --run %s --slot %d", runID, i),
			}
		default:
			return nil, nil, fmt.Errorf("juror %q has invalid backend %q", j.Name, j.Backend)
		}
	}

	if err := WriteRun(run); err != nil {
		return nil, nil, err
	}
	return run, plan, nil
}
