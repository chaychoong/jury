# Design notes

Why `jury` is built the way it is — the reasoning and trade-offs behind the
architecture. `README.md` covers *what it does* and *how to use it*; `AGENTS.md`
lists the *invariants*; this document is the *why*, for anyone changing the
design.

---

## 1. The core thesis: decorrelated blind spots

A single reviewer — even a strong model — has blind spots, and when it shares a
family with the author (or with your default assistant), those blind spots
**correlate** with the author's: it tends to bless exactly the mistakes the
author made. A confident review that quietly agrees with the bug is worse than no
review, because it manufactures false assurance.

The whole design follows from one bet: **N model families miss different things,
so their union covers far more than the best single one alone.** Everything else
— anonymization, blind synthesis, blind scoring — exists to make that union
*trustworthy* (no "defer to the big model" bias) and *measurable over time* (which
models actually earn their seat).

This is a tool for when correctness matters more than speed.

---

## 2. Two runtimes, and why not one

The system splits across a Go binary and a Claude Code dynamic workflow:

- **Binary** — registry, shuffle/anonymization, read-only dispatch, run files,
  scoring, dashboard. Everything stateful or security-sensitive.
- **Workflow** (`workflows/jury.js`) — parallel fan-out and the foreman
  *synthesis* (a model call).

Why not a single standalone Go program that also orchestrates and synthesizes?
Because **synthesis is a model call**, and the Claude Code workflow already
provides model-backed agents, parallel fan-out, and a native Claude juror in
one place. A standalone Go jury (call it "scope C") would have to grow its own
model-API client and prompt plumbing for the foreman — fresh infrastructure for
no near-term gain. So we drew the line at **scope B**: the binary owns the
deterministic, stateful, security-sensitive work; the workflow owns
orchestration + synthesis. The binary is built so a future scope-C orchestrator
could reuse its registry/dispatch/scoring core unchanged.

The split also forces a clean contract (next section), because a workflow
**cannot touch the filesystem or take human input mid-run** — so all state must
live behind the binary.

---

## 3. The instruction-plan / slot abstraction

The workflow never learns juror identities. `jury start-run` shuffles the
registry into anonymous **slots** and returns a per-slot *render plan*:

```
{ run_id, count, slots: [
    { slot, mode: "cli",      exec: "run-juror --run … --slot N" },
    { slot, mode: "subagent", model, prompt },
    … ] }
```

The workflow is a **generic executor** of that plan: `cli` slots spawn a thin
wrapper that runs `jury run-juror …`; the `subagent` slot spawns a native Claude
review. This indirection is what lets **heterogeneous backends** — external CLIs
(`codex`, `pi`) and an in-session Claude — coexist under one blind loop. Adding a
backend is a registry entry plus, at most, a new `mode`; the workflow doesn't
change and never sees a model name.

---

## 4. Anonymization: where blindness holds, and where it leaks

Blindness exists so the **synthesizer judges findings on merit**, not on which
model produced them, and so **scores aren't biased** by model identity. Three
mechanisms, each load-bearing:

- **Shuffle, not positional slots.** Slots are shuffled per run. If slot numbers
  mapped positionally to the registry order, the rater could infer "slot 0 =
  Claude" and "blind" rating would be theater. The binary is the *only* holder of
  the slot→model map (in the run file), revealed only when `score` runs.
- **Style-laundering.** The foreman synthesizes the jurors into *one uniform
  voice*; the scorer and any human read that synthesis, not the raw juror prose.
  So a model's recognizable writing style can't de-anonymize it at scoring time.
- **The contained leak (accepted).** The orchestration layer *does* learn slot
  *modes/tiers* — the `subagent`/`opus` slot is inferably Claude. We accept this:
  it lives purely in the plumbing that *executes* slots, and never reaches the
  layer that *judges* (the foreman) or *scores* (the triage agent). Blindness is
  about protecting judgment, and judgment stays blind. Chasing literal
  identity-free orchestration would buy nothing and cost real complexity.

---

## 5. Capture must be neutral (a hard-won rule)

The `Capture` phase turns the scope into review material. It must produce the
**raw** artifact and **never review, summarize, or analyze** it. This was learned
twice the hard way: a capture agent that "helpfully" summarized the code made the
jury review *the summary* (and its mistakes) instead of the actual code.

Two consequences encoded in the capture prompt:
- **No analysis.** Pre-analysis biases every downstream juror and defeats the
  point of independent review.
- **Self-contained material.** Inline the *actual bytes* (a diff, file contents),
  not a description or a bare list of paths — because some jurors cannot open
  external files, and all jurors must review the *identical* artifact.

---

## 6. The foreman: synthesize and attribute, never grade

The foreman merges the jurors' findings, **tags each with its contributing
slot(s)**, ranks by severity, and keeps a short **"contested / not carried
forward"** footnote so a juror's *weak* findings stay visible (they matter for
scoring). It deliberately does **not** score the jurors — grading is a separate,
independent step (§7), because a synthesizer grading its own panel is the
self-assessment bias we're trying to avoid.

