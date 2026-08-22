// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"errors"

	claudelib "github.com/bborbe/agent/claude"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExtractBudgetPartial", func() {
	DescribeTable("capture extraction",
		func(result *claudelib.ClaudeResult, runErr error, expect string) {
			Expect(pkg.ExtractBudgetPartial(result, runErr)).To(Equal(expect))
		},
		// A run killed at its deadline returns a non-nil result carrying the
		// bounded streamed partial alongside the error (claude runner v0.82.0).
		Entry(
			"killed run returns the captured partial",
			&claudelib.ClaudeResult{Partial: "streamed partial review text"},
			errors.New("claude CLI failed"),
			"streamed partial review text",
		),
		Entry(
			"completed run keeps a captured partial",
			&claudelib.ClaudeResult{Result: `{"verdict":"pass"}`, Partial: "drafted verdict"},
			nil,
			"drafted verdict",
		),
		// No capture present: the salvage signal is an empty partial.
		Entry(
			"nil result with unrelated error yields empty",
			nil,
			errors.New("boom"),
			"",
		),
		Entry(
			"result with empty capture yields empty",
			&claudelib.ClaudeResult{Partial: ""},
			errors.New("claude CLI failed"),
			"",
		),
		Entry(
			"completed run without capture yields empty",
			&claudelib.ClaudeResult{Result: `{"verdict":"pass"}`},
			nil,
			"",
		),
	)
})
