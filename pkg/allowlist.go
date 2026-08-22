// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"strings"
)

// ParseRepoAllowlist parses a comma-separated allowlist string into a slice
// of host-qualified repo keys. Whitespace is trimmed; empty entries are skipped.
// Returns (nil, nil) for empty input (allow-all).
// Entry well-formedness is NOT validated here — call repoallowlist.Validate at
// startup for fail-fast validation, or rely on repoallowlist.IsAllowed which
// logs and skips malformed entries at match time.
func ParseRepoAllowlist(_ context.Context, raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var result []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			result = append(result, entry)
		}
	}
	return result, nil
}
