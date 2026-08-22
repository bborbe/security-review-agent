// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"strings"

	agentlib "github.com/bborbe/agent"
)

// SalvageHeading is the task-file section heading under which a budget-terminated
// run's streamed partial review text is persisted. It is deliberately distinct
// from "## Review" so the ## Review-present idempotency guard in the execution
// step can never treat a salvaged partial as a completed review — a partial can
// never advance into ai_review on a later trigger.
const SalvageHeading = "## Salvage"

// salvageMarker introduces a ## Salvage section as clearly incomplete, never a
// verdict.
const salvageMarker = "_Incomplete: this run was terminated at the soft time budget before producing a final result. Salvaged partial output below._\n\n"

// writeSalvage persists partial under ## Salvage when it is non-empty. No-op for
// an empty capture so a budget-terminated run with nothing streamed still routes
// cleanly. Persisted verbatim as markdown text — never executed or interpreted.
func writeSalvage(md *agentlib.Markdown, partial string) {
	partial = strings.TrimSpace(partial)
	if partial == "" {
		return
	}
	md.ReplaceSection(agentlib.Section{Heading: SalvageHeading, Body: salvageMarker + partial})
}
