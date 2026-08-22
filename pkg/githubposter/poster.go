// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	errors "github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	prurl "github.com/bborbe/maintainer/prurl"
)

type prPoster struct {
	httpClient      HTTPClient
	ghToken         string
	botLogin        string
	currentDateTime libtime.CurrentDateTimeGetter
}

// NewPrPoster creates a prpkg.PrPoster. botLogin must already be resolved by the caller.
func NewPrPoster(
	httpClient HTTPClient,
	ghToken string,
	botLogin string,
	currentDateTime libtime.CurrentDateTimeGetter,
) prpkg.PrPoster {
	return &prPoster{
		httpClient:      httpClient,
		ghToken:         ghToken,
		botLogin:        botLogin,
		currentDateTime: currentDateTime,
	}
}

// reviewEntry is the GitHub API shape for a single pull-request review.
type reviewEntry struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	CommitID string `json:"commit_id"`
	State    string `json:"state"`
}

type postReviewReq struct {
	Event    string `json:"event"`
	CommitID string `json:"commit_id"`
	Body     string `json:"body"`
}

type postReviewResp struct {
	ID int64 `json:"id"`
}

// PostLGTM posts a COMMENT review with body "Reviewed by <botLogin> — no
// concerns flagged." workDir is ignored.
//
// Deprecated: the planning step no longer posts an LGTM shortcut — every PR is
// routed to the execution phase for a real review that posts an earned
// APPROVE/REQUEST_CHANGES verdict (a shallow "no concerns" pass must not
// rubber-stamp a positive review). This method is retained on the interface for
// backward compatibility with cmd/run-task tooling and is slated for removal.
func (p *prPoster) PostLGTM(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA, workDir, botLogin string,
) prpkg.PostResult {
	start := time.Time(p.currentDateTime.Now())
	const event = "COMMENT"
	body := fmt.Sprintf("Reviewed by %s — no concerns flagged.", botLogin)

	reviewID, result, proceed := p.postReview(ctx, pr, headSHA, event, body)
	if !proceed {
		result.ElapsedMs = time.Since(start).Milliseconds()
		return result
	}
	result = p.verifyAfterPost(ctx, pr, headSHA, event, nil)
	result.ReviewID = reviewID
	result.PostedEvent = event
	result.ElapsedMs = time.Since(start).Milliseconds()
	return result
}

// PostOverrideApprove posts an APPROVE review at headSHA with the given body,
// unconditionally — no autoApprove gate, no clone/WorkDir, no prior-review
// dismissal. A fresh bot APPROVE supersedes the bot's own prior
// CHANGES_REQUESTED for reviewDecision, so the false-positive review no longer
// blocks merge. Mirrors PostLGTM's post + verify shape with event "APPROVE".
func (p *prPoster) PostOverrideApprove(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA, body string,
) prpkg.PostResult {
	start := time.Time(p.currentDateTime.Now())
	const event = "APPROVE"

	reviewID, result, proceed := p.postReview(ctx, pr, headSHA, event, body)
	if !proceed {
		result.ElapsedMs = time.Since(start).Milliseconds()
		return result
	}
	result = p.verifyAfterPost(ctx, pr, headSHA, event, nil)
	result.ReviewID = reviewID
	result.PostedEvent = event
	result.ElapsedMs = time.Since(start).Milliseconds()
	return result
}

func (p *prPoster) Post(ctx context.Context, req prpkg.PostRequest) prpkg.PostResult {
	start := time.Time(p.currentDateTime.Now())
	autoApprove, err := ReadAutoApprove(ctx, req.WorkDir)
	if err != nil {
		return prpkg.PostResult{
			Outcome:      "failed",
			FailureStep:  "read .maintainer.yaml",
			Class:        prpkg.ErrorClassPermanent,
			EscalateHint: true,
			Attempt:      1,
			ErrorMessage: err.Error(),
			ElapsedMs:    time.Since(start).Milliseconds(),
		}
	}
	if result, ok := p.dismissPriorReviews(ctx, req.PR, req.HeadSHA); !ok {
		result.ElapsedMs = time.Since(start).Milliseconds()
		return result
	}
	event, body, warnings := mapVerdictAndSummary(req.Verdict, autoApprove, req.Summary)
	result := p.postAndVerify(ctx, req.PR, req.HeadSHA, event, body, warnings)
	result.ElapsedMs = time.Since(start).Milliseconds()
	return result
}

