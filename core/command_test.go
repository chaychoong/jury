// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"io"
	"strings"
	"testing"
)

func TestBuildJurorCommandCodex(t *testing.T) {
	j := Juror{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "gpt-5.5"}
	name, args, err := BuildJurorCommand(j, "/abs/material.md")
	if err != nil {
		t.Fatal(err)
	}
	if name != "codex" {
		t.Errorf("name = %q, want codex", name)
	}
	want := []string{"exec", "-s", "read-only", "--skip-git-repo-check", "-m", "gpt-5.5"}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], w)
		}
	}
	// Last arg is the prompt, as a single argv element, containing the material.
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "/abs/material.md") {
		t.Errorf("prompt missing material path: %q", prompt)
	}
}

func TestBuildJurorCommandPi(t *testing.T) {
	j := Juror{Name: "glm", Backend: BackendCLI, Tool: "pi", Model: "zai-org/GLM-5.2", Provider: "featherless"}
	name, args, err := BuildJurorCommand(j, "/abs/material.md")
	if err != nil {
		t.Fatal(err)
	}
	if name != "pi" {
		t.Errorf("name = %q, want pi", name)
	}
	joined := strings.Join(args, " ")
	for _, frag := range []string{"--provider featherless", "--model zai-org/GLM-5.2", "--tools read,grep,find,ls", "--no-session", "-p"} {
		if !strings.Contains(joined, frag) {
			t.Errorf("pi args missing %q; got %q", frag, joined)
		}
	}
	if args[len(args)-1] != ReviewPrompt("/abs/material.md") {
		t.Errorf("last arg should be the full prompt as one element")
	}
}

// TestNoShellInvocation asserts the §14 MUST: structured argv, never a shell.
func TestNoShellInvocation(t *testing.T) {
	jurors := []Juror{
		{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "gpt-5.5"},
		{Name: "glm", Backend: BackendCLI, Tool: "pi", Model: "zai-org/GLM-5.2", Provider: "featherless"},
	}
	for _, j := range jurors {
		name, args, err := BuildJurorCommand(j, "/abs/material.md")
		if err != nil {
			t.Fatal(err)
		}
		// Executable must be the real CLI, never a shell.
		switch name {
		case "sh", "bash", "zsh", "/bin/sh", "/bin/bash":
			t.Fatalf("juror %q is invoked via a shell (%q)", j.Name, name)
		}
		// No argv element may smuggle a shell redirect / pipe / -c, which would
		// only matter if something passed argv to a shell. We assert none of the
		// fixed flag elements is "-c" and that the redirect token is absent.
		for _, a := range args {
			if a == "-c" {
				t.Errorf("juror %q argv contains a bare -c (shell-style)", j.Name)
			}
			if a == "</dev/null" {
				t.Errorf("juror %q argv contains a shell redirect token; stdin must be wired via NullStdin", j.Name)
			}
		}
	}
}

// TestNewJurorCmdNullStdin asserts the child's Stdin is a null reader (EOF
// immediately), preserving the documented no-stdin behaviour without a shell
// redirect.
func TestNewJurorCmdNullStdin(t *testing.T) {
	j := Juror{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "gpt-5.5"}
	cmd, err := NewJurorCmd(j, "/abs/material.md")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Stdin == nil {
		t.Fatal("juror cmd has nil Stdin; must be a null reader")
	}
	b, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatalf("reading stdin: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("null stdin yielded %d bytes, want 0 (immediate EOF)", len(b))
	}
	// The exec path uses the real CLI as Path, never a shell.
	if strings.Contains(cmd.Path, "sh") && strings.HasSuffix(cmd.Path, "/sh") {
		t.Errorf("cmd.Path looks like a shell: %q", cmd.Path)
	}
}

func TestBuildJurorCommandRejectsSubagent(t *testing.T) {
	j := Juror{Name: "claude", Backend: BackendSubagent, Model: "opus"}
	if _, _, err := BuildJurorCommand(j, "/abs/material.md"); err == nil {
		t.Error("expected error building a cli command for a subagent juror")
	}
}

// TestBuildJurorCommandRejectsFlagLikeSlug asserts the S1 guard: a model or
// provider slug that could be parsed as a flag by the child CLI is rejected.
func TestBuildJurorCommandRejectsFlagLikeSlug(t *testing.T) {
	cases := []Juror{
		{Name: "x", Backend: BackendCLI, Tool: "codex", Model: "--dangerous"},
		{Name: "x", Backend: BackendCLI, Tool: "codex", Model: "-m"},
		{Name: "x", Backend: BackendCLI, Tool: "pi", Model: "ok/model", Provider: "--evil"},
		{Name: "x", Backend: BackendCLI, Tool: "pi", Model: "-bad", Provider: "featherless"},
	}
	for _, j := range cases {
		if _, _, err := BuildJurorCommand(j, "/abs/material.md"); err == nil {
			t.Errorf("flag-like slug accepted: model=%q provider=%q", j.Model, j.Provider)
		}
	}
}

// TestBuildJurorCommandCodexHasSeparator asserts codex gets a `--` end-of-options
// separator immediately before the prompt (S1), so the prompt can never be
// parsed as a flag or subcommand.
func TestBuildJurorCommandCodexHasSeparator(t *testing.T) {
	j := Juror{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "gpt-5.5"}
	_, args, err := BuildJurorCommand(j, "/abs/material.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) < 2 || args[len(args)-2] != "--" {
		t.Errorf("expected `--` right before the prompt, got args %v", args)
	}
}
