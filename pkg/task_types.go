// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import agentlib "github.com/bborbe/agent"

// TaskTypeSecurityReview is the task type for security review tasks. Routes to
// the security agent's 3-phase domain flow (planning → execution → ai_review)
// with no GitHub posting (nil poster + nil verifier); the run ends at the
// ## Review write-back.
const TaskTypeSecurityReview agentlib.TaskType = "security-review"

// TaskTypeBuildFix is the task type for failed Cloud Build analysis tasks.
// The google-cloud-build-watcher emits it (as a plain string) for every
// failed master build; the agent routes it to the code-only build-fix step,
// which reads the build log, extracts the failure cause, and enriches the
// task in place. No Claude, no clone, no auto-fix.
const TaskTypeBuildFix agentlib.TaskType = "build-fix"
