// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"os"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression for the verdict/state mismatch observed on
// bborbe/github-update-go-agent PRs #3 and #5 (2026-07-21): the reviewer emitted
// an "approve" verdict inside a ```json fence, but the byte-level brace walker in
// findLastJSONVerdictBlock mis-extracted the block whenever a JSON string VALUE
// contained a stray brace or unescaped quote (common when the review prose
// describes parser code), so json.Unmarshal failed and the verdict fail-closed
// to request-changes → GitHub state CHANGES_REQUESTED → blocked auto-merge.
//
// The fix extracts the verdict block by ```json fence boundaries (immune to
// braces/quotes in string values) and, when a fenced block is still invalid JSON,
// recovers the literal "verdict" field verbatim. The fixtures below are the REAL
// review bodies captured from the GitHub API.
var _ = Describe("ParseVerdict extraction regression (github-update-go-agent #3/#5/#6)", func() {
	DescribeTable("real reviewer bodies map to the verdict the model actually stated",
		func(fixture string, expected pkg.Verdict) {
			body, err := os.ReadFile("testdata/" + fixture)
			Expect(err).NotTo(HaveOccurred())
			result := pkg.ParseVerdict(string(body))
			Expect(result.Verdict).To(Equal(expected))
		},
		// #3 — fenced approve, but a concerns_addressed string contains unescaped
		// inner double-quotes (`prior "no file changes" error`) → the fenced block
		// is not valid JSON. Fence extraction + verdict-field recovery yields approve.
		Entry("#3 malformed inner quotes → Approve (recovered)",
			"review_pr3_malformed_quotes.md", pkg.VerdictApprove),
		// #5 — fenced approve, valid JSON, but a concerns_addressed string contains
		// a lone '}' (`finds the last '}'`) that fooled the old brace walker into
		// grabbing prose. Fence extraction returns the valid block → approve.
		Entry("#5 unbalanced brace in string → Approve (fenced extraction)",
			"review_pr5_unbalanced_brace.md", pkg.VerdictApprove),
		// #6 — the same-day counter-example that already worked (Dockerfile review,
		// no braces/quotes in string values). Must stay Approve.
		Entry("#6 clean approve → Approve (unchanged)",
			"review_pr6_clean_approve.md", pkg.VerdictApprove),
	)
})

var _ = Describe("ParseVerdict fenced-extraction unit cases", func() {
	fence := func(body string) string {
		return "# Code Review\n\nProse describing parser internals.\n\n```json\n" + body + "\n```\n\nTrailing prose."
	}

	It("extracts approve from a fenced block despite an unbalanced '}' in a string value", func() {
		body := "{\n" +
			`  "verdict": "approve",` + "\n" +
			`  "summary": "ok",` + "\n" +
			`  "concerns_addressed": [` + "\n" +
			`    "correctness: walks backward to find the last '}' then matches"` + "\n" +
			"  ]\n}"
		result := pkg.ParseVerdict(fence(body))
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
	})

	It("recovers approve from a fenced block with unescaped inner double-quotes", func() {
		body := "{\n" +
			`  "verdict": "approve",` + "\n" +
			`  "summary": "the prior "no file changes" error is gone"` + "\n" +
			"}"
		result := pkg.ParseVerdict(fence(body))
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
	})

	It("recovers request-changes (never flips to approve) from a malformed fenced block", func() {
		// The recovery path reads the literal verdict field — it must surface the
		// stated request-changes, never fabricate an approve.
		body := "{\n" +
			`  "verdict": "request-changes",` + "\n" +
			`  "summary": "the "auth" path is broken"` + "\n" +
			"}"
		result := pkg.ParseVerdict(fence(body))
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
	})

	It("prefers the LAST fenced verdict block when an earlier example fence exists", func() {
		text := "# Review\n\nExample of the schema:\n\n```json\n" +
			`{"verdict": "request-changes", "reason": "example only"}` + "\n```\n\n" +
			"Actual verdict:\n\n```json\n" +
			`{"verdict": "approve", "reason": "real verdict"}` + "\n```"
		result := pkg.ParseVerdict(text)
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		Expect(result.Reason).To(Equal("real verdict"))
	})

	It("still fail-closes on BARE (unfenced) malformed JSON — no recovery", func() {
		// Unfenced blocks are too weak a signal to recover past json.Unmarshal;
		// behavior is unchanged from before the fix.
		text := "### Must Fix\n\n- issue\n\n" + `{"verdict": "approve", "reason": invalid json}`
		result := pkg.ParseVerdict(text)
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(HavePrefix("malformed JSON:"))
	})
})
