// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"strings"
	"testing"

	"github.com/chaychoong/jury/core"
)

func sp(n int) *int { return &n }

func sampleAgg() core.Aggregation {
	recs := []core.ScoreRecord{
		{RunID: "r1", TS: "2026-06-01T10:00:00Z", Scope: "auth refactor",
			Scores: map[string]core.Score{"claude": sp(3), "codex": sp(2), "glm": nil}},
		{RunID: "r2", TS: "2026-06-03T10:00:00Z", Scope: "cache layer",
			Scores: map[string]core.Score{"claude": sp(2), "codex": sp(3), "glm": sp(1)}},
	}
	reg := core.Registry{Jurors: []core.Juror{
		{Name: "claude", Family: "anthropic", Enabled: true},
		{Name: "codex", Family: "openai", Enabled: true},
		{Name: "glm", Family: "glm", Enabled: true},
	}}
	return core.Aggregate(recs, reg)
}

func TestSparkline(t *testing.T) {
	got := sparkline([]core.Score{sp(0), sp(1), sp(2), sp(3), nil})
	want := "▁▃▅█·"
	if got != want {
		t.Errorf("sparkline = %q, want %q", got, want)
	}
	if sparkline(nil) != "—" {
		t.Errorf("empty series should render em dash")
	}
}

func TestRenderScoreboardStyledVsPlain(t *testing.T) {
	agg := sampleAgg()

	plain := renderScoreboard(agg, false)
	if containsANSI(plain) {
		t.Errorf("plain scoreboard contains ANSI:\n%q", plain)
	}
	// Carries the data: jurors, families, the run count, and computed numbers.
	for _, frag := range []string{"claude", "codex", "glm", "anthropic", "2 scored run", "2.50", "100%"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("plain scoreboard missing %q\n%s", frag, plain)
		}
	}
	// codex avg 2.5, glm participation 50% (rated 1 of 2), claude avg 2.5.
	if !strings.Contains(plain, "50%") {
		t.Errorf("plain scoreboard should show glm 50%% participation\n%s", plain)
	}

	styled := renderScoreboard(agg, true)
	if !containsANSI(styled) {
		t.Errorf("styled scoreboard should contain ANSI")
	}
	// Same data survives styling (after stripping ANSI).
	if !strings.Contains(stripANSI(styled), "claude") {
		t.Errorf("styled scoreboard lost juror name")
	}
}

func TestRenderScoreboardEmptyState(t *testing.T) {
	empty := core.Aggregate(nil, core.Registry{})

	plain := renderScoreboard(empty, false)
	if containsANSI(plain) {
		t.Errorf("plain empty state contains ANSI")
	}
	if !strings.Contains(plain, "No scored runs yet") {
		t.Errorf("empty state should explain there is nothing yet:\n%s", plain)
	}

	styled := renderScoreboard(empty, true)
	if !strings.Contains(stripANSI(styled), "No scored runs yet") {
		t.Errorf("styled empty state missing message")
	}

	// S5: the empty state must show the "Jury scoreboard" header exactly once,
	// not duplicated by renderScoreboard's own title line.
	if n := strings.Count(stripANSI(plain), "Jury scoreboard"); n != 1 {
		t.Errorf("plain empty state shows header %d times, want 1:\n%s", n, plain)
	}
	if n := strings.Count(stripANSI(styled), "Jury scoreboard"); n != 1 {
		t.Errorf("styled empty state shows header %d times, want 1", n)
	}
}

func TestRenderDetailPerRun(t *testing.T) {
	agg := sampleAgg()
	var glm core.JurorStat
	for _, j := range agg.Jurors {
		if j.Name == "glm" {
			glm = j
		}
	}
	plain := renderDetail(agg, glm, false)
	if containsANSI(plain) {
		t.Errorf("plain detail contains ANSI")
	}
	// glm: r1 null, r2 score 1. Both runs + their scopes show.
	for _, frag := range []string{"glm", "null", "auth refactor", "cache layer"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("detail missing %q:\n%s", frag, plain)
		}
	}
}

func TestPadRightWidth(t *testing.T) {
	// ASCII padding to exact width.
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight = %q", got)
	}
	// Truncation adds an ellipsis and keeps within width.
	got := padRight("abcdefgh", 4)
	if got != "abc…" {
		t.Errorf("truncate = %q, want abc…", got)
	}
}