func (p *prPoster) dismissPriorReviews(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA string,
) (prpkg.PostResult, bool) {
	step := "GET /pulls/N/reviews (dismiss-list)"
	reviews, result, ok := p.listBotReviews(ctx, pr, headSHA, step)
	if !ok {
		return result, false
	}
	for _, r := range reviews {
		if r.State == "DISMISSED" {
			continue
		}
		if result, ok := p.dismissOne(ctx, pr, r.ID); !ok {
			return result, false
		}
	}
	return prpkg.PostResult{}, true
}

func (p *prPoster) listBotReviews(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA, step string,
) ([]reviewEntry, prpkg.PostResult, bool) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls/%d/reviews",
		pr.Owner,
		pr.Repo,
		pr.Number,
	)
	cr := retryCall(ctx, step, func(ctx context.Context) ([]reviewEntry, int, string, error) {
		status, body, err := doRequest(ctx, p.httpClient, p.ghToken, "GET", url, nil)
		if err != nil {
			return nil, status, truncateBody(body), err
		}
		if status != 200 {
			return nil, status, truncateBody(
					body,
				), errors.Errorf(
					ctx,
					"unexpected status %d",
					status,
				)
		}
		var all []reviewEntry
		if err := json.Unmarshal(body, &all); err != nil {
			return nil, status, truncateBody(body), errors.Wrapf(ctx, err, "parse reviews")
		}
		var filtered []reviewEntry
		for _, r := range all {
			// Skip COMMENTED reviews: GitHub's API rejects dismissal of comment-state
			// reviews with HTTP 422 "Can not dismiss a commented pull request review".
			// Comment reviews don't block merges anyway, so leaving them stacked is harmless.
			// Only APPROVED / CHANGES_REQUESTED affect the merge gate and require dismissal.
			// Invariant (spec 031, docs/pr-post-back.md §Dismissal Contract):
			// reviews at the current head SHA are NEVER dismissed — only reviews at
			// superseded (prior) SHAs are eligible. A re-spawned pod must not wipe the
			// review that a previous pod left at the same head.
			if r.User.Login == p.botLogin && r.CommitID != headSHA && r.State != "COMMENTED" {
				filtered = append(filtered, r)
			}
		}
		return filtered, status, truncateBody(body), nil
	})
	if cr.Err != nil {
		return nil, buildFailedResult(step, cr), false
	}
	return cr.Value, prpkg.PostResult{}, true
}

func (p *prPoster) dismissOne(
	ctx context.Context,
	pr prurl.PRInfo,
	reviewID int64,
) (prpkg.PostResult, bool) {
	step := "PUT .../dismissals"
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls/%d/reviews/%d/dismissals",
		pr.Owner, pr.Repo, pr.Number, reviewID,
	)
	payload := []byte(`{"message":"superseded by new automated review"}`)
	cr := retryCall(ctx, step, func(ctx context.Context) (struct{}, int, string, error) {
		status, body, err := doRequest(
			ctx,
			p.httpClient,
			p.ghToken,
			"PUT",
			url,
			bytes.NewReader(payload),
		)
		if err != nil {
			return struct{}{}, status, truncateBody(body), err
		}
		if status < 200 || status >= 300 {
			return struct{}{}, status, truncateBody(
					body,
				), errors.Errorf(
					ctx,
					"unexpected status %d",
					status,
				)
		}
		return struct{}{}, status, truncateBody(body), nil
	})
	if cr.Err != nil {
		return buildFailedResult(step, cr), false
	}
	return prpkg.PostResult{}, true
}

