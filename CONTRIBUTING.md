# Contributing to `jury`

Thanks for your interest in improving `jury`. This is a small, focused tool;
contributions that keep it that way are very welcome.

## Getting started

You'll need **Go 1.26+**. Clone the repo and build:

```bash
go build -o ~/.local/bin/jury ./cmd/jury
```

Before sending a change, make sure all the gates pass:

```bash
gofmt -l .          # must print nothing
go vet ./...
go test -race ./...
go build ./cmd/jury
```

CI runs exactly these on every pull request.

## Where things live

- `cmd/jury/` — the CLI (cobra + fang) and the lipgloss/bubbletea dashboard.
- `core/` — all the logic, pure and unit-tested.
- `workflows/jury.js` — the Claude Code orchestration (a reference copy).

Read **[AGENTS.md](AGENTS.md)** for the architecture and the **invariants** before
making changes, and **[DESIGN.md](DESIGN.md)** for *why* it's built this way.

## Invariants — please don't break these

These are the load-bearing guarantees of the tool. A change that weakens one
needs a very good reason and a clear explanation:

- **Read-only dispatch.** Build juror commands with `exec.Command(name, args...)` —
  never a shell, never string interpolation. The untrusted prompt and material
  path stay single argv elements.
- **Blindness.** The binary is the *only* holder of the slot→model mapping, and
  nothing may reveal it before `score` runs.
- **Material path** is validated and canonicalized (traversal/symlink rejected).
- **Machine output stays clean.** `--json` is pure JSON; `run-juror` stdout is
  the raw relayed review. Styling is only for human output on a TTY.
- **`null` ≠ `0`** in scores: `null` means abstained/failed, `0` means
  participated but useless. They live on separate axes.

## Pull requests

- Keep changes focused; one logical change per PR.
- Add or update tests for behavior you change — the `core/` package is
  table-driven and well covered; match that style.
- Write clear commit messages that explain the *why*, not just the *what*.
- Run the gates above before pushing.

If you're planning something substantial, open an issue first so we can agree on
the approach before you invest the time.

## License

`jury` is licensed under the **Mozilla Public License 2.0**. By contributing, you
agree that your contributions are licensed under the same terms. New source files
should carry the standard MPL 2.0 header (see any existing `.go` file).
