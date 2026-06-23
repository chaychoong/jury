// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixtureReg is a small registry: claude enabled, codex enabled, glm enabled,
// retired (disabled) minimax. "kimi" deliberately absent from the registry so we
// can test a juror present in the log but not the registry.
func fixtureReg() Registry {
	return Registry{Jurors: []Juror{
		{Name: "claude", Family: "anthropic", Enabled: true},
		{Name: "codex", Family: "openai", Enabled: true},
		{Name: "glm", Family: "glm", Enabled: true},
		{Name: "minimax", Family: "minimax", Enabled: false},
	}}
}

// fixtureLog is a deliberately out-of-order, with-dups log. run "r2" is scored
// twice; the LATER timestamp must win.
func fixtureLog() []ScoreRecord {
	return []ScoreRecord{
		// r1 (oldest): claude 3, codex 2, glm null, minimax 1, kimi 0
		{RunID: "r1", TS: "2026-06-01T10:00:00Z", Scope: "one",
			Scores: map[string]Score{"claude": intp(3), "codex": intp(2), "glm": nil, "minimax": intp(1), "kimi": intp(0)}},
		// r2 FIRST (stale) version — should be overwritten
		{RunID: "r2", TS: "2026-06-02T10:00:00Z", Scope: "two-stale",
			Scores: map[string]Score{"claude": intp(0), "codex": intp(0), "glm": intp(0)}},
		// r3 (newest): claude 2, codex 3, glm 1
		{RunID: "r3", TS: "2026-06-03T10:00:00Z", Scope: "three",
			Scores: map[string]Score{"claude": intp(2), "codex": intp(3), "glm": intp(1)}},
		// r2 SECOND (fresh) version, appended later, with a LATER ts — wins
		{RunID: "r2", TS: "2026-06-02T12:00:00Z", Scope: "two-fresh",
			Scores: map[string]Score{"claude": intp(1), "codex": intp(2), "glm": nil}},
	}
}

func TestLatestPerRunIDDedupsAndOrders(t *testing.T) {
	got := LatestPerRunID(fixtureLog())
	if len(got) != 3 {
		t.Fatalf("want 3 deduped runs, got %d", len(got))
	}
	// Chronological order by ts.
	wantOrder := []string{"r1", "r2", "r3"}
	for i, w := range wantOrder {
		if got[i].RunID != w {
			t.Errorf("run %d = %q, want %q", i, got[i].RunID, w)
		}
	}
	// r2 must be the FRESH version (later ts), not the stale one.
	for _, r := range got {
		if r.RunID == "r2" {
			if r.Scope != "two-fresh" {
				t.Errorf("r2 kept stale record (%q); latest-per-run-id must win", r.Scope)
			}
			if v := r.Scores["claude"]; v == nil || *v != 1 {
				t.Errorf("r2 claude = %v, want fresh value 1", v)
			}
		}
	}
}

func TestLatestPerRunIDTiebreakAppendOrder(t *testing.T) {
	// Identical timestamps: the later-appended record wins.
	recs := []ScoreRecord{
		{RunID: "x", TS: "2026-06-01T00:00:00Z", Scope: "first", Scores: map[string]Score{"a": intp(1)}},
		{RunID: "x", TS: "2026-06-01T00:00:00Z", Scope: "second", Scores: map[string]Score{"a": intp(2)}},
	}
	got := LatestPerRunID(recs)
	if len(got) != 1 || got[0].Scope != "second" {
		t.Fatalf("tie should keep later append; got %+v", got)
	}
}

