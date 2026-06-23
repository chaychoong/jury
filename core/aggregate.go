// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"
)

// RecentFormK is the default window for "recent form" (last K rated/seen runs).
const RecentFormK = 10

// ReadScoreLog reads the JSONL score log at path and returns every record in
// file order. A missing file is NOT an error — it returns an empty slice (the
// empty-state case). Blank lines are skipped; a malformed line is an error so
// corruption is surfaced rather than silently dropped.
func ReadScoreLog(path string) ([]ScoreRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open score log %s: %w", path, err)
	}
	defer f.Close()

	var recs []ScoreRecord
	sc := bufio.NewScanner(f)
	// Allow long lines (notes / many jurors).
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(trimSpace(b)) == 0 {
			continue
		}
		var rec ScoreRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			return nil, fmt.Errorf("score log %s line %d: %w", path, line, err)
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read score log %s: %w", path, err)
	}
	return recs, nil
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\r' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}

// LatestPerRunID dedups records by run_id, keeping the LATEST record for each
// run (§15: "Dashboard takes the latest record per run_id"). Latest is decided
// by parsed timestamp; when two records for a run_id carry an identical (or
// unparseable) timestamp, the one appearing later in the input wins (append
// order is the tiebreaker, consistent with an append-only log).
//
// The returned slice is sorted chronologically by timestamp (ascending), then
// by run_id for stability — this is the canonical ordering all aggregations
// build on (trend, recent form).
func LatestPerRunID(recs []ScoreRecord) []ScoreRecord {
	type entry struct {
		rec   ScoreRecord
		ts    time.Time
		tsOK  bool
		order int
	}
	latest := map[string]entry{}
	for i, r := range recs {
		ts, tsOK := parseTS(r.TS)
		cand := entry{rec: r, ts: ts, tsOK: tsOK, order: i}
		prev, ok := latest[r.RunID]
		if !ok || candidateNewer(cand.ts, cand.tsOK, cand.order, prev.ts, prev.tsOK, prev.order) {
			latest[r.RunID] = cand
		}
	}

	out := make([]entry, 0, len(latest))
	for _, e := range latest {
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ei, ej := out[i], out[j]
		// Records with parseable timestamps sort by time; unparseable ones sort
		// last but keep their append order among themselves.
		switch {
		case ei.tsOK && ej.tsOK:
			if !ei.ts.Equal(ej.ts) {
				return ei.ts.Before(ej.ts)
			}
		case ei.tsOK != ej.tsOK:
			return ei.tsOK // ok timestamps before un-ok ones
		}
		if ei.order != ej.order {
			return ei.order < ej.order
		}
		return ei.rec.RunID < ej.rec.RunID
	})

	res := make([]ScoreRecord, len(out))
	for i, e := range out {
		res[i] = e.rec
	}
	return res
}

func candidateNewer(cts time.Time, cOK bool, cOrder int, pts time.Time, pOK bool, pOrder int) bool {
	switch {
	case cOK && pOK:
		if cts.Equal(pts) {
			return cOrder >= pOrder // later append wins on tie
		}
		return cts.After(pts)
	case cOK && !pOK:
		return true // a real timestamp beats an unparseable one
	case !cOK && pOK:
		return false
	default:
		return cOrder >= pOrder // both unparseable: later append wins
	}
}

func parseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// JurorStat is the aggregated dashboard view for one juror.
type JurorStat struct {
	Name   string
	Family string
	// Enabled reflects the registry; a juror may be enabled with no scores yet
	// (Appearances == 0) or appear in history while now disabled/removed.
	Enabled bool
	// InRegistry is false for a juror that appears in the log but is no longer in
	// the registry (family is then unknown).
	InRegistry bool

	// Appearances is the number of (deduped) runs the juror appears in (any value,
	// including null).
	Appearances int
	// Rated is the count of non-null scores; Abstained is the count of nulls.
	Rated     int
	Abstained int
	// SumRated is the total of non-null scores; Average = SumRated/Rated.
	SumRated int

	// Series is this juror's score per appearance in chronological order. A nil
	// entry is an abstention/failure (null). Used for trend + recent form.
	Series []Score
}

// Average is the mean over rated (non-null) runs. The bool is false when the
// juror has no rated runs (avoid a divide-by-zero / meaningless 0.0).
func (s JurorStat) Average() (float64, bool) {
	if s.Rated == 0 {
		return 0, false
	}
	return float64(s.SumRated) / float64(s.Rated), true
}

