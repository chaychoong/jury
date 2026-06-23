// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/chaychoong/jury/core"
	"github.com/spf13/cobra"
)

// jurorFailedError signals that a juror CLI failed (non-zero/timeout). It is
// returned by run-juror's RunE instead of calling os.Exit inside cobra (W5), so
// the failure path runs deferred cleanup and stays testable. The marker has
// already been written to stdout by the time this is returned; main recognizes
// this type to exit non-zero WITHOUT letting fang print a styled error on top
// of the raw marker.
type jurorFailedError struct{ marker string }

func (e *jurorFailedError) Error() string { return e.marker }

// newRootCmd assembles the cobra command tree.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "jury",
		Short: "Multi-model adversarial review tool",
		Long: "jury runs a model-diverse adversarial review: it owns the juror registry, " +
			"the slot shuffle/anonymization, read-only CLI dispatch, run files, and scoring. " +
			"Orchestration and synthesis live in the Claude Code workflow.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newListCmd(), newStartRunCmd(), newRunJurorCmd(), newScoreCmd(), newTuiCmd())
	return root
}

// loadRegistry seeds (if absent) and loads the registry.
func loadRegistry() (core.Registry, error) {
	path, err := core.RegistryPath()
	if err != nil {
		return core.Registry{}, err
	}
	if err := core.SeedRegistry(path); err != nil {
		return core.Registry{}, err
	}
	return core.LoadRegistry(path)
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func newListCmd() *cobra.Command {
	var onlyEnabled, asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print the juror roster",
		Long:  "Print the juror roster from the registry. Never touches any run file, so it can never leak a slot→model mapping.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}
			jurors := reg.Jurors
			if onlyEnabled {
				jurors = reg.Enabled()
			}

			// GUARDRAIL: --json must be pure JSON, no styling ever.
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(jurors)
			}

			fmt.Fprint(cmd.OutOrStdout(), renderRoster(jurors, styleActive(cmd.OutOrStdout(), false)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&onlyEnabled, "enabled", false, "only show enabled jurors")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON (pure, unstyled)")
	return cmd
}

// renderRoster builds the human-readable roster. When styled is false it emits
// plain aligned text (non-TTY / piped).
func renderRoster(jurors []core.Juror, styled bool) string {
	var b strings.Builder

	if styled {
		b.WriteString(styHeader.Render("Juror roster") + "\n\n")
	} else {
		b.WriteString("Juror roster\n\n")
	}

	for _, j := range jurors {
		name := j.Name
		state := "enabled"
		if !j.Enabled {
			state = "disabled"
		}
		prov := ""
		if j.Provider != "" {
			prov = " · " + j.Provider
		}

		if !styled {
			plainProv := ""
			if j.Provider != "" {
				plainProv = " provider=" + j.Provider
			}
			fmt.Fprintf(&b, "%-10s %-9s backend=%-8s model=%-26s family=%s%s\n",
				name, state, j.Backend, j.Model, j.Family, plainProv)
			continue
		}

		dot := lipgloss.NewStyle().Foreground(familyColor(j.Family)).Render("●")
		nameCol := lipgloss.NewStyle().Bold(true).Width(11).Render(name)
		stateCol := styEnabled.Width(9).Render(state)
		if !j.Enabled {
			stateCol = styDisabled.Width(9).Render(state)
		}
		backendCol := styLabel.Render("backend ") + styValue.Width(9).Render(j.Backend)
		modelCol := styLabel.Render("model ") + styValue.Width(27).Render(j.Model)
		familyCol := styLabel.Render("family ") +
			lipgloss.NewStyle().Foreground(familyColor(j.Family)).Render(j.Family+prov)

		fmt.Fprintf(&b, "%s %s%s%s%s%s\n",
			dot, nameCol, stateCol, backendCol, modelCol, familyCol)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// start-run
// ---------------------------------------------------------------------------

func newStartRunCmd() *cobra.Command {
	var scope, material string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "start-run",
		Short: "Shuffle jurors into slots, write the run file, emit the instruction plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(scope) == "" {
				return errors.New("--scope is required")
			}
			if strings.TrimSpace(material) == "" {
				return errors.New("--material is required")
			}
			validMaterial, err := core.ValidateMaterial(material)
			if err != nil {
				return err
			}
			reg, err := loadRegistry()
			if err != nil {
				return err
			}
			_, plan, err := core.StartRun(reg, scope, validMaterial, time.Now())
			if err != nil {
				return err
			}

			// GUARDRAIL: --json must be pure JSON, no styling ever.
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(plan)
			}

			fmt.Fprint(cmd.OutOrStdout(), renderPlan(plan, scope, styleActive(cmd.OutOrStdout(), false)))
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "human-readable scope description (required)")
	cmd.Flags().StringVar(&material, "material", "", "path to the material file under review (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the plan as JSON (pure, unstyled)")
	return cmd
}

