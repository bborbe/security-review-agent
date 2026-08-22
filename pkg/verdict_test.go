// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"strings"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Parse", func() {
	var (
		reviewText string
		result     pkg.Result
	)

	JustBeforeEach(func() {
		result = pkg.ParseVerdict(reviewText)
	})

	Context("empty review text", func() {
		BeforeEach(func() {
			reviewText = ""
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'empty review text'", func() {
			Expect(result.Reason).To(Equal("empty review text"))
		})
	})

	Context("review with no recognizable sections", func() {
		BeforeEach(func() {
			reviewText = "This is just some random text without any sections."
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section containing items", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

- Security issue in authentication
- SQL injection vulnerability`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section saying *None*", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

*None*

### Should Fix (Important)

- Add error handling`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section saying None identified", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

None identified.

### Should Fix (Important)

- Improve error messages`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section saying none (lowercase)", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

none

### Nice to Have (Optional)

- Add docstrings`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section that is empty", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)


### Should Fix (Important)

- Add tests`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with only Should Fix and Nice to Have sections", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Add error handling
- Improve logging

### Nice to Have (Optional)

- Add docstrings
- Refactor for clarity`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with only Should Fix section", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Add error handling`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with only Nice to Have section", func() {
		BeforeEach(func() {
			reviewText = `### Nice to Have (Optional)

- Add comments`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with h2 Must Fix instead of h3", func() {
		BeforeEach(func() {
			reviewText = `## Must Fix (Critical)

- Critical security flaw`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with h2 Must Fix saying None", func() {
		BeforeEach(func() {
			reviewText = `## Must Fix (Critical)

None

## Should Fix (Important)

- Add tests`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with case variations in Must Fix header", func() {
		BeforeEach(func() {
			reviewText = `### MUST FIX (Critical)

- Issue found`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})
	})

	Context("review with Must Fix containing 'No issues found' text", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

No issues found.

### Should Fix (Important)

- Improve code structure`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix at end of document", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Add tests

### Must Fix (Critical)

- Security vulnerability`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix None separated by horizontal rules", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix

None.

---

### Should Fix

None.

---

### Nice to Have

- Minor style improvement`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("complex review with multiple sections", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

## Summary

This PR adds new features.

### Must Fix (Critical)

*None*

### Should Fix (Important)

- Add error handling in main.go:45
- Missing input validation

### Nice to Have (Optional)

- Add docstrings
- Refactor for clarity

## Conclusion

Overall good work!`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("JSON verdict on bare line", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content here.

{"verdict": "approve", "reason": "all checks passed"}`
		})

		It("returns VerdictApprove from JSON", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		})

		It("returns reason from JSON", func() {
			Expect(result.Reason).To(Equal("all checks passed"))
		})
	})

	Context("JSON verdict inside code fence", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content here.

` + "```json" + `
{"verdict": "request-changes", "reason": "critical security issues"}
` + "```" + ``
		})

		It("returns VerdictRequestChanges from JSON", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason from JSON", func() {
			Expect(result.Reason).To(Equal("critical security issues"))
		})
	})

	Context("JSON verdict with extra whitespace", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some content.

   {"verdict": "comment", "reason": "informational only"}   `
		})

		It("returns VerdictRequestChanges (JSON comment rejected)", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns unknown verdict reason from JSON", func() {
			Expect(result.Reason).To(Equal("unknown verdict: comment"))
		})
	})

	Context("invalid JSON falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix

- Security issue

{"verdict": "approve", "reason": invalid json}`
		})

		It("returns VerdictRequestChanges from heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns malformed JSON reason", func() {
			Expect(result.Reason).To(HavePrefix("malformed JSON:"))
		})
	})

	Context("missing verdict field falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix

- Add tests

{"reason": "just a reason"}`
		})

		It("returns VerdictRequestChanges from heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("unknown verdict value falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Nice to Have

- Refactor code

{"verdict": "unknown-verdict", "reason": "some reason"}`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns unknown verdict reason", func() {
			Expect(result.Reason).To(Equal("unknown verdict: unknown-verdict"))
		})
	})

	Context("no JSON at all uses heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix

None

### Should Fix

- Add error handling`
		})

		It("returns VerdictRequestChanges from heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("JSON verdict in middle of review is ignored", func() {
		BeforeEach(func() {
			// Build a review with >50 lines so JSON in middle is outside the search window
			var lines []string
			lines = append(lines, "# Code Review")
			lines = append(lines, "")
			lines = append(lines, `{"verdict": "approve", "reason": "this should be ignored"}`)
			lines = append(lines, "")

			// Add 60 more lines to push JSON out of the last-50 window
			for i := 0; i < 60; i++ {
				lines = append(lines, "Some review content line.")
			}

			lines = append(lines, "### Must Fix")
			lines = append(lines, "- Critical issue")

			reviewText = strings.Join(lines, "\n")
		})

		It("ignores JSON in middle and uses heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("JSON verdict with 'comment' value is rejected — falls to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix

- Add error handling

{"verdict": "comment", "reason": "informational only"}`
		})

		It("returns VerdictRequestChanges (not comment)", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns unknown verdict reason from JSON", func() {
			Expect(result.Reason).To(Equal("unknown verdict: comment"))
		})
	})

	Context("Should Fix only — non-empty content triggers request-changes", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Improve error handling in pkg/server.go:42`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("Should Fix present but empty — approve", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

None.

### Nice to Have

- Add docstrings`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("empty input returns request-changes (fail-closed)", func() {
		BeforeEach(func() {
			reviewText = ""
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'empty review text'", func() {
			Expect(result.Reason).To(Equal("empty review text"))
		})
	})

	Context("unparseable input (no sections) returns request-changes (fail-closed)", func() {
		BeforeEach(func() {
			reviewText = "Just some random prose without any review sections."
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("multi-line fenced JSON with new spec-025 schema (no reason field) — approve", func() {
		BeforeEach(func() {
			reviewText = "# Code Review\n\nThe PR adds an HTML comment to README.md.\n\n" +
				"```json\n" +
				"{\n" +
				"  \"verdict\": \"approve\",\n" +
				"  \"summary\": \"Trivial doc-only change, no findings.\",\n" +
				"  \"comments\": [],\n" +
				"  \"concerns_addressed\": [\n" +
				"    \"correctness: no logic changes\"\n" +
				"  ]\n" +
				"}\n" +
				"```"
		})

		It("returns VerdictApprove from the multi-line block", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		})
	})

	Context("multi-line fenced JSON with new spec-025 schema — request-changes", func() {
		BeforeEach(func() {
			reviewText = "# Code Review\n\nThe PR has critical issues.\n\n" +
				"```json\n" +
				"{\n" +
				"  \"verdict\": \"request-changes\",\n" +
				"  \"summary\": \"Critical issues found.\",\n" +
				"  \"comments\": []\n" +
				"}\n" +
				"```"
		})

		It("returns VerdictRequestChanges from the multi-line block", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})
	})

	Context("malformed JSON in fenced block falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = "```json\n{verdict: invalid no quotes\n```\n## Must Fix\n- problem"
		})

		It("returns VerdictRequestChanges from heuristic must-fix items", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})
})

