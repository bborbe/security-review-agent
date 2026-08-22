// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts

import (
	_ "embed"

	claudelib "github.com/bborbe/agent/claude"
)

//go:embed planning_workflow.md
var planningWorkflow string

//go:embed planning_output-format.md
var planningOutputFormat string

// BuildPlanningInstructions assembles the planning-phase prompt from embedded modules.
func BuildPlanningInstructions() claudelib.Instructions {
	return claudelib.Instructions{
		{Name: "workflow", Content: planningWorkflow},
		{Name: "output-format", Content: planningOutputFormat},
	}
}