// listBotReviewsAtHead returns bot reviews at the exact head SHA with state
// APPROVED or CHANGES_REQUESTED. Unlike listBotReviews (which intentionally
// excludes head-SHA reviews for the dismissPriorReviews path), this targets
// only the current-head review for the hallucination-dismissal path.
func (p *prPoster) listBotReviewsAtHead(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA string,
) ([]reviewEntry, prpkg.PostResult, bool) {
	step := "GET /pulls/N/reviews (dismiss-current)"
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls/%d/reviews",
		pr.Owner,
		pr.Repo,
		pr.Number,
	)
	cr := retryCall(ctx, step, func(ctx context.Context) ([]reviewEntry, int, string, error) {
		status, body, err := doRequest(ctx, p.httpClient, p.ghToken, "GET", url, nil)
		if err != nil {
			return nil, status, truncateBody(body), err
		}
		if status != 200 {
			return nil, status, truncateBody(
					body,
				), errors.Errorf(
					ctx,
					"unexpected status %d",
					status,
				)
		}
		var all []reviewEntry
		if err := json.Unmarshal(body, &all); err != nil {
			return nil, status, truncateBody(body), errors.Wrapf(ctx, err, "parse reviews")
		}
		var filtered []reviewEntry
		for _, r := range all {
			if r.User.Login == p.botLogin && r.CommitID == headSHA &&
				(r.State == "APPROVED" || r.State == "CHANGES_REQUESTED") {
				filtered = append(filtered, r)
			}
		}
		return filtered, status, truncateBody(body), nil
	})
	if cr.Err != nil {
		return nil, buildFailedResult(step, cr), false
	}
	return cr.Value, prpkg.PostResult{}, true
}

// dismissPayload is the JSON body sent to PUT .../reviews/{id}/dismissals
// when the bot's own review has been flagged hallucinated by ai_review.
var dismissPayload = []byte(
	`{"message":"hallucinated review — see follow-up COMMENT for evidence"}`,
)

// buildHallucinationCommentBody builds the COMMENT review body for the
// follow-up comment after dismissing a hallucinated review. The opening
// line does NOT repeat the dismissal sentence (which already appears in
// the dismissed review's "reason"); it leads with the evidence header
// the reader needs.
func buildHallucinationCommentBody(hallucinations []prpkg.Hallucination) string {
	if len(hallucinations) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Hallucinations flagged by ai_review (file:line not in diff):\n\n")
	for _, h := range hallucinations {
		fmt.Fprintf(&sb, "- %s:%d — %s\n", h.File, h.Line, h.Issue)
	}
	return sb.String()
}

// noopDismissResult is the success no-op return for the empty-headSHA and
// no-matching-review paths.
var noopDismissResult = prpkg.PostResult{
	Outcome:     "success",
	FailureStep: "dismiss-current-noop",
	HTTPStatus:  0,
}

// executeDismissPUT issues PUT .../reviews/{id}/dismissals via the existing
// retryCall + doRequest plumbing and returns the HTTP status from the call
// alongside the retryCall outcome. Emits a glog line on completion so pod
// logs carry an audit trail of every dismissal attempt.
func (p *prPoster) executeDismissPUT(
	ctx context.Context,
	pr prurl.PRInfo,
	reviewID int64,
) CallResult[struct{}] {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls/%d/reviews/%d/dismissals",
		pr.Owner, pr.Repo, pr.Number, reviewID,
	)
	step := fmt.Sprintf("PUT /pulls/%d/reviews/%d/dismissals", pr.Number, reviewID)
	cr := retryCall(ctx, step, func(ctx context.Context) (struct{}, int, string, error) {
		status, body, err := doRequest(
			ctx, p.httpClient, p.ghToken, "PUT", url, bytes.NewReader(dismissPayload),
		)
		if err != nil {
			return struct{}{}, status, truncateBody(body), err
		}
		if status < 200 || status >= 300 {
			return struct{}{}, status, truncateBody(body),
				errors.Errorf(ctx, "unexpected status %d", status)
		}
		return struct{}{}, status, truncateBody(body), nil
	})
	glog.Infof(
		"dismiss-current: step=%s http_status=%d attempts=%d err=%v",
		step, cr.HTTPStatus, cr.Attempts, cr.Err,
	)
	return cr
}

