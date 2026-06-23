// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"image/color"
	"io"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// styleActive reports whether human-facing styled output should be rendered to
// the given writer.
//
// It is false whenever:
//   - the caller asked for JSON (asJSON) — the workflow parses that, it must be
//     pure JSON with no ANSI; or
//   - the target writer is not a terminal — piped/redirected output stays plain.
//
// It inspects the actual command output writer (w) rather than os.Stdout (S5),
// so a command whose output is redirected to a non-TTY writer renders plain
// even if the process stdout happens to be a terminal.
//
// The run-juror path never calls into this package's styling at all (its stdout
// is the raw relayed review), so styling cannot leak there regardless.
func styleActive(w io.Writer, asJSON bool) bool {
	if asJSON {
		return false
	}
	type fdWriter interface{ Fd() uintptr }
	f, ok := w.(fdWriter)
	if !ok {
		// A writer with no fd (e.g. a test buffer) is never a terminal.
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// Palette. Colors are adaptive-friendly 256-color codes that lipgloss downgrades
// or drops on limited / non-color terminals.
var (
	colAccent = lipgloss.Color("63")  // indigo — headers / labels
	colMuted  = lipgloss.Color("245") // dim grey — secondary text
	colOK     = lipgloss.Color("78")  // green — enabled / good
	colOff    = lipgloss.Color("203") // red — disabled / null
	colKey    = lipgloss.Color("250") // near-white — values
)

var (
	styHeader   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styLabel    = lipgloss.NewStyle().Foreground(colMuted)
	styValue    = lipgloss.NewStyle().Foreground(colKey)
	styEnabled  = lipgloss.NewStyle().Foreground(colOK)
	styDisabled = lipgloss.NewStyle().Foreground(colMuted)
	styNull     = lipgloss.NewStyle().Foreground(colOff).Italic(true)
	stySlot     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
)

// familyColor maps a juror family to a subtle accent color. Unknown families
// fall back to muted grey.
func familyColor(family string) color.Color {
	switch family {
	case "anthropic":
		return lipgloss.Color("173") // warm tan
	case "openai":
		return lipgloss.Color("36") // teal
	case "glm":
		return lipgloss.Color("69") // blue
	case "minimax":
		return lipgloss.Color("141") // violet
	case "moonshot":
		return lipgloss.Color("215") // amber
	default:
		return colMuted
	}
}