**Graceful degradation** is part of the contract: a juror that can't run replies
`JUROR_FAILED`, which is filtered out *before* synthesis (never relayed as if it
were a real verdict) and scored `null`. The panel simply shrinks. Reviews are
aligned to slots by **explicit slot id, not array position**, so a dropped juror
can't shift labels and misattribute findings.

---

## 7. Scoring: Moment B, 0–3, and `null` ≠ `0`

Scoring is the part most likely to be done naively, so the reasoning matters.

- **Moment A vs Moment B.** *Moment A* is the system grading itself (the foreman
  decides who was right) — rejected: it's circular and inherits the synthesizer's
  bias. *Moment B* is judging each juror's findings **against reality** *after*
  the verdict. We use Moment B: an independent, blind triage agent re-checks the
  findings against the actual code/material and rates each slot.
- **Why it's an in-workflow phase.** Originally scoring sat outside the workflow,
  because a workflow can't pause for a human. The moment we **delegated scoring to
  an agent** (not a human), that constraint dissolved — an agent *can* be a phase.
  The payoff: **every run self-scores and logs**, so the score history builds
  consistently instead of only when someone remembers to score.
- **Why 0–3, not 1–5.** A 4-point scale has **no neutral midpoint**, so the rater
  is forced into the lower half (`0` noise / `1` weak) or the upper half (`2`
  solid / `3` decisive). A 1–5 scale lets everything pool at a non-committal "3";
  for a small, personal dataset where you want a decisive signal, forcing the lean
  is more useful than finer gradation.
- **`null` ≠ `0`.** `null` = abstained / failed (didn't participate); `0` =
  participated and was useless. Keeping them distinct lets the dashboard track
  **participation** and **quality** as independent axes — a flaky model dings its
  *participation rate*, not its *quality average*. (This is exactly the signal you
  want for a model that sometimes returns nothing.)
- **Override.** The log is append-only; a human (or the coordinator) can re-run
  `jury score` to log a corrected record. The dashboard takes the latest record
  per `run_id`.

The result is a *data-driven panel*: over time you see which models consistently
catch real bugs versus add noise, instead of guessing.

---

## 8. Read-only is enforced, not requested

Jurors must never modify what they review. "Read-only" by prompt alone is not a
control — a model can ignore it. So enforcement is structural:

- CLI jurors are dispatched with **pinned read-only flags built as structured
  argv** (`exec.Command(name, args...)`), never a shell string and never string
  interpolation, so the model can't widen the tool set or smuggle a flag. Child
  stdin is a null reader.
- The native Claude juror runs with read-only tools only.
- The binary owns this construction, so a juror's read-only-ness can't be
  weakened by editing a prompt.

This also localizes the trust boundary: the untrusted input is the *review
material and the prompt*, which are passed as single argv elements — never as
something a shell parses.

---

## 9. Packaging: a plugin that can't ship its own workflow

`jury` is distributed as a Claude Code plugin (`plugin/`) plus a separately
installed binary. Two platform constraints shaped how:

- **A plugin cannot register a dynamic workflow as a `/name` command, nor
  auto-install one into `~/.claude/workflows/`.** There is no `workflows` key in
  `plugin.json`, and that directory is reserved for user/project-saved workflows.
  So we use the pattern Anthropic's own `code-modernization` plugin uses: the
  workflow ships *inside* the plugin (`plugin/workflows/jury.js`) and a thin
  markdown command (`plugin/commands/jury.md`) invokes it by path via the Workflow
  tool (`scriptPath: "${CLAUDE_PLUGIN_ROOT}/workflows/jury.js"`), with a markdown
  fallback for builds predating the Workflow tool. This is sanctioned-by-example
  rather than by spec, so it could change; if a `workflows` plugin component
  lands, the command wrapper collapses into a declaration.
- **A plugin cannot install the native binary**, so the binary is installed
  separately (script / Homebrew / `go install`) and the workflow calls it by bare
  name (`jury`), which is why it must be on PATH.

**Why we don't ship an auto-approve permission hook.** A plugin *could* bundle a
`PermissionRequest` hook that silently auto-approves `Bash(jury:*)`, so `/jury`
would work with zero setup. We deliberately don't. `jury`'s whole posture is that
the user stays in control of what runs on their machine — read-only dispatch,
validated material paths, an explicit registry. A plugin that grants itself shell
access on install contradicts that, and worse, normalizes accepting plugins that
self-authorize shell commands. The binary is read-only by construction so the
concrete risk is low, but the principle is consent: we ask the user to add one
explicit `Bash(jury:*)` rule rather than granting it for them.

---

## Non-goals / deferred

- **Standalone (non-Claude-Code) orchestration** — possible (scope C), reuses this
  core, not built.
- **Web dashboard** — the aggregation core is UI-agnostic; only a terminal
  (`tui`) frontend exists today.
- **Literal identity-free orchestration** — see §4; the contained leak is an
  accepted trade-off.