// postHallucinationComment posts the follow-up COMMENT review citing each
// hallucination after a successful dismissal.
//
// Outcome semantics: the returned PostResult always carries Outcome="success"
// because the dismissal has already mutated PR review state — the merge gate
// is cleared regardless of what happens to the COMMENT POST. A failed COMMENT
// POST is captured in FailureStep + HTTPStatus + ErrorMessage for the
// operator's diagnostic trail but is deliberately NOT promoted to
// Outcome="failed" — doing so would misleadingly signal "dismissal didn't
// happen" when in fact it did. The 422 case (e.g. PR closed between dismiss
// and comment) is handled the same way: the dismissal stands, the failure is
// recorded, the operator can grep `## Diagnostics` if context is needed.
func (p *prPoster) postHallucinationComment(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA string,
	hallucinations []prpkg.Hallucination,
) prpkg.PostResult {
	body := buildHallucinationCommentBody(hallucinations)
	if len(body) > maxGitHubCommentBody {
		keep := maxGitHubCommentBody - len(maxGitHubCommentBodyNotice)
		if keep < 0 {
			keep = 0
		}
		body = body[:keep] + maxGitHubCommentBodyNotice
	}
	commentStep := fmt.Sprintf("POST /pulls/%d/reviews (comment-after-dismiss)", pr.Number)
	_, commentResult, proceed := p.postReview(ctx, pr, headSHA, "COMMENT", body)
	if !proceed {
		glog.Warningf(
			"dismiss-current: %s failed http_status=%d err=%s",
			commentStep, commentResult.HTTPStatus, commentResult.ErrorMessage,
		)
		return prpkg.PostResult{
			Outcome:      "success",
			FailureStep:  commentStep,
			HTTPStatus:   commentResult.HTTPStatus,
			ErrorMessage: commentResult.ErrorMessage,
		}
	}
	glog.Infof("dismiss-current: %s http_status=200", commentStep)
	// postReview returns an empty PostResult on success — HTTPStatus is set
	// from the wire only on failure. Use 200 as the success indicator so AC
	// verification can grep http_status: 200 in the diagnostics block.
	return prpkg.PostResult{
		Outcome:    "success",
		HTTPStatus: 200,
	}
}

// DismissCurrentReview dismisses every APPROVED or CHANGES_REQUESTED bot
// review at the current head SHA and posts a single follow-up COMMENT citing
// the hallucinations. See PrPoster.DismissCurrentReview for full semantics.
//
// Multiple-review handling: in practice the bot posts at most one
// review-with-verdict per (PR, SHA) cycle, so the list is almost always
// length 1. The loop is a belt-and-suspenders guard for the edge case where
// a previous trigger somehow left an undismissed sibling — we dismiss them
// all rather than silently leaving one to keep blocking the merge gate.
func (p *prPoster) DismissCurrentReview(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA string,
	hallucinations []prpkg.Hallucination,
) prpkg.PostResult {
	if headSHA == "" {
		return noopDismissResult
	}
	reviews, result, ok := p.listBotReviewsAtHead(ctx, pr, headSHA)
	if !ok {
		return result
	}
	if len(reviews) == 0 {
		return noopDismissResult
	}
	for _, review := range reviews {
		cr := p.executeDismissPUT(ctx, pr, review.ID)
		if cr.Err != nil {
			return buildFailedResult(
				fmt.Sprintf("PUT /pulls/%d/reviews/%d/dismissals", pr.Number, review.ID),
				cr,
			)
		}
	}
	return p.postHallucinationComment(ctx, pr, headSHA, hallucinations)
}

func (p *prPoster) postAndVerify(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA, event, body string,
	warnings []string,
) prpkg.PostResult {
	reviewID, result, proceed := p.postReview(ctx, pr, headSHA, event, body)
	if !proceed {
		result.Warnings = warnings
		return result
	}
	result = p.verifyAfterPost(ctx, pr, headSHA, event, warnings)
	result.ReviewID = reviewID
	result.PostedEvent = event
	return result
}

