// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts

import (
	_ "embed"

	claudelib "github.com/bborbe/agent/claude"
)

//go:embed review_workflow.md
var reviewWorkflow string

//go:embed review_output-format.md
var reviewOutputFormat string

// BuildReviewInstructions assembles the ai_review-phase prompt from embedded modules.
func BuildReviewInstructions() claudelib.Instructions {
	return claudelib.Instructions{
		{Name: "workflow", Content: reviewWorkflow},
		{Name: "output-format", Content: reviewOutputFormat},
	}
}
