# AGENTS.md — `jury`

`jury` is a multi-model adversarial review tool. Independent models ("jurors")
review the same material in parallel; their blind spots are decorrelated; a
foreman synthesizes one verdict; a blind scorer rates each juror 0–3 over time.
Works on code, designs, research claims, plans, or arguments.

For *why* it's built this way (the reasoning and trade-offs), see `DESIGN.md`.

## Two runtimes (read this first)

The system deliberately splits across two runtimes — keep the boundary intact:

- **This Go binary (`jury`)** owns everything stateful and security-sensitive:
  the juror registry, the slot shuffle/anonymization, read-only CLI dispatch,
  run files, scoring, and the `tui` dashboard.
- **A Claude Code dynamic workflow** (`plugin/workflows/jury.js`) owns the
  parallel fan-out and the foreman synthesis (a model call). It ships as a Claude
  Code plugin (see `plugin/`): the `/jury` command invokes the bundled workflow
  via the Workflow tool. Workflows cannot touch the filesystem or take human input
  mid-run, which is exactly why orchestration delegates all stateful work to this
  binary.

## Layout

- `cmd/jury/` — CLI (cobra + `charmbracelet/fang`): `main.go`, `commands.go`
  (subcommands), `style.go` + `tui.go` (lipgloss / bubbletea dashboard).
- `core/` — all logic, pure and unit-tested: `registry.go` (TOML roster),
  `paths.go`, `material.go` (path validation), `run.go` (run files, IDs),
  `shuffle.go`, `plan.go` (`start-run` → instruction plan), `command.go`
  (read-only argv construction), `dispatch.go` (`run-juror`), `score.go` (JSONL
  log), `aggregate.go` (dashboard stats).
- `plugin/` — the Claude Code plugin: `commands/jury.md` (the `/jury` entry
  point), `workflows/jury.js` (the orchestration, invoked via the Workflow tool),
  and `.claude-plugin/plugin.json`. The repo root is also a single-plugin
  marketplace (`.claude-plugin/marketplace.json`).
- `scripts/install.sh` — release-binary installer used by the README quickstart.

## Build / test

    go build -o ~/.local/bin/jury ./cmd/jury
    go test ./...
    go vet ./...

## Subcommands

- `start-run --scope <text> --material <path> --json` — shuffle the registry into
  anonymous slots, write the run file, emit the instruction plan.
- `run-juror --run <id> --slot <i>` — resolve slot→juror, exec the read-only CLI,
  relay raw stdout.
- `score <run_id> <slot>=<0..3|null> … [--note]` — resolve slots→models, append a
  JSONL score record.
- `list [--enabled] [--json]` — print the roster. `tui` — dashboard over the log.

## Invariants — do not break

- **Read-only dispatch.** Build CLI commands with `exec.Command(name, args...)` —
  never a shell, never string interpolation. The (untrusted) prompt and material
  path are single argv elements; child stdin is a null reader, not a shell
  `</dev/null`. (`core/command.go`, `core/dispatch.go`)
- **Blindness.** The binary is the *only* holder of the slot→model mapping. No
  command may reveal it before `score` runs. The workflow, foreman, and scorer
  see only slot numbers (the jurors are shuffled per run).
- **Material path** is validated + canonicalized (reject traversal/symlink) —
  `core/material.go`.
- **Machine paths stay clean.** `--json` output is pure JSON (no ANSI/styling);
  `run-juror` stdout is the raw relayed review. lipgloss styling applies only to
  human output on a TTY.
- **`null` ≠ `0`** in scores: `null` = abstained/failed; `0` = participated but
  useless. They are tracked on separate axes by the dashboard.

## Runtime state (lives outside the repo)

- `~/.claude/jury/jurors.toml` — the registry (seeded on first run).
- `~/.claude/.jury-runs/<run_id>.json` — per-run files (slot→juror map, status);
  materialized review inputs land here too.
- `~/.claude/jury-scores.jsonl` — append-only score log (dashboard takes the
  latest record per `run_id`).
- `JURY_HOME` overrides `~/.claude` (used by tests for isolation).

## Hardening notes (resolved — keep them that way)

An early round of the jury reviewing its own code surfaced five gaps; all are now
fixed. They're documented here so the protections aren't accidentally regressed:

- **Concurrent run-file writes.** `SetSlotStatus` takes an exclusive advisory lock
  (`withFileLock`, `core/flock.go`) around the read-modify-write and rewrites the
  file atomically (temp + rename, `core/run.go`), so parallel slots can't drop
  each other's status updates.
- **Dispatch-time material revalidation.** `RunJuror` re-canonicalizes and
  re-validates the stored material path right before exec (`core/dispatch.go`),
  closing the start-run→dispatch TOCTOU window.
- **No `os.Exit` inside `RunE`.** `run-juror` returns a `jurorFailedError`
  sentinel; `main` recognizes it to exit non-zero without `fang` printing over the
  raw marker, so the failure path runs cleanup and stays testable
  (`cmd/jury/main.go`).
- **Atomic score-log append.** `AppendScoreRecord` holds an advisory lock around
  open+write (`core/score.go`), so concurrent `jury score` runs can't tear lines.
- **Run-ID validation.** `ValidateRunID` enforces `^[0-9]+-[a-z0-9]+$`
  (`core/paths.go`) before any path join, rejecting traversal like `--run ../..`.

The locking uses `flock(2)` and is therefore unix-only (`core/flock.go` is
`//go:build unix`); `jury` targets Linux and macOS.
