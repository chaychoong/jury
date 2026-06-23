// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/chaychoong/jury/core"
	"github.com/spf13/cobra"
)

// newTuiCmd builds the `jury tui` dashboard command.
//
// On a real TTY it launches an interactive Bubble Tea program. When stdout is
// not a terminal (piped/redirected, e.g. `jury tui | cat`), it degrades to a
// single static render of the scoreboard and exits 0 — so the board is
// scriptable and testable without driving a PTY.
func newTuiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Dashboard over the score log (juror averages, participation, recent form)",
		Long: "Open a Bubble Tea dashboard summarising the jury score history: per-juror average, " +
			"participation, recent form, and trend, grouped by family. On a non-TTY stdout it prints " +
			"the scoreboard once and exits, so `jury tui | cat` works for scripting.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agg, err := loadAggregation()
			if err != nil {
				return err
			}

			// Non-TTY: static render, no interactive program.
			if !styleActive(cmd.OutOrStdout(), false) {
				fmt.Fprint(cmd.OutOrStdout(), renderScoreboard(agg, false))
				return nil
			}

			// TTY: interactive dashboard.
			m := newDashboardModel(agg)
			p := tea.NewProgram(m)
			_, err = p.Run()
			return err
		},
	}
	return cmd
}

// loadAggregation reads the score log (honoring JURY_HOME) and the registry,
// then folds them into the dashboard dataset. A missing log is not an error —
// it produces an empty aggregation that renders the empty-state panel.
func loadAggregation() (core.Aggregation, error) {
	scoresPath, err := core.ScoresPath()
	if err != nil {
		return core.Aggregation{}, err
	}
	recs, err := core.ReadScoreLog(scoresPath)
	if err != nil {
		return core.Aggregation{}, err
	}
	// The registry is best-effort: a juror missing from it just lacks a family.
	// If the registry can't load at all, still show the log-derived stats.
	reg, _ := loadRegistry()
	return core.Aggregate(recs, reg), nil
}

// ---------------------------------------------------------------------------
// Pure rendering — shared by the static (non-TTY) path and the TUI body, and
// unit-tested directly.
// ---------------------------------------------------------------------------

// sparkBlocks maps a 0..3 score to a rising block glyph. Index 0..3 = score;
// a null (abstain/fail) renders as a dim middle dot via sparkline().
var sparkBlocks = []rune{'▁', '▃', '▅', '█'}

// sparkline renders a recent-form series as block glyphs. A nil entry (null /
// abstain) becomes a low dot so a gap is visible without implying a 0 score.
func sparkline(series []core.Score) string {
	if len(series) == 0 {
		return "—"
	}
	var b strings.Builder
	for _, s := range series {
		if s == nil {
			b.WriteRune('·')
			continue
		}
		v := *s
		if v < 0 {
			v = 0
		}
		if v > 3 {
			v = 3
		}
		b.WriteRune(sparkBlocks[v])
	}
	return b.String()
}

// sbCol is one fixed column of the scoreboard's main table.
type sbCol struct {
	title string
	width int
}

var scoreboardCols = []sbCol{
	{"juror", 11},
	{"family", 10},
	{"avg", 5},
	{"rated/seen", 11},
	{"part%", 6},
	{"recent", 12},
}

// scoreboardRow holds the formatted cells for one juror.
func scoreboardRow(j core.JurorStat) []string {
	avg := "—"
	if a, ok := j.Average(); ok {
		avg = fmt.Sprintf("%.2f", a)
	}
	ratedSeen := fmt.Sprintf("%d/%d", j.Rated, j.Appearances)
	part := "—"
	if p, ok := j.Participation(); ok {
		part = fmt.Sprintf("%d%%", int(p*100+0.5))
	}
	fam := j.Family
	if fam == "" {
		fam = "unknown"
	}
	name := j.Name
	if !j.InRegistry {
		name += "*" // marks a juror with history but no current registry entry
	}
	return []string{
		name,
		fam,
		avg,
		ratedSeen,
		part,
		sparkline(j.RecentForm(core.RecentFormK)),
	}
}

