# Security Policy

## Supported versions

`jury` is pre-1.0. Security fixes land on `main` and in the latest tagged
release. Older tags are not maintained — track the latest release.

## Reporting a vulnerability

Please report security issues **privately**, not through public issues or pull
requests:

- Email **hi@chay.dev**, or
- Open a private advisory via GitHub's
  **Security → Report a vulnerability** on this repository.

Include enough detail to reproduce (version/commit, configuration, and steps).
You'll get an acknowledgement within a few days. Please give a reasonable window
to ship a fix before any public disclosure.

## Threat model — what you are trusting

`jury` is a local orchestration tool. Understanding its boundaries matters,
because some of the trust sits with *you* and with the tools you configure.

**Jurors are arbitrary programs you choose to run.** The roster in
`~/.claude/jury/jurors.toml` maps each juror to a CLI executable. `jury` runs
those executables on your machine with your privileges. It can only be as
trustworthy as the binaries you put in the roster — **only add jurors you
trust.**

**Dispatch is read-only by construction, on `jury`'s side.** Commands are built
with `exec.Command(name, args...)` — never via a shell, never with string
interpolation. The (untrusted) prompt and material path are passed as single
argv elements, and the child's stdin is an empty reader. This prevents `jury`
itself from being tricked into shell injection. It does **not** sandbox the
juror tool: once invoked, a juror decides for itself what it reads, writes, or
sends over the network. `jury` cannot constrain a juror that chooses to do more
than review.

**Your material is sent to the backends you configured.** A juror typically
forwards the material to a model provider — often a third party. Treat anything
you submit as disclosed to those providers. **Do not submit secrets, credentials,
or data you are not allowed to share with them.**

**Material paths are validated.** The path is canonicalized and checked against
an allowed root (symlink and `..` traversal are rejected), and it is
re-validated immediately before dispatch to close the time-of-check/time-of-use
window. It must resolve to a real regular file.

**Blindness protects rating integrity, not confidentiality.** The `jury` binary
is the sole holder of the per-run slot→model mapping and never reveals it before
you score. This stops a foreman or scorer from being biased by which model
produced which review. It is not a confidentiality control over your material,
which the juror backends still see.

## Scope

In scope: shell/argument injection in dispatch, path-traversal or symlink
escapes in material validation, leakage of the slot→model mapping before
scoring, run-file or score-log corruption, and similar integrity/confidentiality
breaks in this binary.

Out of scope: vulnerabilities in third-party juror CLIs or model providers,
and misuse resulting from adding an untrusted juror to your own roster.
