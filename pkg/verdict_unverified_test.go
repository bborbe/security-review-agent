// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HasUnverifiedConcerns", func() {
	fence := func(body string) string {
		return "# Code Review\n\n```json\n" + body + "\n```\n"
	}

	DescribeTable("detects ## Plan concerns flagged as not verified",
		func(reviewBody string, expected bool) {
			Expect(pkg.HasUnverifiedConcerns(reviewBody)).To(Equal(expected))
		},
		// flagged lowercase `not verified` → the model stopped at the time budget.
		Entry(
			"flagged lowercase not verified",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit not verified"]}`,
			),
			true,
		),
		// case-insensitive match on the canonical wording.
		Entry(
			"flagged uppercase NOT VERIFIED",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit NOT VERIFIED"]}`,
			),
			true,
		),
		// `unverified` variant is matched too (robust to model phrasing).
		Entry(
			"unverified variant",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit unverified"]}`,
			),
			true,
		),
		// No flagged concern → the gate must not over-trigger.
		Entry(
			"no flags",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit addressed in handler.go:45"]}`,
			),
			false,
		),
		// Empty concerns list → no over-trigger.
		Entry("empty concerns_addressed",
			fence(`{"verdict":"approve","concerns_addressed":[]}`), false),
		// Missing concerns_addressed key → no over-trigger.
		Entry("no concerns_addressed key",
			fence(`{"verdict":"approve","reason":"clean"}`), false),
		// Prose without a verdict block → nothing to examine.
		Entry("prose without a verdict block",
			"LGTM. All checks pass.", false),
		// Malformed verdict JSON → unmarshal fails → no over-trigger.
		Entry("malformed JSON",
			fence(`{"verdict":"approve","concerns_addressed":["unterminated`), false),
	)
})
