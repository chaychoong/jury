// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
)

// safeSlug matches a model/provider slug that cannot be mistaken for a flag by
// the child CLI: it may not start with '-' and is restricted to a conservative
// charset (alphanumerics plus the separators real slugs use, e.g.
// "zai-org/GLM-5.2", "gpt-5.5"). This is the S1 flag-injection guard — a slug
// like "--dangerous" would otherwise be consumed as an option by codex/pi.
var safeSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]*$`)

func validateSlug(kind, slug string) error {
	if !safeSlug.MatchString(slug) {
		return fmt.Errorf("%s %q is not a valid slug (must start alphanumeric, no flag-like leading dash)", kind, slug)
	}
	return nil
}

// reviewPromptTemplate is the adversarial review instruction handed to every
// juror. %s is the material path. The juror reads the material with its
// read-only tools and returns severity-ranked findings.
const reviewPromptTemplate = `You are an adversarial reviewer on a multi-model jury. Independently scrutinize the material at the path below. Read it (and any files it references) using your read-only tools only — do not modify anything.

Material: %s

Find the strongest objections: correctness bugs, security holes, broken assumptions, missing cases, and design or reasoning flaws. Be concrete and cite specific locations. Return concise, severity-ranked findings (most serious first). If you find nothing of substance, say so plainly rather than inventing issues.`

// ReviewPrompt renders the adversarial review prompt for a given material path.
func ReviewPrompt(materialPath string) string {
	return fmt.Sprintf(reviewPromptTemplate, materialPath)
}

// BuildJurorCommand constructs the read-only CLI invocation for a cli-backend
// juror as structured argv (§14): no shell, no string interpolation. The prompt
// and material path are passed as single argv elements. Returns the executable
// name and its argument vector.
//
// Templates (§8):
//
//	codex: codex exec -s read-only --skip-git-repo-check -m <model> "<prompt>"
//	pi:    pi -p --provider <provider> --model <slug> --tools read,grep,find,ls --no-session "<prompt>"
//
// The documented `</dev/null` no-stdin behaviour is preserved NOT here but by
// the caller wiring the child's Stdin to a null reader (see NullStdin).
func BuildJurorCommand(j Juror, materialPath string) (name string, args []string, err error) {
	if j.Backend != BackendCLI {
		return "", nil, fmt.Errorf("slot juror is backend %q, not a cli juror", j.Backend)
	}
	prompt := ReviewPrompt(materialPath)

	switch j.Tool {
	case "codex":
		if err := validateSlug("model", j.Model); err != nil {
			return "", nil, err
		}
		// `--` terminates option parsing so the prompt (and a defensively
		// validated model slug) can never be misread as a flag (S1).
		return "codex", []string{
			"exec",
			"-s", "read-only",
			"--skip-git-repo-check",
			"-m", j.Model,
			"--",
			prompt,
		}, nil
	case "pi":
		if j.Provider == "" {
			return "", nil, errors.New("pi juror has no provider configured")
		}
		if err := validateSlug("provider", j.Provider); err != nil {
			return "", nil, err
		}
		if err := validateSlug("model", j.Model); err != nil {
			return "", nil, err
		}
		// NOTE: pi does not accept a `--` end-of-options separator (it errors
		// "Unknown option: --"), so we rely solely on slug validation above to
		// keep the model/provider from being parsed as a flag (S1). The prompt is
		// the trailing positional and starts with non-dash text.
		return "pi", []string{
			"-p",
			"--provider", j.Provider,
			"--model", j.Model,
			"--tools", "read,grep,find,ls",
			"--no-session",
			prompt,
		}, nil
	default:
		return "", nil, fmt.Errorf("slot juror has unsupported cli tool %q", j.Tool)
	}
}

// NullStdin returns a reader that yields EOF immediately, used as the child
// process Stdin so codex/pi do not block waiting on a TTY (§8/§14). This is the
// structured-argv replacement for a shell `</dev/null` redirect.
func NullStdin() *bytes.Reader {
	return bytes.NewReader(nil)
}

// NewJurorCmd builds an *exec.Cmd for a cli juror with the null stdin already
// wired. Stdout/Stderr are left for the caller to capture.
func NewJurorCmd(j Juror, materialPath string) (*exec.Cmd, error) {
	name, args, err := BuildJurorCommand(j, materialPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = NullStdin()
	return cmd, nil
}