// renderPlan renders the human-facing run summary. It prints slot modes/tiers
// only — never juror names (blind-rating integrity).
func renderPlan(plan *core.Plan, scope string, styled bool) string {
	var b strings.Builder

	if !styled {
		fmt.Fprintf(&b, "run_id: %s\nscope:  %s\ncount:  %d\n", plan.RunID, scope, plan.Count)
		for _, s := range plan.Slots {
			if s.Mode == "cli" {
				fmt.Fprintf(&b, "  slot %d  cli       exec: jury %s\n", s.Slot, s.Exec)
			} else {
				fmt.Fprintf(&b, "  slot %d  subagent  model: %s\n", s.Slot, s.Model)
			}
		}
		return b.String()
	}

	b.WriteString(styHeader.Render("Jury run started") + "\n")
	b.WriteString(styLabel.Render("run_id  ") + styValue.Render(plan.RunID) + "\n")
	b.WriteString(styLabel.Render("scope   ") + styValue.Render(scope) + "\n")
	b.WriteString(styLabel.Render("slots   ") + styValue.Render(strconv.Itoa(plan.Count)) + "\n\n")

	for _, s := range plan.Slots {
		slotTag := stySlot.Render(fmt.Sprintf("slot %d", s.Slot))
		if s.Mode == "cli" {
			mode := lipgloss.NewStyle().Foreground(colMuted).Width(9).Render("cli")
			fmt.Fprintf(&b, "  %s %s%s%s\n",
				slotTag, mode, styLabel.Render("exec "), styValue.Render("jury "+s.Exec))
		} else {
			// The plan deliberately carries no juror family (blindness), so we
			// cannot color this by the real family without a leak; use the neutral
			// accent rather than falsely implying every subagent is anthropic.
			mode := lipgloss.NewStyle().Foreground(colAccent).Width(9).Render("subagent")
			fmt.Fprintf(&b, "  %s %s%s%s\n",
				slotTag, mode, styLabel.Render("model "), styValue.Render(s.Model))
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// run-juror
// ---------------------------------------------------------------------------

func newRunJurorCmd() *cobra.Command {
	var runID string
	var slot int
	cmd := &cobra.Command{
		Use:   "run-juror",
		Short: "Resolve a slot to its juror, exec the read-only CLI, relay raw stdout",
		Long: "Execute the read-only juror CLI for a slot and relay its raw stdout (the model's review). " +
			"Output is intentionally unstyled — it is fed verbatim to the foreman. On juror failure it " +
			"prints 'JUROR_FAILED: <reason>' and exits non-zero.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID == "" {
				return errors.New("--run is required")
			}
			if slot < 0 {
				return errors.New("--slot is required (>= 0)")
			}
			reg, err := loadRegistry()
			if err != nil {
				return err
			}
			res, dispatchErr := core.RunJuror(runID, slot, reg, core.DefaultJurorTimeout)
			if dispatchErr != nil {
				// Hard dispatch/config error → normal (styled) error path.
				return dispatchErr
			}
			if res.Failed {
				// GUARDRAIL: relay the marker raw on stdout (no styling), then
				// return a sentinel so cobra/fang set a non-zero exit code without
				// printing a styled error over the raw marker. We do NOT os.Exit
				// here (W5): returning lets deferred cleanup run and makes the
				// failure path unit-testable.
				fmt.Fprintln(cmd.OutOrStdout(), res.Marker)
				return &jurorFailedError{marker: res.Marker}
			}
			// GUARDRAIL: raw, unstyled review on stdout — exactly the CLI output.
			fmt.Fprint(cmd.OutOrStdout(), res.Review)
			if !strings.HasSuffix(res.Review, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run", "", "run id (required)")
	cmd.Flags().IntVar(&slot, "slot", -1, "slot index (required, >= 0)")
	return cmd
}

// ---------------------------------------------------------------------------
// score
// ---------------------------------------------------------------------------

func newScoreCmd() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "score <run_id> <slot>=<0..3|null> ...",
		Short: "Resolve slots→models via the run file and append a JSONL score record",
		Long: "Score a run after triage. Provide a rating per slot (0..3, or null for an abstained/failed juror). " +
			"The slot→model mapping is revealed only here, after the record is written.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			slotScores, err := parseSlotScores(args[1:])
			if err != nil {
				return err
			}
			run, err := core.ReadRun(runID)
			if err != nil {
				return err
			}
			rec, err := core.BuildScoreRecord(run, slotScores, note, time.Now())
			if err != nil {
				return err
			}
			scoresPath, err := core.ScoresPath()
			if err != nil {
				return err
			}
			if err := core.AppendScoreRecord(scoresPath, rec); err != nil {
				return err
			}

			// The reveal happens only now, after the record is committed (§14).
			fmt.Fprint(cmd.OutOrStdout(), renderScore(run, rec, scoresPath, styleActive(cmd.OutOrStdout(), false)))
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "optional free-text note")
	return cmd
}

func renderScore(run *core.Run, rec *core.ScoreRecord, scoresPath string, styled bool) string {
	var b strings.Builder

	scoreText := func(v core.Score) (text string, isNull bool) {
		if v == nil {
			return "null", true
		}
		return strconv.Itoa(*v), false
	}

	if !styled {
		fmt.Fprintf(&b, "scored run %s → %s\n", rec.RunID, scoresPath)
		for _, slot := range run.Slots {
			text, _ := scoreText(rec.Scores[slot.Juror])
			fmt.Fprintf(&b, "  slot %d  %-10s %s\n", slot.Slot, slot.Juror, text)
		}
		return b.String()
	}

	b.WriteString(styHeader.Render("Run scored") + "\n")
	b.WriteString(styLabel.Render("run_id  ") + styValue.Render(rec.RunID) + "\n")
	b.WriteString(styLabel.Render("log     ") + styValue.Render(scoresPath) + "\n")
	if rec.Note != "" {
		b.WriteString(styLabel.Render("note    ") + styValue.Render(rec.Note) + "\n")
	}
	b.WriteString("\n")

	for _, slot := range run.Slots {
		text, isNull := scoreText(rec.Scores[slot.Juror])
		slotTag := stySlot.Render(fmt.Sprintf("slot %d", slot.Slot))
		jurorCol := lipgloss.NewStyle().Foreground(colKey).Width(11).Render(slot.Juror)
		var scoreCol string
		if isNull {
			scoreCol = styNull.Render("null (abstained)")
		} else {
			scoreCol = lipgloss.NewStyle().Bold(true).Foreground(colOK).Render(text)
		}
		fmt.Fprintf(&b, "  %s %s%s\n", slotTag, jurorCol, scoreCol)
	}
	return b.String()
}

// parseSlotScores parses "<int>=<0..3|null>" tokens.
func parseSlotScores(toks []string) ([]core.SlotScore, error) {
	var out []core.SlotScore
	for _, t := range toks {
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			return nil, fmt.Errorf("bad score argument %q (expected <slot>=<0..3|null>)", t)
		}
		slot, err := strconv.Atoi(strings.TrimSpace(k))
		if err != nil {
			return nil, fmt.Errorf("bad slot in %q: %w", t, err)
		}
		v = strings.TrimSpace(v)
		ss := core.SlotScore{Slot: slot}
		if v == "null" {
			ss.Value = nil
		} else if v == "" {
			return nil, fmt.Errorf("bad score in %q: empty value; use an explicit 0..3 or null", t)
		} else {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("bad score in %q: %w", t, err)
			}
			ss.Value = &n
		}
		out = append(out, ss)
	}
	return out, nil
}
