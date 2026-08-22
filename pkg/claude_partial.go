// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	claudelib "github.com/bborbe/agent/claude"
)

// ExtractBudgetPartial returns the bounded streamed partial review text the
// claude runner captured when a run was terminated before its final result
// event. Returns "" when no capture is present (the run completed normally,
// or the failure was unrelated to termination). Consumed by the soft-budget
// routing so a budget-terminated run can salvage what the model wrote.
func ExtractBudgetPartial(result *claudelib.ClaudeResult, runErr error) string {
	if result != nil {
		return result.Partial
	}
	return ""
}
