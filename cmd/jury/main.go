// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command jury is the multi-model adversarial review tool. It owns the juror
// registry, slot shuffle/anonymization, read-only CLI dispatch, run files, and
// scoring. Orchestration + synthesis live in the Claude Code workflow.
package main

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/charmbracelet/fang"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := newRootCmd()
	if err := fang.Execute(
		context.Background(),
		root,
		fang.WithVersion(version),
		// A failed juror has already printed its raw "JUROR_FAILED: <reason>"
		// marker to stdout (run-juror's contract); suppress fang's styled error
		// for that sentinel so nothing is layered on top. fang still returns the
		// error, so we exit non-zero below.
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			var jf *jurorFailedError
			if errors.As(err, &jf) {
				return
			}
			fang.DefaultErrorHandler(w, styles, err)
		}),
	); err != nil {
		os.Exit(1)
	}
}