var _ = Describe("StripJSONVerdict", func() {
	var (
		reviewText string
		stripped   string
	)

	JustBeforeEach(func() {
		stripped = pkg.StripJSONVerdict(reviewText)
	})

	Context("removes JSON verdict on bare line", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content here.

{"verdict": "approve", "reason": "all checks passed"}`
		})

		It("removes the JSON line", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).To(ContainSubstring("Some review content here."))
		})
	})

	Context("removes JSON verdict inside code fence", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content.

` + "```json" + `
{"verdict": "approve", "reason": "looks good"}
` + "```" + `

End of review.`
		})

		It("removes the JSON and code fence", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).NotTo(ContainSubstring("```json"))
			Expect(stripped).To(ContainSubstring("Some review content."))
			Expect(stripped).To(ContainSubstring("End of review."))
		})
	})

	Context("preserves text when no JSON found", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content without JSON verdict.`
		})

		It("returns unchanged text", func() {
			Expect(stripped).To(Equal(reviewText))
		})
	})

	Context("handles multiple trailing newlines", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some content.

{"verdict": "approve", "reason": "good"}


`
		})

		It("collapses trailing blank lines", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).NotTo(HaveSuffix("\n\n\n"))
			Expect(stripped).To(ContainSubstring("Some content."))
		})
	})

	Context("preserves other JSON in review", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Here's an example:
` + "```json" + `
{"config": "value"}
` + "```" + `

{"verdict": "approve", "reason": "ok"}`
		})

		It("only removes verdict JSON", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).To(ContainSubstring(`"config"`))
		})
	})
})

var _ = Describe("ParseVerdict normalisation regression (spec-030)", func() {
	DescribeTable("verdict spelling and case normalisation",
		func(reviewText string, expectedVerdict pkg.Verdict) {
			result := pkg.ParseVerdict(reviewText)
			Expect(result.Verdict).To(Equal(expectedVerdict))
		},
		// (a) canonical hyphen spelling — the parser must always accept this
		Entry("request-changes hyphen → RequestChanges",
			`{"verdict": "request-changes"}`,
			pkg.VerdictRequestChanges,
		),
		// (b) underscore drift — THE SMOKING-GUN ROW.
		// Pre-fix `ParseVerdict` returned VerdictApprove for this input via the
		// deleted heuristic; this row must remain RequestChanges to prove the
		// normalisation switch is load-bearing. The spec-030 revert-test AC
		// requires this row to fail when `strings.ReplaceAll(_, "_", "-")` is
		// removed from the parser.
		Entry("request_changes underscore → RequestChanges (normalised)",
			`{"verdict": "request_changes"}`,
			pkg.VerdictRequestChanges,
		),
		// (c) ALL-CAPS hyphen
		Entry("REQUEST-CHANGES caps → RequestChanges (normalised)",
			`{"verdict": "REQUEST-CHANGES"}`,
			pkg.VerdictRequestChanges,
		),
		// (d) approve canonical
		Entry("approve → Approve",
			`{"verdict": "approve"}`,
			pkg.VerdictApprove,
		),
		// (e) mixed-case approve
		Entry("Approve mixed-case → Approve (normalised)",
			`{"verdict": "Approve"}`,
			pkg.VerdictApprove,
		),
		// (f) unknown value fails closed
		Entry("comment → RequestChanges (fail-closed, unknown verdict)",
			`{"verdict": "comment"}`,
			pkg.VerdictRequestChanges,
		),
		// (g) empty review text fails closed
		Entry("empty text → RequestChanges (fail-closed)",
			``,
			pkg.VerdictRequestChanges,
		),
		// (h) malformed JSON containing quoted "verdict" key — block found, unmarshal fails
		Entry("malformed JSON → RequestChanges (fail-closed)",
			`{"verdict": invalid}`,
			pkg.VerdictRequestChanges,
		),
		// (i) multi-line fenced block with ≥3 prose lines before and ≥1 after
		Entry(
			"multi-line fenced request-changes amid prose → RequestChanges",
			"Line of prose 1.\nLine of prose 2.\nLine of prose 3.\n\n```json\n{\n  \"verdict\": \"request-changes\",\n  \"summary\": \"Issues found.\",\n  \"comments\": []\n}\n```\nTrailing prose line.",
			pkg.VerdictRequestChanges,
		),
	)

	It("unknown verdict reason names the raw value", func() {
		result := pkg.ParseVerdict(`{"verdict": "block"}`)
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(Equal("unknown verdict: block"))
	})

	It("request_changes reason is preserved from JSON reason field", func() {
		result := pkg.ParseVerdict(`{"verdict": "request_changes", "reason": "security issue"}`)
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(Equal("security issue"))
	})

	It("no verdict block reason is 'no verdict block'", func() {
		result := pkg.ParseVerdict("### Must Fix\n\n- critical item")
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(Equal("no verdict block"))
	})
})

