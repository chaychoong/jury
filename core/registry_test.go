// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"path/filepath"
	"testing"
)

func TestSeedAndLoadRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jury", "jurors.toml")

	if err := SeedRegistry(path); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Idempotent.
	if err := SeedRegistry(path); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reg.Jurors) != 5 {
		t.Fatalf("want 5 jurors, got %d", len(reg.Jurors))
	}

	want := map[string]struct {
		backend  string
		model    string
		provider string
	}{
		"claude":  {BackendSubagent, "opus", ""},
		"codex":   {BackendCLI, "gpt-5.5", ""},
		"glm":     {BackendCLI, "zai-org/GLM-5.2", "featherless"},
		"minimax": {BackendCLI, "MiniMaxAI/MiniMax-M3", "featherless"},
		"kimi":    {BackendCLI, "moonshotai/Kimi-K2.7-Code", "featherless"},
	}
	for name, exp := range want {
		j, ok := reg.ByName(name)
		if !ok {
			t.Errorf("missing juror %q", name)
			continue
		}
		if j.Backend != exp.backend || j.Model != exp.model || j.Provider != exp.provider {
			t.Errorf("juror %q = %+v, want backend=%s model=%s provider=%s",
				name, j, exp.backend, exp.model, exp.provider)
		}
		if !j.Enabled {
			t.Errorf("seed juror %q should be enabled", name)
		}
		if j.Family == "" {
			t.Errorf("seed juror %q missing family", name)
		}
	}

	if got := reg.Enabled(); len(got) != 5 {
		t.Errorf("want 5 enabled, got %d", len(got))
	}
}

func TestLoadRegistryRejectsBad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")

	cases := map[string]string{
		"dup name":       "[[juror]]\nname='a'\nbackend='cli'\ntool='codex'\n[[juror]]\nname='a'\nbackend='cli'\ntool='codex'\n",
		"empty name":     "[[juror]]\nname=''\nbackend='cli'\ntool='codex'\n",
		"bad backend":    "[[juror]]\nname='a'\nbackend='wat'\n",
		"no jurors":      "# nothing\n",
		"cli no tool":    "[[juror]]\nname='a'\nbackend='cli'\n",
		"cli bad tool":   "[[juror]]\nname='a'\nbackend='cli'\ntool='claude'\n",
		"pi no provider": "[[juror]]\nname='a'\nbackend='cli'\ntool='pi'\nmodel='m'\n",
	}
	for label, content := range cases {
		if err := writeFile(path, content); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRegistry(path); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}