func (p *prPoster) postReview(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA, event, body string,
) (int64, prpkg.PostResult, bool) {
	const step = "POST /pulls/N/reviews"
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls/%d/reviews",
		pr.Owner,
		pr.Repo,
		pr.Number,
	)
	payload, err := json.Marshal(postReviewReq{Event: event, CommitID: headSHA, Body: body})
	if err != nil {
		return 0, prpkg.PostResult{
			Outcome: "failed", FailureStep: step,
			Class: prpkg.ErrorClassPermanent, EscalateHint: true, Attempt: 1, ErrorMessage: err.Error(),
		}, false
	}
	cr := retryCall(ctx, step, func(ctx context.Context) (postReviewResp, int, string, error) {
		status, rb, err := doRequest(
			ctx,
			p.httpClient,
			p.ghToken,
			"POST",
			url,
			bytes.NewReader(payload),
		)
		tb := truncateBody(rb)
		if err != nil {
			return postReviewResp{}, status, tb, err
		}
		if status == 422 {
			return postReviewResp{}, status, tb, errors.Errorf(
				ctx,
				"PR closed or validation error (422)",
			)
		}
		if status < 200 || status >= 300 {
			return postReviewResp{}, status, tb, errors.Errorf(ctx, "unexpected status %d", status)
		}
		var r postReviewResp
		if err := json.Unmarshal(rb, &r); err != nil {
			return postReviewResp{}, status, tb, errors.Wrapf(ctx, err, "parse POST response")
		}
		return r, status, tb, nil
	})
	if cr.HTTPStatus == 422 {
		return 0, prpkg.PostResult{
			Outcome: "success", Class: prpkg.ErrorClassNotAFailure,
			FailureStep: step, HTTPStatus: 422, Attempt: cr.Attempts,
		}, false
	}
	if cr.Err != nil {
		return 0, buildFailedResult(step, cr), false
	}
	return cr.Value.ID, prpkg.PostResult{}, true
}

func (p *prPoster) verifyAfterPost(
	ctx context.Context,
	pr prurl.PRInfo,
	headSHA, event string,
	warnings []string,
) prpkg.PostResult {
	step := "GET /pulls/N/reviews (verify)"
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls/%d/reviews",
		pr.Owner,
		pr.Repo,
		pr.Number,
	)
	expectedState := eventToState(event)
	cr := retryCall(ctx, step, func(ctx context.Context) (bool, int, string, error) {
		status, body, err := doRequest(ctx, p.httpClient, p.ghToken, "GET", url, nil)
		if err != nil {
			return false, status, truncateBody(body), err
		}
		if status != 200 {
			return false, status, truncateBody(
					body,
				), errors.Errorf(
					ctx,
					"unexpected status %d",
					status,
				)
		}
		var reviews []reviewEntry
		if err := json.Unmarshal(body, &reviews); err != nil {
			return false, status, truncateBody(body), errors.Wrapf(ctx, err, "parse reviews")
		}
		for _, r := range reviews {
			if r.User.Login == p.botLogin && r.CommitID == headSHA && r.State == expectedState {
				return true, status, truncateBody(body), nil
			}
		}
		return false, 200, truncateBody(body), errPhantomPOST
	})
	if errors.Is(cr.Err, errPhantomPOST) {
		return prpkg.PostResult{
			Outcome:      "failed",
			FailureStep:  step,
			Class:        prpkg.ErrorClassTransient,
			Attempt:      cr.Attempts,
			ErrorMessage: "phantom POST: review absent in GET after POST",
			Warnings:     warnings,
		}
	}
	if cr.Err != nil {
		r := buildFailedResult(step, cr)
		r.Warnings = warnings
		return r
	}
	return prpkg.PostResult{
		Outcome:    "success",
		HTTPStatus: cr.HTTPStatus,
		Attempt:    cr.Attempts,
		Warnings:   warnings,
	}
}

