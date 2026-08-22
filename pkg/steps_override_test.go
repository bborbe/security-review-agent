// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("overrideStep", func() {
	var (
		ctx    context.Context
		poster *mocks.PrPoster
		step   agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		poster = &mocks.PrPoster{}
		step = pkg.NewOverrideStep(poster, "ben-s-pull-request-reviewer[bot]")
	})

	It("has name pr-override", func() {
		Expect(step.Name()).To(Equal("pr-override"))
	})

	It("ShouldRun always returns true", func() {
		md, err := agentlib.ParseMarkdown(ctx, "# PR Override\n")
		Expect(err).NotTo(HaveOccurred())
		run, err := step.ShouldRun(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(run).To(BeTrue())
	})

	Describe("Run", func() {
		It("posts APPROVE at head SHA and routes to human_review", func() {
			poster.PostOverrideApproveReturns(pkg.PostResult{
				Outcome: "success", ReviewID: 99, PostedEvent: "APPROVE",
			})
			md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Override

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())

			Expect(poster.PostOverrideApproveCallCount()).To(Equal(1))
			_, prInfo, headSHA, body := poster.PostOverrideApproveArgsForCall(0)
			Expect(prInfo.Owner).To(Equal("bborbe"))
			Expect(prInfo.Repo).To(Equal("maintainer"))
			Expect(prInfo.Number).To(Equal(14))
			Expect(headSHA).To(Equal("abc123"))
			Expect(body).To(ContainSubstring("override-review"))

			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))

			section, ok := md.FindSection("## Override")
			Expect(ok).To(BeTrue())
			Expect(section.Body).To(ContainSubstring("APPROVE"))
		})

		It("fails when ref (head SHA) is missing", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Override

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(poster.PostOverrideApproveCallCount()).To(Equal(0))
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		})

		It("routes to human_review when no PR URL is present", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Override

no url here
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(poster.PostOverrideApproveCallCount()).To(Equal(0))
			Expect(result.NextPhase).To(Equal("human_review"))
		})

		It("fails the step when the APPROVE post fails", func() {
			poster.PostOverrideApproveReturns(pkg.PostResult{
				Outcome: "failed", FailureStep: "POST /pulls/N/reviews", HTTPStatus: 500,
			})
			md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Override

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		})
	})
})
