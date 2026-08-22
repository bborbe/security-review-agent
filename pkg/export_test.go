// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"time"

	agentlib "github.com/bborbe/agent"
)

// ShouldVerifyPostForTest exposes reviewStep.shouldVerifyPost for unit testing
// without exposing it to production callers.
func ShouldVerifyPostForTest(ctx context.Context, md *agentlib.Markdown) (bool, error) {
	s := &reviewStep{}
	return s.shouldVerifyPost(ctx, md)
}

// VerdictPayloadForTest re-exports the unexported verdictPayload so
// review_test.go (in the pkg_test package) can assert on the parsed
// values without exposing the type to production callers.
type VerdictPayloadForTest = verdictPayload

// ExtractVerdictForTest re-exports the unexported extractVerdict so
// review_test.go (in the pkg_test package) can table-test parsing
// across the various LLM response shapes Claude produces.
func ExtractVerdictForTest(raw string) (VerdictPayloadForTest, error) {
	return extractVerdict(context.Background(), raw)
}

// NewGHTokenCheckStepWithURLForTest constructs a ghTokenCheckStep
// pointed at a custom URL (httptest.Server in tests). Production code
// should use NewGHTokenCheckStep which hardcodes the GitHub URL.
func NewGHTokenCheckStepWithURLForTest(token, url string) *ghTokenCheckStep {
	return newGHTokenCheckStep(token, url)
}

// PostAndRouteForTest calls postAndRoute on a minimal checkoutExecutionStep,
// bypassing the Claude runner entirely. The md should already have ## Review
// populated by the test. This allows unit-testing the posting path without
// a live Claude process.
func PostAndRouteForTest(
	ctx context.Context,
	prPoster PrPoster,
	md *agentlib.Markdown,
	prURLStr string,
	worktreePath string,
	jobRunTime time.Time,
	funnelRan bool,
) (*agentlib.Result, error) {
	s := &checkoutExecutionStep{prPoster: prPoster}
	return s.postAndRoute(ctx, md, prURLStr, worktreePath, jobRunTime, funnelRan)
}

// ParsePlanningConcernsForTest exposes parsePlanningConcerns for unit testing.
func ParsePlanningConcernsForTest(body string) ([]struct{}, error) {
	return parsePlanningConcerns(context.Background(), body)
}

// AppendVerifyDiagnosticForTest exposes appendVerifyDiagnostic for unit testing.
func AppendVerifyDiagnosticForTest(
	ctx context.Context,
	md *agentlib.Markdown,
	result VerifyResult,
) {
	appendVerifyDiagnostic(ctx, md, result)
}

// NormalizeURLForTest exposes normalizeURL for unit testing.
func NormalizeURLForTest(url string) string {
	return normalizeURL(url)
}

// IsFailClosedReasonForTest exposes isFailClosedReason for unit testing.
func IsFailClosedReasonForTest(reason string) bool {
	return isFailClosedReason(reason)
}

// LastCharsForTest exposes lastChars for unit testing.
func LastCharsForTest(s string, n int) string {
	return lastChars(s, n)
}

// AppendDismissDiagnosticForTest exposes appendDismissDiagnostic to the
// _test package.
func AppendDismissDiagnosticForTest(md *agentlib.Markdown, result PostResult) {
	appendDismissDiagnostic(md, result)
}

// AdvanceIfAlreadyReviewedForTest exposes advanceIfAlreadyReviewed for unit
// testing (the ## Review-present idempotency guard).
func AdvanceIfAlreadyReviewedForTest(md *agentlib.Markdown) *agentlib.Result {
	s := &checkoutExecutionStep{}
	return s.advanceIfAlreadyReviewed(md)
}
