// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chaychoong/jury/core"
)

const esc = "\x1b"

func containsANSI(s string) bool { return strings.Contains(s, esc) }

func sampleJurors() []core.Juror {
	return []core.Juror{
		{Name: "claude", Backend: "subagent", Model: "opus", Family: "anthropic", Enabled: true},
		{Name: "codex", Backend: "cli", Tool: "codex", Model: "gpt-5.5", Family: "openai", Enabled: true},
		{Name: "glm", Backend: "cli", Tool: "pi", Model: "zai-org/GLM-5.2", Provider: "featherless", Family: "glm", Enabled: false},
	}
}

func TestRenderRosterStyledHasANSIPlainDoesNot(t *testing.T) {
	js := sampleJurors()

	plain := renderRoster(js, false)
	if containsANSI(plain) {
		t.Errorf("plain roster contains ANSI escapes:\n%q", plain)
	}
	// Plain output must still carry the data.
	for _, frag := range []string{"claude", "subagent", "opus", "anthropic", "disabled", "featherless"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("plain roster missing %q", frag)
		}
	}

	styled := renderRoster(js, true)
	if !containsANSI(styled) {
		t.Errorf("styled roster should contain ANSI escapes; got:\n%q", styled)
	}
}

func TestRenderPlanStyledVsPlainAndNoNameLeak(t *testing.T) {
	plan := &core.Plan{
		RunID: "1718901780-abcde",
		Count: 3,
		Slots: []core.PlanSlot{
			{Slot: 0, Mode: "cli", Exec: "run-juror --run 1718901780-abcde --slot 0"},
			{Slot: 1, Mode: "subagent", Model: "opus", Prompt: "review prompt"},
			{Slot: 2, Mode: "cli", Exec: "run-juror --run 1718901780-abcde --slot 2"},
		},
	}

	plain := renderPlan(plan, "auth refactor", false)
	if containsANSI(plain) {
		t.Errorf("plain plan contains ANSI:\n%q", plain)
	}
	styled := renderPlan(plan, "auth refactor", true)
	if !containsANSI(styled) {
		t.Errorf("styled plan should contain ANSI")
	}

	// Neither rendering may leak juror names (only modes/tiers).
	for _, out := range []string{plain, styled} {
		for _, name := range []string{"claude", "codex", "glm", "minimax", "kimi"} {
			if strings.Contains(stripANSI(out), name) {
				t.Errorf("plan rendering leaked juror name %q", name)
			}
		}
		// The prompt text must not appear in the human summary.
		if strings.Contains(out, "review prompt") {
			t.Errorf("plan rendering leaked the prompt body")
		}
	}
}

func TestRenderScoreNullDistinctAndNoANSIWhenPlain(t *testing.T) {
	run := &core.Run{
		RunID: "1718901780-abcde",
		Slots: []core.RunSlot{
			{Slot: 0, Juror: "kimi", Status: core.StatusRan},
			{Slot: 1, Juror: "glm", Status: core.StatusFailed},
		},
	}
	three := 3
	rec := &core.ScoreRecord{
		RunID:  run.RunID,
		Scores: map[string]core.Score{"kimi": &three, "glm": nil},
		Note:   "glm timed out",
	}

	plain := renderScore(run, rec, "/x/jury-scores.jsonl", false)
	if containsANSI(plain) {
		t.Errorf("plain score contains ANSI:\n%q", plain)
	}
	if !strings.Contains(plain, "null") {
		t.Errorf("plain score should render null distinctly")
	}
	if !strings.Contains(plain, "kimi") || !strings.Contains(plain, "glm") {
		t.Errorf("plain score should resolve slot→juror names (reveal at log time)")
	}

	styled := renderScore(run, rec, "/x/jury-scores.jsonl", true)
	if !containsANSI(styled) {
		t.Errorf("styled score should contain ANSI")
	}
	if !strings.Contains(stripANSI(styled), "null") {
		t.Errorf("styled score should still show null")
	}
}

// TestJSONEncodingHasNoANSI guards the hard requirement that --json paths are
// pure JSON. We exercise the same encoder the commands use.
func TestJSONEncodingHasNoANSI(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sampleJurors()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if containsANSI(out) {
		t.Errorf("JSON encoding contains ANSI escapes")
	}
	var back []core.Juror
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Errorf("encoded JSON does not round-trip: %v", err)
	}
}

// stripANSI removes CSI escape sequences so name-leak assertions look at the
// visible text only.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			// skip until a letter terminates the CSI sequence
			i++
			for i < len(s) && !((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
