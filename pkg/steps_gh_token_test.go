// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	agentlib "github.com/bborbe/agent"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GHTokenCheckStep", func() {
	var (
		ctx context.Context
		md  *agentlib.Markdown
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		md, err = agentlib.ParseMarkdown(ctx, "---\nfoo: bar\n---\n\nbody")
		Expect(err).NotTo(HaveOccurred())
	})

	It("returns needs_input when token is empty", func() {
		step := pkg.NewGHTokenCheckStep("")
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(got.Message).To(ContainSubstring("GH_TOKEN not set"))
	})

	It("returns done + ContinueToNext for a healthy PAT", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Authorization")).To(Equal("token good-token"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":4999}}}`))
		}))
		defer srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("good-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(got.ContinueToNext).To(BeTrue())
	})

	It("returns needs_input when token degrades to anonymous (60/hr)", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"resources":{"core":{"limit":60,"remaining":57}}}`))
		}))
		defer srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("revoked-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(got.Message).To(ContainSubstring("anonymous"))
		Expect(got.Message).To(ContainSubstring("rotate the PAT or the App PEM"))
	})

	It("returns needs_input on 401 unauthorized", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
		}))
		defer srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("bad-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(got.Message).To(ContainSubstring("HTTP 401"))
		Expect(got.Message).To(ContainSubstring("Bad credentials"))
	})

	It("returns failed (retryable) when remaining quota is nearly exhausted", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":3}}}`))
		}))
		defer srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("good-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(got.Message).To(ContainSubstring("nearly exhausted"))
		Expect(got.Message).To(ContainSubstring("3/5000"))
	})

	It("returns failed (retryable) on non-200/401 HTTP status", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`upstream down`))
		}))
		defer srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("good-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(got.Message).To(ContainSubstring("HTTP 502"))
	})

	It("returns failed (retryable) on network error", func() {
		// Closed server — connection refused.
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("good-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(got.Message).To(ContainSubstring("network"))
	})

	It("returns failed when response JSON is unparseable", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not json`))
		}))
		defer srv.Close()

		step := pkg.NewGHTokenCheckStepWithURLForTest("good-token", srv.URL)
		got, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(got.Message).To(ContainSubstring("unparseable"))
	})

	It("ShouldRun always returns true", func() {
		step := pkg.NewGHTokenCheckStep("anything")
		ok, err := step.ShouldRun(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	It("Name returns the step identifier", func() {
		step := pkg.NewGHTokenCheckStep("anything")
		Expect(step.Name()).To(Equal("verify-gh-token"))
	})
})