func TestAggregateAveragesIgnoreNull(t *testing.T) {
	agg := Aggregate(fixtureLog(), fixtureReg())

	byName := map[string]JurorStat{}
	for _, j := range agg.Jurors {
		byName[j.Name] = j
	}

	// claude: appears in r1(3), r2(1), r3(2) → rated 3, sum 6, avg 2.0, no nulls.
	c := byName["claude"]
	if c.Appearances != 3 || c.Rated != 3 || c.Abstained != 0 {
		t.Errorf("claude counts: app=%d rated=%d abstain=%d", c.Appearances, c.Rated, c.Abstained)
	}
	if avg, ok := c.Average(); !ok || avg != 2.0 {
		t.Errorf("claude avg = %v (%v), want 2.0", avg, ok)
	}
	if p, _ := c.Participation(); p != 1.0 {
		t.Errorf("claude participation = %v, want 1.0", p)
	}

	// glm: r1 null, r2 null, r3 1 → appearances 3, rated 1, abstained 2,
	// avg over rated = 1.0 (nulls ignored), participation 1/3.
	g := byName["glm"]
	if g.Appearances != 3 || g.Rated != 1 || g.Abstained != 2 {
		t.Errorf("glm counts: app=%d rated=%d abstain=%d", g.Appearances, g.Rated, g.Abstained)
	}
	if avg, ok := g.Average(); !ok || avg != 1.0 {
		t.Errorf("glm avg = %v (%v), want 1.0 (nulls ignored)", avg, ok)
	}
	if p, _ := g.Participation(); p != 1.0/3.0 {
		t.Errorf("glm participation = %v, want 1/3", p)
	}

	// codex: r1(2), r2(2), r3(3) → avg 7/3.
	cx := byName["codex"]
	if avg, _ := cx.Average(); avg != 7.0/3.0 {
		t.Errorf("codex avg = %v, want 7/3", avg)
	}

	// minimax: only r1(1) → appearances 1, avg 1.0. Disabled in registry but
	// still surfaced because it has history.
	mm := byName["minimax"]
	if mm.Appearances != 1 || mm.Enabled {
		t.Errorf("minimax: app=%d enabled=%v (want app=1, disabled)", mm.Appearances, mm.Enabled)
	}
	if !mm.InRegistry {
		t.Errorf("minimax should be marked InRegistry")
	}

	// kimi: in the log (r1 score 0) but NOT in the registry.
	k := byName["kimi"]
	if k.InRegistry {
		t.Errorf("kimi should NOT be in registry")
	}
	if k.Appearances != 1 || k.Rated != 1 || k.SumRated != 0 {
		t.Errorf("kimi: app=%d rated=%d sum=%d (want 1,1,0)", k.Appearances, k.Rated, k.SumRated)
	}
	// A 0 score is rated (participated, useless) — distinct from null.
	if avg, ok := k.Average(); !ok || avg != 0.0 {
		t.Errorf("kimi avg = %v (%v), want 0.0 rated", avg, ok)
	}
}

func TestAggregateRecentFormChronological(t *testing.T) {
	agg := Aggregate(fixtureLog(), fixtureReg())
	var claude JurorStat
	for _, j := range agg.Jurors {
		if j.Name == "claude" {
			claude = j
		}
	}
	// Series must be chronological: r1=3, r2=1, r3=2.
	want := []int{3, 1, 2}
	if len(claude.Series) != len(want) {
		t.Fatalf("claude series len = %d, want %d", len(claude.Series), len(want))
	}
	for i, w := range want {
		if claude.Series[i] == nil || *claude.Series[i] != w {
			t.Errorf("claude series[%d] = %v, want %d", i, claude.Series[i], w)
		}
	}
	// RecentForm(2) returns the last two entries.
	rf := claude.RecentForm(2)
	if len(rf) != 2 || *rf[0] != 1 || *rf[1] != 2 {
		t.Errorf("claude recent form(2) = %v, want [1 2]", rf)
	}
	// RecentForm beyond length returns the whole series.
	if len(claude.RecentForm(99)) != 3 {
		t.Errorf("recent form should clamp to series length")
	}
}

func TestAggregateSurfacesEnabledJurorWithNoScores(t *testing.T) {
	// Registry has a juror "newbie" that never appears in the log.
	reg := fixtureReg()
	reg.Jurors = append(reg.Jurors, Juror{Name: "newbie", Family: "newco", Enabled: true})

	agg := Aggregate(fixtureLog(), reg)
	var nb *JurorStat
	for i := range agg.Jurors {
		if agg.Jurors[i].Name == "newbie" {
			nb = &agg.Jurors[i]
		}
	}
	if nb == nil {
		t.Fatal("enabled juror with no scores should still appear")
	}
	if nb.Appearances != 0 {
		t.Errorf("newbie appearances = %d, want 0", nb.Appearances)
	}
	if _, ok := nb.Average(); ok {
		t.Errorf("newbie should have no average (no rated runs)")
	}
	if _, ok := nb.Participation(); ok {
		t.Errorf("newbie should have no participation (never appeared)")
	}
}

