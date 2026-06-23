// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package core

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// Shuffle returns a new slice containing the jurors in a cryptographically
// random order (Fisher–Yates over crypto/rand). It does not mutate the input.
//
// Per §14 Q3 the shuffle uses real randomness and is intentionally NOT seeded
// on the run id; auditability comes from persisting the resulting slot→juror
// map in the run file, not from reproducibility.
func Shuffle(jurors []Juror) ([]Juror, error) {
	out := make([]Juror, len(jurors))
	copy(out, jurors)
	for i := len(out) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, fmt.Errorf("shuffle randomness: %w", err)
		}
		j := int(jBig.Int64())
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