// Participation is Rated/Appearances in [0,1]. The bool is false when the juror
// never appeared.
func (s JurorStat) Participation() (float64, bool) {
	if s.Appearances == 0 {
		return 0, false
	}
	return float64(s.Rated) / float64(s.Appearances), true
}

// RecentForm returns the last k entries of the series (chronological), or all
// of them when fewer than k exist.
func (s JurorStat) RecentForm(k int) []Score {
	if k <= 0 || len(s.Series) <= k {
		return s.Series
	}
	return s.Series[len(s.Series)-k:]
}

// Aggregation is the full dashboard dataset.
type Aggregation struct {
	// Jurors holds one stat per juror, sorted by average descending (rated jurors
	// first), then by name. Includes registry jurors with no scores yet.
	Jurors []JurorStat
	// Runs is the deduped, chronologically-sorted record set the stats derive
	// from (latest-per-run-id). Empty when there is no history.
	Runs []ScoreRecord
	// FirstTS / LastTS bound the history; zero when there are no runs.
	FirstTS time.Time
	LastTS  time.Time
}

// Empty reports whether there is no scored history to show.
func (a Aggregation) Empty() bool { return len(a.Runs) == 0 }

// Families groups the juror stats by family, returning family names sorted
// alphabetically and, per family, its jurors in the same order as a.Jurors.
func (a Aggregation) Families() []FamilyGroup {
	idx := map[string]int{}
	var groups []FamilyGroup
	for _, j := range a.Jurors {
		fam := j.Family
		if fam == "" {
			fam = "unknown"
		}
		gi, ok := idx[fam]
		if !ok {
			idx[fam] = len(groups)
			groups = append(groups, FamilyGroup{Family: fam})
			gi = len(groups) - 1
		}
		groups[gi].Jurors = append(groups[gi].Jurors, j)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Family < groups[j].Family })
	return groups
}

// FamilyGroup is one family's jurors.
type FamilyGroup struct {
	Family string
	Jurors []JurorStat
}

// Aggregate builds the dashboard dataset from a raw score log and the registry.
// It dedups to latest-per-run-id, walks runs chronologically to build each
// juror's series, and folds in registry jurors so an enabled juror with no
// scores yet still appears (Appearances == 0).
//
// This is the single source of truth for tui (and a future serve); all metrics
// (average ignoring null, participation counts, recent-form ordering) come from
// here so they can be unit-tested without a TTY.
func Aggregate(recs []ScoreRecord, reg Registry) Aggregation {
	runs := LatestPerRunID(recs)

	// Stable per-juror accumulation. We discover jurors from both the registry
	// (so empty-but-enabled jurors show) and the log (so retired jurors still
	// surface their history).
	stats := map[string]*JurorStat{}
	var order []string // first-seen order, registry first then log

	ensure := func(name string) *JurorStat {
		if s, ok := stats[name]; ok {
			return s
		}
		s := &JurorStat{Name: name}
		stats[name] = s
		order = append(order, name)
		return s
	}

	for _, j := range reg.Jurors {
		s := ensure(j.Name)
		s.Family = j.Family
		s.Enabled = j.Enabled
		s.InRegistry = true
	}

	// Walk runs in chronological order so Series is chronological.
	for _, r := range runs {
		// Deterministic per-run juror iteration for stable series across equal
		// timestamps is unnecessary (series entries are per-juror), but we sort
		// the keys so behaviour is reproducible.
		for name, sc := range r.Scores {
			s := ensure(name)
			s.Appearances++
			s.Series = append(s.Series, sc)
			if sc == nil {
				s.Abstained++
			} else {
				s.Rated++
				s.SumRated += *sc
			}
		}
	}

	out := make([]JurorStat, 0, len(order))
	for _, name := range order {
		out = append(out, *stats[name])
	}

	// Sort: jurors with rated runs first (by average desc), then jurors that
	// appeared but are unrated, then never-appeared registry jurors; name breaks
	// ties throughout for stability.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aAvg, aOK := a.Average()
		bAvg, bOK := b.Average()
		if aOK != bOK {
			return aOK // rated jurors first
		}
		if aOK && bOK && aAvg != bAvg {
			return aAvg > bAvg
		}
		// Among unrated, those who at least appeared rank above never-appeared.
		if (a.Appearances > 0) != (b.Appearances > 0) {
			return a.Appearances > 0
		}
		return a.Name < b.Name
	})

	agg := Aggregation{Jurors: out, Runs: runs}
	if len(runs) > 0 {
		if t, ok := parseTS(runs[0].TS); ok {
			agg.FirstTS = t
		}
		if t, ok := parseTS(runs[len(runs)-1].TS); ok {
			agg.LastTS = t
		}
	}
	return agg
}
