// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isFailClosedReason", func() {
	DescribeTable("classifies request-changes reasons",
		func(reason string, expected bool) {
			Expect(pkg.IsFailClosedReasonForTest(reason)).To(Equal(expected))
		},
		// Fail-closed reasons ParseVerdict emits — these drive the diagnostic log.
		Entry("empty review text", "empty review text", true),
		Entry("no verdict block", "no verdict block", true),
		Entry("malformed JSON", "malformed JSON: unexpected end of JSON input", true),
		Entry("unknown verdict", "unknown verdict: block", true),
		// Model-authored reasons on a genuine request-changes — must NOT log.
		Entry("real reason text", "Two must-fix issues in the auth handler", false),
		Entry("empty string", "", false),
		Entry("approve-style reason", "looks good", false),
	)
})

var _ = Describe("lastChars", func() {
	DescribeTable("returns the tail without splitting runes",
		func(s string, n int, expected string) {
			Expect(pkg.LastCharsForTest(s, n)).To(Equal(expected))
		},
		Entry("shorter than n returns whole string", "abc", 10, "abc"),
		Entry("exactly n returns whole string", "abcde", 5, "abcde"),
		Entry("longer than n returns tail", "abcdef", 3, "def"),
		Entry("zero n returns empty", "abcdef", 0, ""),
		Entry("negative n returns empty", "abcdef", -1, ""),
		Entry("empty input", "", 5, ""),
		Entry("multibyte runes not split", "aé😀xy", 2, "xy"),
	)
})
