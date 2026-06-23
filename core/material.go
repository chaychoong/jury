// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateMaterial canonicalizes a caller-supplied material path and rejects it
// unless it is a real, regular file with no symlink component, located under an
// allowed root.
//
// Rationale (§14): the material path is written by an LLM-driven capture agent,
// so a path like "../../etc/passwd" is an arbitrary-file-read confused-deputy
// risk. We do not embed the file's contents — the binary only ever passes the
// path string to the read-only juror CLI — but we still constrain which paths
// are acceptable so a juror cannot be aimed at sensitive files outside the
// project. Allowed roots are the current working directory (the project being
// reviewed) and the jury runs dir (the binary-owned location the capture agent
// is expected to write to). EvalSymlinks resolves every component, so a symlink
// escaping an allowed root is caught.
func ValidateMaterial(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("material path is empty")
	}

	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve material path: %w", err)
	}

	// Resolve symlinks across the whole path. This both canonicalizes and
	// defeats symlink-based escapes from an allowed root.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("material path does not resolve to a real file: %w", err)
	}

	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat material path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("material path %q is not a regular file", resolved)
	}

	roots, err := allowedRoots()
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if withinRoot(resolved, root) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("material path %q is outside the allowed roots %v", resolved, roots)
}

// allowedRoots returns the canonicalized roots a material file may live under.
func allowedRoots() ([]string, error) {
	var roots []string
	if cwd, err := os.Getwd(); err == nil {
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			roots = append(roots, r)
		} else {
			roots = append(roots, cwd)
		}
	}
	runs, err := RunsDir()
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(runs); err == nil {
		roots = append(roots, r)
	} else {
		roots = append(roots, runs)
	}
	// Also allow an explicit override root (tests / relocation).
	if extra := os.Getenv("JURY_MATERIAL_ROOT"); extra != "" {
		if r, err := filepath.EvalSymlinks(extra); err == nil {
			roots = append(roots, r)
		} else {
			roots = append(roots, extra)
		}
	}
	return roots, nil
}

// withinRoot reports whether path is root itself or nested under it, using a
// path-segment comparison (so /a/bc is not considered under /a/b).
func withinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
