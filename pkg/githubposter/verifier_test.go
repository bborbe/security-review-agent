// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter_test

import (
	"context"
	stderrors "errors"
	"net/http"

	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/githubposter"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	prpkg "github.com/bborbe/maintainer/prurl"
)

var _ = Describe("ReviewVerifier", func() {
	var (
		fakeClient *mocks.HTTPClient
		verifier   pkg.ReviewVerifier
		pr         prpkg.PRInfo
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		fakeClient = &mocks.HTTPClient{}
		currentDateTime := libtime.NewCurrentDateTime()
		verifier = githubposter.NewReviewVerifier(
			fakeClient,
			"test-token",
			testBotLogin,
			currentDateTime,
		)
		pr = prpkg.PRInfo{Owner: "owner", Repo: "repo", Number: 1}
	})

	req := func(states ...string) pkg.VerifyRequest {
		return pkg.VerifyRequest{
			PR:             pr,
			HeadSHA:        testHeadSHA,
			ExpectedStates: states,
		}
	}

	Context("review found with matching state", func() {
		It("returns Found:true and the state", func() {
			body := reviewListJSON(reviewJSON(42, testBotLogin, testHeadSHA, "APPROVED"))
			fakeClient.DoReturns(makeHTTPResp(200, body), nil)
			result := verifier.VerifyReview(ctx, req("APPROVED"))
			Expect(result.Found).To(BeTrue())
			Expect(result.Outcome).To(Equal("success"))
			Expect(result.FoundState).To(Equal("APPROVED"))
			Expect(result.Attempt).To(Equal(1))
		})
	})

	Context("review absent first attempt, found on second", func() {
		It("retries and returns Found:true with Attempt:2", func() {
			body := reviewListJSON(reviewJSON(42, testBotLogin, testHeadSHA, "APPROVED"))
			fakeClient.DoReturnsOnCall(0, makeHTTPResp(200, reviewListJSON()), nil)
			fakeClient.DoReturnsOnCall(1, makeHTTPResp(200, body), nil)
			result := verifier.VerifyReview(ctx, req("APPROVED"))
			Expect(result.Found).To(BeTrue())
			Expect(result.Outcome).To(Equal("success"))
			Expect(result.Attempt).To(Equal(2))
		})
	})

	Context("review absent both attempts", func() {
		It("returns Found:false with transient class", func() {
			fakeClient.DoStub = func(_ *http.Request) (*http.Response, error) {
				return makeHTTPResp(200, reviewListJSON()), nil
			}
			result := verifier.VerifyReview(ctx, req("APPROVED"))
			Expect(result.Found).To(BeFalse())
			Expect(result.Outcome).To(Equal("failed"))
			Expect(result.Class).To(Equal(pkg.ErrorClassTransient))
			Expect(result.Attempt).To(Equal(2))
		})
	})

	Context("GET returns 404", func() {
		It("returns permanent failure without retry", func() {
			fakeClient.DoReturns(makeHTTPResp(404, `{"message":"Not Found"}`), nil)
			result := verifier.VerifyReview(ctx, req("APPROVED"))
			Expect(result.Found).To(BeFalse())
			Expect(result.Outcome).To(Equal("failed"))
			Expect(result.Class).To(Equal(pkg.ErrorClassPermanent))
			Expect(result.EscalateHint).To(BeTrue())
			Expect(result.Attempt).To(Equal(1))
			Expect(fakeClient.DoCallCount()).To(Equal(1))
		})
	})

	Context("GET returns 429 then 200 with review", func() {
		It("retries on rate limit and returns Found:true", func() {
			body := reviewListJSON(reviewJSON(42, testBotLogin, testHeadSHA, "APPROVED"))
			fakeClient.DoReturnsOnCall(0, makeHTTPResp(429, `{"message":"rate limited"}`), nil)
			fakeClient.DoReturnsOnCall(1, makeHTTPResp(200, body), nil)
			result := verifier.VerifyReview(ctx, req("APPROVED"))
			Expect(result.Found).To(BeTrue())
			Expect(result.Outcome).To(Equal("success"))
			Expect(result.Attempt).To(Equal(2))
		})
	})

	Context("network error", func() {
		It("returns transient class on connection failure", func() {
			fakeClient.DoReturns(nil, stderrors.New("connection refused"))
			result := verifier.VerifyReview(ctx, req("APPROVED"))
			Expect(result.Found).To(BeFalse())
			Expect(result.Outcome).To(Equal("failed"))
			Expect(result.Class).To(Equal(pkg.ErrorClassTransient))
		})
	})

	Context(
		"verifier hard-excludes COMMENTED even when caller's allow-list includes it (spec 060)",
		func() {
			It(
				"returns Found:false — verifier's internal exclusion overrides ExpectedStates",
				func() {
					// Even though the caller passes ExpectedStates=["COMMENTED"], the verifier's
					// internal hard-exclusion (findReview skips COMMENTED) means a COMMENTED review
					// at the head SHA is treated as a non-match. This documents the defense-in-depth
					// invariant: COMMENTED is never a valid fresh-review state, independent of caller.
					body := reviewListJSON(reviewJSON(42, testBotLogin, testHeadSHA, "COMMENTED"))
					fakeClient.DoStub = func(_ *http.Request) (*http.Response, error) {
						return makeHTTPResp(200, body), nil
					}
					result := verifier.VerifyReview(ctx, req("COMMENTED"))
					Expect(result.Found).To(BeFalse())
					Expect(result.Outcome).To(Equal("failed"))
					Expect(result.Class).To(Equal(pkg.ErrorClassTransient))
					// parity with existing "review absent both attempts" — phantom-POST exhausts retries
					Expect(result.Attempt).To(Equal(2))
				},
			)

			It(
				"finds APPROVED review even when a stale COMMENTED exists at the same head SHA",
				func() {
					// Defense-in-depth coexistence test: a pre-fix stale COMMENTED at the head SHA
					// must not shadow a fresh APPROVED at the same SHA. findReview skips the
					// COMMENTED entry and returns the APPROVED one.
					body := reviewListJSON(
						reviewJSON(41, testBotLogin, testHeadSHA, "COMMENTED"),
						reviewJSON(42, testBotLogin, testHeadSHA, "APPROVED"),
					)
					fakeClient.DoStub = func(_ *http.Request) (*http.Response, error) {
						return makeHTTPResp(200, body), nil
					}
					result := verifier.VerifyReview(ctx, req("APPROVED"))
					Expect(result.Found).To(BeTrue())
					Expect(result.Outcome).To(Equal("success"))
					Expect(result.FoundState).To(Equal("APPROVED"))
				},
			)
		},
	)
})