// renderScoreboard produces the full dashboard body. When styled is false it
// emits plain aligned text (non-TTY / piped); when true it carries lipgloss
// styling. Both render the identical data so the piped board matches the TUI.
func renderScoreboard(agg core.Aggregation, styled bool) string {
	// Empty state renders its own "Jury scoreboard" header, so return before
	// writing the title here to avoid duplicating it (S5).
	if agg.Empty() {
		return renderEmptyState(styled)
	}

	var b strings.Builder

	title := "Jury scoreboard"
	if styled {
		b.WriteString(styHeader.Render(title) + "\n")
	} else {
		b.WriteString(title + "\n")
	}

	// Summary line.
	summary := fmt.Sprintf("%d scored run(s)", len(agg.Runs))
	if !agg.FirstTS.IsZero() && !agg.LastTS.IsZero() {
		summary += fmt.Sprintf(" · %s → %s",
			agg.FirstTS.Format("2006-01-02"), agg.LastTS.Format("2006-01-02"))
	}
	if styled {
		b.WriteString(styLabel.Render(summary) + "\n\n")
	} else {
		b.WriteString(summary + "\n\n")
	}

	// Header row.
	b.WriteString(renderRow(headerCells(), styled, true, "") + "\n")

	for _, j := range agg.Jurors {
		cells := scoreboardRow(j)
		famColorKey := j.Family
		b.WriteString(renderRow(cells, styled, false, famColorKey) + "\n")
	}

	// Legend.
	legend := "scale 0–3 · recent form ▁▃▅█ (· = abstain/fail) · * = not in registry"
	if styled {
		b.WriteString("\n" + styLabel.Render(legend) + "\n")
	} else {
		b.WriteString("\n" + legend + "\n")
	}
	return b.String()
}

func headerCells() []string {
	out := make([]string, len(scoreboardCols))
	for i, c := range scoreboardCols {
		out[i] = c.title
	}
	return out
}

// renderRow pads each cell to its column width and joins them. header rows and
// styled data rows get color treatment; the family dot is colored for data
// rows by famColorKey.
func renderRow(cells []string, styled, header bool, famColorKey string) string {
	var parts []string
	for i, c := range cells {
		w := 10
		if i < len(scoreboardCols) {
			w = scoreboardCols[i].width
		}
		cell := padRight(c, w)
		if styled {
			switch {
			case header:
				cell = styLabel.Render(cell)
			case i == 0: // juror name in its family color
				cell = lipgloss.NewStyle().Bold(true).Foreground(familyColor(famColorKey)).Render(cell)
			case i == 1: // family
				cell = lipgloss.NewStyle().Foreground(familyColor(famColorKey)).Render(cell)
			default:
				cell = styValue.Render(cell)
			}
		}
		parts = append(parts, cell)
	}
	return "  " + strings.Join(parts, " ")
}

// padRight pads s with spaces to visible width w (truncating with an ellipsis
// when longer). Uses lipgloss width to respect wide runes in the sparkline.
func padRight(s string, w int) string {
	vw := lipgloss.Width(s)
	if vw == w {
		return s
	}
	if vw > w {
		// Truncate to w-1 visible columns + ellipsis.
		if w <= 1 {
			return string([]rune(s)[:max(0, w)])
		}
		runes := []rune(s)
		for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
			runes = runes[:len(runes)-1]
		}
		return string(runes) + "…"
	}
	return s + strings.Repeat(" ", w-vw)
}

// renderEmptyState is the clean panel shown when there is no scored history.
func renderEmptyState(styled bool) string {
	msg := "No scored runs yet — run /jury to populate"
	if !styled {
		return "Jury scoreboard\n\n" + msg + "\n"
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colMuted).
		Padding(1, 3).
		Foreground(colKey)
	header := styHeader.Render("Jury scoreboard")
	hint := styLabel.Render("Scores land in ~/.claude/jury-scores.jsonl after a /jury run is triaged.")
	return header + "\n\n" + box.Render(msg) + "\n\n" + hint + "\n"
}

