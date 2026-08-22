// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	prurl "github.com/bborbe/maintainer/prurl"
)

//counterfeiter:generate -o ../mocks/pr-poster.go --fake-name PrPoster . PrPoster
//counterfeiter:generate -o ../mocks/review-verifier.go --fake-name ReviewVerifier . ReviewVerifier

// Hallucination describes a single file-reference that ai_review flagged
// as fabricated (the file or line does not exist in the diff).
type Hallucination struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Issue string `json:"issue"`
}

// PrPoster posts a completed review verdict to GitHub as a pull-request review event.
// The concrete implementation lives in pkg/githubposter and is wired by the factory.
// Defining the interface in pkg (rather than pkg/githubposter) breaks the import cycle:
// pkg/githubposter already imports pkg for PRInfo/Verdict.
type PrPoster interface {
	Post(ctx context.Context, req PostRequest) PostResult
	// PostLGTM posts a COMMENT review with body "Reviewed by <botLogin> — no
	// concerns flagged." event is always "COMMENT"; workDir is ignored.
	//
	// Deprecated: planning no longer calls this — the "no concerns → LGTM"
	// shortcut was removed so every PR gets a real execution review that posts an
	// earned APPROVE/REQUEST_CHANGES verdict. Retained for backward compatibility
	// with cmd/run-task tooling and slated for removal.
	PostLGTM(ctx context.Context, pr prurl.PRInfo, headSHA, workDir, botLogin string) PostResult
	// PostOverrideApprove posts an APPROVE review at headSHA unconditionally
	// (no autoApprove gate, no clone/WorkDir). Used by the `pr-override` task
	// type to clear a false-positive review: a fresh bot APPROVE supersedes the
	// bot's own prior CHANGES_REQUESTED for reviewDecision, so a trusted author
	// who applied the override label can merge without admin.
	PostOverrideApprove(ctx context.Context, pr prurl.PRInfo, headSHA, body string) PostResult
	// DismissCurrentReview dismisses the bot's APPROVED or CHANGES_REQUESTED
	// review at the current head SHA, then posts a follow-up COMMENT review
	// citing each hallucination. A no-matching-review case is a non-error
	// no-op (returns success with FailureStep="dismiss-current-noop"). A
	// dismissal failure returns a failed PostResult; a COMMENT-post failure
	// after a successful dismissal still returns success — the merge gate
	// is already cleared.
	DismissCurrentReview(
		ctx context.Context,
		pr prurl.PRInfo,
		headSHA string,
		hallucinations []Hallucination,
	) PostResult
}

// PostRequest carries all inputs needed for a single posting sequence.
type PostRequest struct {
	PR      prurl.PRInfo
	HeadSHA string
	Verdict Verdict
	Summary string
	WorkDir string
}

// PostResult carries all diagnostic fields needed for the ## Diagnostics block.
type PostResult struct {
	Outcome      string
	ReviewID     int64
	PostedEvent  string
	FailureStep  string
	Class        ErrorClass
	EscalateHint bool
	Attempt      int
	HTTPStatus   int
	ErrorMessage string
	ResponseBody string
	ElapsedMs    int64
	Warnings     []string
}

// ErrorClass categorizes a posting failure for retry and escalation decisions.
type ErrorClass string

const (
	// ErrorClassTransient indicates a transient failure that may succeed on retry.
	ErrorClassTransient ErrorClass = "transient"
	// ErrorClassPermanent indicates a permanent failure that will not succeed on retry.
	ErrorClassPermanent ErrorClass = "permanent"
	// ErrorClassUnknown indicates an unknown error class.
	ErrorClassUnknown ErrorClass = "unknown"
	// ErrorClassNotAFailure indicates a non-failure outcome (e.g., 422 PR closed).
	ErrorClassNotAFailure ErrorClass = "not-a-failure"
	// ErrorClassSoftWarning indicates a soft warning that does not block posting.
	ErrorClassSoftWarning ErrorClass = "soft-warning"
)

// ReviewVerifier confirms that the in_progress step's POST persisted on GitHub.
// The concrete implementation lives in pkg/githubposter and is wired by the factory.
// Defining the interface in pkg breaks the import cycle (githubposter already imports pkg).
type ReviewVerifier interface {
	VerifyReview(ctx context.Context, req VerifyRequest) VerifyResult
}

// VerifyRequest carries all inputs for a single review-existence check.
type VerifyRequest struct {
	PR             prurl.PRInfo
	HeadSHA        string
	ExpectedStates []string
}

// VerifyResult carries all diagnostic fields from the ai_review verification GET.
type VerifyResult struct {
	Found        bool
	Outcome      string
	FoundState   string
	FailureStep  string
	Class        ErrorClass
	EscalateHint bool
	Attempt      int
	HTTPStatus   int
	ErrorMessage string
	ResponseBody string
	ElapsedMs    int64
}
