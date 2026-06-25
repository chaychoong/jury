# jury — Claude Code plugin

Adds the `/jury` command: a multi-model adversarial review that runs independent
model families over the same material behind anonymized slots, has a foreman
synthesize one verdict blind to which model said what, and a blind scorer rate
each juror 0–3 over time.

It bundles the orchestration workflow (`workflows/jury.js`) and invokes it via
the Workflow tool; on older builds it falls back to direct orchestration. See the
[main project](https://github.com/chaychoong/jury) for the full design and the
juror registry format.

## Prerequisite: the `jury` binary

The plugin drives a native `jury` binary — it does **not** install it (plugins
can't ship platform binaries). Install it first and put it on your PATH:

```bash
# install script (Linux/macOS)
curl -fsSL https://raw.githubusercontent.com/chaychoong/jury/main/scripts/install.sh | sh
# or Homebrew
brew install --cask chaychoong/tap/jury
# or from source (needs Go 1.26+)
go install github.com/chaychoong/jury/cmd/jury@latest
```

Then allow it in `~/.claude/settings.json`:

```json
{ "permissions": { "allow": ["Bash(jury:*)"] } }
```

## Use

```
/jury                              # review the current working-copy change
/jury review core/dispatch.go      # specific files
/jury is this retry logic sound?   # a design question
```

Jurors and their backends are configured in `~/.claude/jury/jurors.toml`, seeded
on first `jury list`. See the main README for adding jurors and providing keys.
