// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	stderrors "errors"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/github-pr-review-agent/mocks"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResolvePosters", func() {
	var (
		botLogin string
	)

	BeforeEach(func() {
		botLogin = "ben-s-pull-request-reviewer[bot]"
	})

	Describe("SkipPost: true", func() {
		It("returns nil interfaces", func() {
			cfg := factory.RunConfig{SkipPost: true}
			poster, verifier := factory.ResolvePosters(cfg, botLogin)
			Expect(poster).To(BeNil())
			Expect(verifier).To(BeNil())
		})
	})

	Describe("SkipPost: false", func() {
		It("returns non-nil interfaces", func() {
			cfg := factory.RunConfig{
				SkipPost:        false,
				GHToken:         "ghp_testtoken",
				CurrentDateTime: libtime.NewCurrentDateTime(),
			}
			poster, verifier := factory.ResolvePosters(cfg, botLogin)
			Expect(poster).NotTo(BeNil())
			Expect(verifier).NotTo(BeNil())
		})
	})
})

var _ = Describe("DeliverStartupFailure", func() {
	var (
		ctx       context.Context
		deliverer *mocks.ResultDeliverer
	)

	BeforeEach(func() {
		ctx = context.Background()
		deliverer = &mocks.ResultDeliverer{}
	})

	It("publishes Failed result containing the wrapped error before returning", func() {
		// Mirrors the prod incident: gh auth setup-git captured a real stderr
		// message; the deliverer must surface it as Status: Failed so the
		// passthrough content generator splices it into the task ## Failure body.
		const stderrText = "X11 connection rejected because of wrong authentication"
		cause := stderrors.New("gh auth setup-git failed: " + stderrText)

		returned := factory.DeliverStartupFailure(ctx, deliverer, cause, "github auth setup failed")

		Expect(returned).To(HaveOccurred())
		Expect(returned.Error()).To(ContainSubstring("github auth setup failed"))
		Expect(returned.Error()).To(ContainSubstring(stderrText))

		Expect(deliverer.DeliverResultCallCount()).To(Equal(1))
		_, info := deliverer.DeliverResultArgsForCall(0)
		Expect(info.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(info.Message).To(ContainSubstring("github auth setup failed"))
		Expect(info.Message).To(ContainSubstring(stderrText))
	})

	It("still returns the original wrapped error when delivery itself fails", func() {
		// Delivery errors must not mask the original startup cause — that's what
		// operators need to see in pod logs even if Kafka/file delivery is broken.
		deliverer.DeliverResultReturns(stderrors.New("kafka unreachable"))
		cause := stderrors.New("gh exited 1")

		returned := factory.DeliverStartupFailure(ctx, deliverer, cause, "github auth setup failed")

		Expect(returned).To(HaveOccurred())
		Expect(returned.Error()).To(ContainSubstring("github auth setup failed"))
		Expect(returned.Error()).To(ContainSubstring("gh exited 1"))
		Expect(returned.Error()).NotTo(ContainSubstring("kafka unreachable"))
		Expect(deliverer.DeliverResultCallCount()).To(Equal(1))
	})
})
