# jury

A multi-model **adversarial review jury**. Several independent models — different
families, different blind spots — review the same material in parallel, behind
anonymized "slots." A foreman synthesizes their findings into one verdict, blind
to which model said what. A blind scorer then rates each juror, so over time you
learn which models actually earn their seat.

Works on a code diff, a whole file or codebase, a design, a research claim, a
plan, or a plain question.

---

## Quickstart

**Prerequisite:** [Claude Code](https://www.claude.com/product/claude-code) (for the `/jury` command). No Go toolchain needed unless you build from source.

**1. Install the `jury` binary** (pick one):

```bash
# install script (Linux/macOS) — downloads the latest release to ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/chaychoong/jury/main/scripts/install.sh | sh

# or Homebrew
brew install --cask chaychoong/tap/jury

# or from source (needs Go 1.26+)
go install github.com/chaychoong/jury/cmd/jury@latest
```

Make sure it's on your PATH, then seed the roster:

```bash
jury list            # seeds ~/.claude/jury/jurors.toml with the default roster
```

**2. Install the plugin** so Claude Code gets the `/jury` command:

```
/plugin marketplace add chaychoong/jury
/plugin install jury@jury
```
> Prefer not to use the plugin? Install the workflow file directly instead:
> `curl -fsSL https://raw.githubusercontent.com/chaychoong/jury/main/plugin/workflows/jury.js -o ~/.claude/workflows/jury.js`

**3. Allow the binary** in `~/.claude/settings.json` (so the workflow may run it
— the child `pi`/`codex` it spawns are subprocesses, not separately gated):

```json
{ "permissions": { "allow": ["Bash(jury:*)"] } }
```

**4. Give the jurors their backends** (see the registry, `~/.claude/jury/jurors.toml`):

| Juror | Backend | Needs |
|---|---|---|
| `claude` | native subagent | nothing (in-session Opus) |
| `codex`  | `codex` CLI | `codex` installed + authenticated |
| `glm` / `minimax` / `kimi` | `pi` CLI | `pi` installed + a provider key (these ship pointed at OpenRouter — key in `~/.pi/agent/auth.json`) |

**5. Review something** — in Claude Code:

```
/jury                              # reviews the current working-copy change
/jury review src/auth.ts          # a file
/jury is this retry logic sound?  # a design question
```

You get one synthesized verdict plus an auto-scored line, and the run is logged.

**6. See the scoreboard**

```bash
jury tui            # interactive dashboard (↑/↓ select, ↵ detail, q quit)
jury tui | cat      # static one-shot table
```

---

## Examples

Review the staged/working change and let the panel pressure-test it:
```
/jury
```

Review specific code with a focus:
```
/jury review core/dispatch.go and core/command.go — focus on the read-only command construction and any shell-injection risk
```

Cross-check a non-code claim:
```
/jury Is it true that float("0.1")*100 truncated to int loses a cent? Verify.
```

Score a run by hand (the workflow does this automatically, but you can override —
the log is append-only, latest record per `run_id` wins):
```bash
jury score 1782153199-fmhki 0=3 1=2 2=null 3=1 4=2 --note "slot 0 caught the TOCTOU"
```

Inspect the roster / a run plan directly:
```bash
jury list --json
jury start-run --scope "demo" --material ~/.claude/.jury-runs/demo.txt --json
```

---

## Tutorial: add a juror

The registry (`~/.claude/jury/jurors.toml`) is the single source of truth — add a
block, and both dispatch and scoring pick it up. No workflow edits, no rebuild
(the binary reads it at runtime).

```toml
[[juror]]
name     = "deepseek"
backend  = "cli"
tool     = "pi"
model    = "deepseek/deepseek-chat"
provider = "openrouter"
family   = "deepseek"
enabled  = true
```

- `backend = "cli"` jurors are dispatched by `jury run-juror` (read-only, pinned
  flags). `tool` selects the CLI (`pi` or `codex`); `provider` + `model` address
  the model.
- `backend = "subagent"` is the native Claude path (no CLI); only `model` (e.g.
  `opus`) and `family` apply.
- `enabled = false` benches a juror without deleting it.
- `family` groups jurors in the dashboard.

Next `/jury` run includes it; the scoreboard starts tracking it once it's scored.

---

## Why

A good reviewer catches what the author missed. But a *single* reviewer — even a
strong model — has its own blind spots, and if it's from the same family as the
author (or as your default assistant), those blind spots **correlate**: it tends
to bless exactly the mistakes the author would. You get a confident review that
quietly agrees with the bug.

`jury` attacks that with **decorrelation**:

- **Model diversity.** GPT, Claude, GLM, MiniMax, and Kimi miss *different*
  things. Run them as independent jurors and the union of their findings covers
  far more than any one — including the strongest one — would alone.
- **Blind synthesis.** The foreman that merges the verdicts never learns which
  model produced which finding (jurors are shuffled into anonymous slots each
  run). It judges findings on evidence, not on "trust the big model."
- **Blind, independent scoring.** After each run a separate judge rates every
  juror 0–3 against reality — also blind to identity — and appends it to a log.
  The result is a *data-driven* panel: you can see which models consistently
  catch real bugs and which just add noise, instead of guessing.

It's the tool for the moment when correctness matters more than speed — before
merging a non-trivial change, or pressure-testing a design or claim you're about
to commit to.

---

## How it works

```
/jury <scope>
  capture     an agent turns the scope into concrete review material
  start-run   the jury binary shuffles the registry into anonymous slots 0..N
  jury        each slot runs in parallel, blind — a CLI juror or a native one
  verdict     the foreman synthesizes one slot-attributed, severity-ranked verdict
  triage+score a blind judge checks findings vs reality, scores each slot 0–3, logs it
```

Two runtimes cooperate:

- **The `jury` Go binary** owns everything stateful and security-sensitive: the
  juror **registry**, the **shuffle/anonymization**, **read-only** CLI dispatch
  (structured argv, no shell), the per-run files, **scoring**, and the dashboard.
- **A Claude Code dynamic workflow** (`plugin/workflows/jury.js`) owns the parallel
  fan-out and the foreman **synthesis**. It only ever sees opaque slot numbers —
  the binary holds the slot→model mapping and reveals it only at score time.

The jurors are **read-only by construction**: CLI jurors run with pinned
read-only flags built as structured argv (never a shell); the native Claude juror
runs with read-only tools.

> Why it's built this way — decorrelated blind spots, the anonymization scheme,
> the scoring philosophy — is in **[DESIGN.md](DESIGN.md)**.

---

## Architecture reference

```
cmd/jury/      CLI (cobra + fang): commands, lipgloss styling, bubbletea tui
core/          registry · paths · material validation · run files · shuffle ·
               instruction plan · read-only command construction · dispatch ·
               scoring · dashboard aggregation   (pure, unit-tested)
plugin/        the Claude Code plugin: commands/jury.md + workflows/jury.js
scripts/       install.sh — release-binary installer
```

Subcommands (most are called *by* the workflow, not you):

| command | role |
|---|---|
| `start-run` | shuffle registry → slots, write run file, emit instruction plan |
| `run-juror` | resolve a slot → juror, exec the read-only CLI, relay raw stdout |
| `score` | resolve slots→models via the run file, append a JSONL score record |
| `list` | print the roster |
| `tui` | dashboard over the score log |

Runtime state (outside the repo): `~/.claude/jury/jurors.toml` (registry),
`~/.claude/.jury-runs/` (per-run files + materialized inputs),
`~/.claude/jury-scores.jsonl` (append-only score log). `JURY_HOME` overrides
`~/.claude`.

---

## Status & limitations

Working end to end: registry-driven blind jurors, parallel fan-out, anonymized
synthesis, blind 0–3 scoring, and the dashboard. The concurrency and path-safety
gaps an early self-review surfaced (run-file write race, dispatch-time
revalidation, score-log atomicity, run-ID validation) are fixed and documented in
`AGENTS.md`. `jury` targets Linux and macOS — the file locking is `flock(2)`-based.
A standalone (non-Claude-Code) orchestrator and a web dashboard are possible
future directions; today orchestration + synthesis run in the Claude Code workflow.