// doRequest executes an HTTP request with GitHub auth headers and returns the full response body.
// The caller is responsible for truncating the body if storing for diagnostics.
func doRequest(
	ctx context.Context,
	client HTTPClient,
	token, method, rawURL string,
	body io.Reader,
) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, nil, errors.Wrapf(ctx, err, "create request %s %s", method, rawURL)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, errors.Wrapf(ctx, err, "do request %s %s", method, rawURL)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, errors.Wrapf(ctx, err, "read response body")
	}
	return resp.StatusCode, bodyBytes, nil
}

// truncateBody returns at most 500 bytes of the body as a string, for diagnostics.
func truncateBody(b []byte) string {
	if len(b) > 500 {
		return string(b[:500])
	}
	return string(b)
}

// maxGitHubCommentBody is the GitHub API's hard limit for PR review body /
// issue comment / PR description text: 65,536 characters. Bodies longer than
// this are rejected with HTTP 422 "Body is too long". We truncate one
// character below the limit and append a trailing notice so the operator can
// see the truncation happened in-band (the rest is captured in the vault
// `## Review` section).
//
// Reference: https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request
const maxGitHubCommentBody = 65536

// maxGitHubCommentBodyNotice is appended when truncation fires. Kept short so
// the truncation budget for the actual review content is maximized.
const maxGitHubCommentBodyNotice = "\n\n…[truncated to 65 KiB GitHub limit; full review in vault task ## Review section]"

// mapVerdictAndSummary maps verdict + autoApprove to a GitHub review event and body.
// Empty summary is substituted with a default and recorded as a soft-warning.
// Over-length bodies are truncated to 65,536 chars (the GitHub API limit).
//
// autoApprove is reserved for future per-repo gating; the verdict alone decides
// the event today. The parameter is preserved in the signature for backward
// compatibility with operator tooling that sets the field.
func mapVerdictAndSummary(
	verdict prpkg.Verdict,
	autoApprove bool,
	summary string,
) (event, body string, warnings []string) { //nolint:unparam // autoApprove reserved for future per-repo gating (spec 060)
	_ = autoApprove
	switch verdict {
	case prpkg.VerdictRequestChanges:
		event = "REQUEST_CHANGES"
	case prpkg.VerdictApprove:
		event = "APPROVE"
	}
	if summary == "" {
		summary = "automated review — no summary produced"
		warnings = []string{"soft-warning: empty summary substituted with default"}
	}
	body += summary

	if len(body) > maxGitHubCommentBody {
		// Truncate the body so prefix + content + notice ≤ 65,536 chars.
		// Reserve space for the notice; cut summary content from the end (the
		// JSON verdict block at the end is parseable independently from vault).
		keep := maxGitHubCommentBody - len(maxGitHubCommentBodyNotice)
		if keep < 0 {
			keep = 0
		}
		body = body[:keep] + maxGitHubCommentBodyNotice
		warnings = append(warnings, "soft-warning: body truncated to GitHub's 65536-char limit")
	}
	return event, body, warnings
}

// eventToState converts a GitHub review event string to its resulting state string.
func eventToState(event string) string {
	switch event {
	case "APPROVE":
		return "APPROVED"
	case "REQUEST_CHANGES":
		return "CHANGES_REQUESTED"
	default:
		return "COMMENTED"
	}
}

// buildFailedResult builds a PostResult representing a failed step from a CallResult.
func buildFailedResult[T any](step string, cr CallResult[T]) prpkg.PostResult {
	msg := ""
	if cr.Err != nil {
		msg = cr.Err.Error()
	}
	return prpkg.PostResult{
		Outcome:      "failed",
		FailureStep:  step,
		Class:        cr.Class,
		EscalateHint: cr.Class == prpkg.ErrorClassPermanent || cr.Class == prpkg.ErrorClassUnknown,
		Attempt:      cr.Attempts,
		HTTPStatus:   cr.HTTPStatus,
		ErrorMessage: msg,
		ResponseBody: cr.ResponseBody,
	}
}
