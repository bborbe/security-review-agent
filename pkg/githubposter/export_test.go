// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
)

func init() {
	retryBaseDelay = 0
	retryJitterMs = 0
}

// ClassifyErrorForTest exposes classifyError for unit testing.
func ClassifyErrorForTest(httpStatus int, err error) prpkg.ErrorClass {
	return classifyError(httpStatus, err)
}

// EventToStateForTest exposes eventToState for unit testing.
func EventToStateForTest(event string) string {
	return eventToState(event)
}

// TruncateBodyForTest exposes truncateBody for unit testing.
func TruncateBodyForTest(b []byte) string {
	return truncateBody(b)
}
