// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"sort"
	"testing"
)

func sampleJurors() []Juror {
	return []Juror{
		{Name: "claude", Backend: BackendSubagent, Model: "opus", Enabled: true},
		{Name: "codex", Backend: BackendCLI, Tool: "codex", Model: "gpt-5.5", Enabled: true},
		{Name: "glm", Backend: BackendCLI, Tool: "pi", Model: "zai-org/GLM-5.2", Provider: "featherless", Enabled: true},
		{Name: "minimax", Backend: BackendCLI, Tool: "pi", Model: "MiniMaxAI/MiniMax-M3", Provider: "featherless", Enabled: true},
		{Name: "kimi", Backend: BackendCLI, Tool: "pi", Model: "moonshotai/Kimi-K2.7-Code", Provider: "featherless", Enabled: true},
	}
}

func names(js []Juror) []string {
	out := make([]string, len(js))
	for i, j := range js {
		out[i] = j.Name
	}
	sort.Strings(out)
	return out
}

func TestShuffleIsPermutation(t *testing.T) {
	in := sampleJurors()
	want := names(in)

	for trial := 0; trial < 50; trial++ {
		got, err := Shuffle(in)
		if err != nil {
			t.Fatalf("shuffle: %v", err)
		}
		if len(got) != len(in) {
			t.Fatalf("length changed: %d != %d", len(got), len(in))
		}
		gotNames := names(got)
		for i := range want {
			if gotNames[i] != want[i] {
				t.Fatalf("not a permutation: got %v want %v", gotNames, want)
			}
		}
	}

	// Input must be unmutated.
	if names(in)[0] != want[0] || len(in) != 5 {
		t.Errorf("shuffle mutated input slice")
	}
}

func TestShuffleActuallyShuffles(t *testing.T) {
	in := sampleJurors()
	identical := 0
	const trials = 40
	for i := 0; i < trials; i++ {
		got, err := Shuffle(in)
		if err != nil {
			t.Fatal(err)
		}
		same := true
		for k := range got {
			if got[k].Name != in[k].Name {
				same = false
				break
			}
		}
		if same {
			identical++
		}
	}
	// With 5 elements, P(identity) = 1/120; 40 trials all-identity is virtually
	// impossible unless the shuffle is broken.
	if identical == trials {
		t.Errorf("shuffle never reordered across %d trials", trials)
	}
}