func TestAggregateSortingRatedFirstByAvg(t *testing.T) {
	agg := Aggregate(fixtureLog(), fixtureReg())
	// First juror should be the highest-average rated one. codex avg 7/3 ≈ 2.33
	// beats claude 2.0.
	if agg.Jurors[0].Name != "codex" {
		t.Errorf("highest avg should sort first; got %q", agg.Jurors[0].Name)
	}
	// Last should be a never-appeared / unrated juror if any; here all appear, so
	// just assert never-appeared (if present) is after rated. Add a newbie.
	reg := fixtureReg()
	reg.Jurors = append(reg.Jurors, Juror{Name: "zzz", Family: "z", Enabled: true})
	agg2 := Aggregate(fixtureLog(), reg)
	if agg2.Jurors[len(agg2.Jurors)-1].Name != "zzz" {
		t.Errorf("never-appeared juror should sort last; got %q", agg2.Jurors[len(agg2.Jurors)-1].Name)
	}
}

func TestAggregateFamilyGrouping(t *testing.T) {
	agg := Aggregate(fixtureLog(), fixtureReg())
	groups := agg.Families()
	got := map[string]int{}
	for _, g := range groups {
		got[g.Family] = len(g.Jurors)
	}
	// kimi has no registry family → "unknown".
	if got["unknown"] != 1 {
		t.Errorf("kimi (no registry) should group under 'unknown'; got %v", got)
	}
	if got["anthropic"] != 1 || got["openai"] != 1 {
		t.Errorf("family grouping wrong: %v", got)
	}
	// Families sorted alphabetically.
	for i := 1; i < len(groups); i++ {
		if groups[i-1].Family > groups[i].Family {
			t.Errorf("families not sorted: %q before %q", groups[i-1].Family, groups[i].Family)
		}
	}
}

func TestReadScoreLogMissingFileIsEmpty(t *testing.T) {
	recs, err := ReadScoreLog(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("missing file should yield 0 records, got %d", len(recs))
	}
	// And Aggregate over it is Empty.
	if !Aggregate(recs, fixtureReg()).Empty() {
		t.Errorf("aggregation over empty log should be Empty()")
	}
}

func TestReadScoreLogRoundTripWithJuryHome(t *testing.T) {
	// Exercise the real on-disk path resolution via JURY_HOME, writing through
	// AppendScoreRecord and reading back through ScoresPath/ReadScoreLog.
	home := t.TempDir()
	t.Setenv("JURY_HOME", home)

	path, err := ScoresPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "jury-scores.jsonl"); path != want {
		t.Fatalf("ScoresPath honoring JURY_HOME = %q, want %q", path, want)
	}

	run := scoreRun()
	rec, _ := BuildScoreRecord(run, []SlotScore{
		{0, intp(1)}, {1, intp(3)}, {2, nil}, {3, intp(3)}, {4, intp(2)},
	}, "fixture", time.Now())
	if err := AppendScoreRecord(path, rec); err != nil {
		t.Fatal(err)
	}

	recs, err := ReadScoreLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	agg := Aggregate(recs, Registry{Jurors: []Juror{
		{Name: "claude", Family: "anthropic", Enabled: true},
	}})
	if agg.Empty() {
		t.Errorf("aggregation should not be empty after appending a record")
	}
}

func TestReadScoreLogSkipsBlankRejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scores.jsonl")
	good := `{"run_id":"r1","ts":"2026-06-01T10:00:00Z","scope":"s","scores":{"a":1}}`
	if err := os.WriteFile(path, []byte(good+"\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadScoreLog(path)
	if err != nil {
		t.Fatalf("blank line should be skipped, not error: %v", err)
	}
	if len(recs) != 1 {
		t.Errorf("want 1 record (blank skipped), got %d", len(recs))
	}

	if err := os.WriteFile(path, []byte(good+"\n{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadScoreLog(path); err == nil {
		t.Errorf("malformed line should surface an error")
	}
}
