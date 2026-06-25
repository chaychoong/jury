// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build unix

// Advisory file locking via flock(2). Unix-only by design — `jury` targets
// Linux and macOS; there is no Windows build.

package core

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// withFileLock acquires an exclusive advisory lock (flock) on a dedicated lock
// file at lockPath, runs fn while holding it, and releases it afterwards. The
// lock file is created if absent and is never removed (removing it would race
// with another holder that has it open).
//
// This serializes the read→mutate→write sequences in SetSlotStatus and the
// append in AppendScoreRecord so concurrent jury processes (the workflow runs
// jurors in parallel, each invoking `jury run-juror` / `jury score`) cannot
// clobber or tear each other's writes. flock is advisory and per-open-file, and
// works across separate processes, which is exactly the concurrency model here.
func withFileLock(lockPath string, fn func() error) (err error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock %s: %w", lockPath, err)
	}
	defer func() {
		if uerr := unix.Flock(int(f.Fd()), unix.LOCK_UN); uerr != nil && err == nil {
			err = fmt.Errorf("release lock %s: %w", lockPath, uerr)
		}
	}()

	return fn()
}
