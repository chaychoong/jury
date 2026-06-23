// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateMaterial(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JURY_MATERIAL_ROOT", root)

	// A regular file under the allowed root passes.
	good := filepath.Join(root, "material.md")
	if err := os.WriteFile(good, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := ValidateMaterial(good)
	if err != nil {
		t.Fatalf("valid material rejected: %v", err)
	}
	if want, _ := filepath.EvalSymlinks(good); resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}

	// Empty path rejected.
	if _, err := ValidateMaterial(""); err == nil {
		t.Error("empty path should be rejected")
	}

	// Nonexistent path rejected.
	if _, err := ValidateMaterial(filepath.Join(root, "nope.md")); err == nil {
		t.Error("nonexistent path should be rejected")
	}

	// A directory rejected (not a regular file).
	if _, err := ValidateMaterial(root); err == nil {
		t.Error("directory should be rejected")
	}

	// A path outside the allowed roots is rejected. /etc/hosts exists on macOS
	// and is well outside cwd / runs dir / the override root.
	if _, err := ValidateMaterial("/etc/hosts"); err == nil {
		t.Error("path outside allowed roots should be rejected")
	}

	// A symlink under the root that escapes to an outside target is rejected,
	// because EvalSymlinks resolves it to the outside target.
	link := filepath.Join(root, "escape.md")
	if err := os.Symlink("/etc/hosts", link); err == nil {
		if _, err := ValidateMaterial(link); err == nil {
			t.Error("symlink escaping the root should be rejected")
		}
	}
}