var _ = Describe("ParseVerdict end-anchored window regression (long verdict block)", func() {
	// Reproduces the maintainer PR #29 inversion (2026-05-30): the reviewer
	// emitted a well-formed `"verdict": "approve"` block, but the block's long
	// `comments` array pushed the "verdict" KEY ~60 lines above the closing
	// brace — outside the old key-line 50-line window — so the parser returned
	// "no verdict block", fail-closed to request-changes, and posted a false
	// CHANGES_REQUESTED on a PR the bot actually approved. The fix anchors the
	// window on the block's CLOSING brace (which sits at the end of the review
	// per the execution output-format spec) and walks back unbounded to the
	// matching open brace, so block length no longer drops a present verdict.

	// longVerdictBlock builds a fenced JSON verdict block whose "verdict" key is
	// pushed `commentCount * 5` lines above the closing brace by the comments
	// array, with ≥3 prose lines before the fence.
	longVerdictBlock := func(verdict string, commentCount int) string {
		var b strings.Builder
		b.WriteString(
			"# Code Review\n\nNarrative line 1.\nNarrative line 2.\nNarrative line 3.\n\n",
		)
		b.WriteString("```json\n{\n")
		b.WriteString(`  "verdict": "` + verdict + `",` + "\n")
		b.WriteString(`  "summary": "Well-scoped change; only nits flagged.",` + "\n")
		b.WriteString(`  "comments": [` + "\n")
		for i := 0; i < commentCount; i++ {
			comma := ","
			if i == commentCount-1 {
				comma = ""
			}
			b.WriteString("    {\n")
			b.WriteString(`      "file": "prompts/completed/stale.md",` + "\n")
			b.WriteString(`      "line": 1,` + "\n")
			b.WriteString(`      "severity": "nit",` + "\n")
			b.WriteString(`      "message": "stale prompt file"` + "\n")
			b.WriteString("    }" + comma + "\n")
		}
		b.WriteString("  ]\n}\n```")
		return b.String()
	}

	It("parses approve when the verdict key is >50 lines above a near-end closing brace", func() {
		reviewText := longVerdictBlock("approve", 20)
		// Sanity: the verdict key really is outside the trailing 50-line window.
		lines := strings.Split(reviewText, "\n")
		verdictLine := -1
		for i, l := range lines {
			if strings.Contains(l, `"verdict"`) {
				verdictLine = i
			}
		}
		Expect(verdictLine).NotTo(Equal(-1), "generated block must contain a verdict key")
		Expect(len(lines)-verdictLine).To(BeNumerically(">", 50),
			"test setup must push the verdict key beyond the 50-line window")

		result := pkg.ParseVerdict(reviewText)
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		// Pin the exact regression symptom: the block was FOUND and parsed, not
		// silently dropped and fail-closed (which would read "no verdict block").
		Expect(result.Reason).NotTo(Equal("no verdict block"))
	})

	It("finds a verdict block sitting just past the 50-line boundary", func() {
		// commentCount=11 → ~55 comment lines: closing brace within the window,
		// verdict key just outside the old 50-line cap.
		result := pkg.ParseVerdict(longVerdictBlock("approve", 11))
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		Expect(result.Reason).NotTo(Equal("no verdict block"))
	})

	It("still maps a long request-changes block to RequestChanges via the block", func() {
		result := pkg.ParseVerdict(longVerdictBlock("request-changes", 20))
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		// Must come from the parsed block, not from fail-closed "no verdict block".
		Expect(result.Reason).NotTo(Equal("no verdict block"))
	})

	It("matches the outer brace despite balanced braces inside a string value", func() {
		reviewText := "# Review\n\nProse line.\n\n```json\n{\n" +
			`  "verdict": "approve",` + "\n" +
			`  "reason": "use map[string]struct{}{} and {} sparingly"` + "\n" +
			"}\n```"
		result := pkg.ParseVerdict(reviewText)
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
	})
})