// renderDetail shows one juror's per-run scores + the run scope/note, used by
// the TUI detail view (and testable on its own).
func renderDetail(agg core.Aggregation, j core.JurorStat, styled bool) string {
	var b strings.Builder
	heading := fmt.Sprintf("%s · %s", j.Name, famOrUnknown(j.Family))
	if styled {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(familyColor(j.Family)).Render(heading) + "\n")
	} else {
		b.WriteString(heading + "\n")
	}

	avg := "—"
	if a, ok := j.Average(); ok {
		avg = fmt.Sprintf("%.2f", a)
	}
	stat := fmt.Sprintf("avg %s · rated %d · abstained %d · appearances %d",
		avg, j.Rated, j.Abstained, j.Appearances)
	if styled {
		b.WriteString(styLabel.Render(stat) + "\n\n")
	} else {
		b.WriteString(stat + "\n\n")
	}

	// Per-run scores, most recent first.
	rows := jurorRuns(agg, j.Name)
	if len(rows) == 0 {
		if styled {
			b.WriteString(styLabel.Render("No runs yet.") + "\n")
		} else {
			b.WriteString("No runs yet.\n")
		}
		return b.String()
	}
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		score := "null"
		if r.score != nil {
			score = fmt.Sprintf("%d", *r.score)
		}
		line := fmt.Sprintf("  %s  %-4s  %s", r.date, score, r.scope)
		if r.note != "" {
			line += "  — " + r.note
		}
		if styled {
			scoreStyled := styValue.Render(fmt.Sprintf("%-4s", score))
			if r.score == nil {
				scoreStyled = styNull.Render(fmt.Sprintf("%-4s", "null"))
			}
			line = "  " + styLabel.Render(r.date) + "  " + scoreStyled + "  " + styValue.Render(r.scope)
			if r.note != "" {
				line += styLabel.Render("  — " + r.note)
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func famOrUnknown(f string) string {
	if f == "" {
		return "unknown"
	}
	return f
}

type detailRow struct {
	date  string
	score core.Score
	scope string
	note  string
}

// jurorRuns extracts the per-run rows (chronological) for one juror.
func jurorRuns(agg core.Aggregation, name string) []detailRow {
	var out []detailRow
	for _, r := range agg.Runs {
		sc, ok := r.Scores[name]
		if !ok {
			continue
		}
		date := r.TS
		if len(date) >= 10 {
			date = date[:10]
		}
		out = append(out, detailRow{date: date, score: sc, scope: r.Scope, note: r.Note})
	}
	return out
}

// ---------------------------------------------------------------------------
// Bubble Tea interactive model (TTY only).
// ---------------------------------------------------------------------------

type dashboardModel struct {
	agg      core.Aggregation
	cursor   int // selected juror index
	detail   bool
	quitting bool
}

func newDashboardModel(agg core.Aggregation) dashboardModel {
	return dashboardModel{agg: agg}
}

func (m dashboardModel) Init() tea.Cmd { return nil }

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if m.detail {
				m.detail = false
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if !m.detail && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if !m.detail && m.cursor < len(m.agg.Jurors)-1 {
				m.cursor++
			}
		case "enter", "right", "l":
			if !m.detail && len(m.agg.Jurors) > 0 {
				m.detail = true
			}
		case "left", "h":
			m.detail = false
		}
	}
	return m, nil
}

func (m dashboardModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if m.agg.Empty() {
		return tea.NewView(renderEmptyState(true) + "\n" + footerHelp(false))
	}
	if m.detail && m.cursor < len(m.agg.Jurors) {
		body := renderDetail(m.agg, m.agg.Jurors[m.cursor], true)
		return tea.NewView(body + "\n" + footerHelp(true))
	}
	return tea.NewView(renderInteractiveBoard(m) + "\n" + footerHelp(false))
}

// renderInteractiveBoard reuses the styled scoreboard but highlights the
// selected row with a marker so the user can see the cursor.
func renderInteractiveBoard(m dashboardModel) string {
	var b strings.Builder
	b.WriteString(styHeader.Render("Jury scoreboard") + "\n")

	summary := fmt.Sprintf("%d scored run(s)", len(m.agg.Runs))
	if !m.agg.FirstTS.IsZero() && !m.agg.LastTS.IsZero() {
		summary += fmt.Sprintf(" · %s → %s",
			m.agg.FirstTS.Format("2006-01-02"), m.agg.LastTS.Format("2006-01-02"))
	}
	b.WriteString(styLabel.Render(summary) + "\n\n")
	b.WriteString("  " + renderRow(headerCells(), true, true, "") + "\n")

	for i, j := range m.agg.Jurors {
		row := renderRow(scoreboardRow(j), true, false, j.Family)
		marker := "  "
		if i == m.cursor {
			marker = stySlot.Render("▸ ")
		}
		b.WriteString(marker + row + "\n")
	}
	return b.String()
}

func footerHelp(detail bool) string {
	var keys string
	if detail {
		keys = "←/h back · q quit"
	} else {
		keys = "↑/↓ select · →/enter detail · q quit"
	}
	return styLabel.Render(keys)
}
