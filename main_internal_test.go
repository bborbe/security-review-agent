// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"time"

	libargument "github.com/bborbe/argument/v2"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the REVIEW_MAX_DURATION argument/v2
// resolution on the real application struct — the struct-tag → typed-field
// boundary. They run under the Ginkgo suite bootstrapped in main_test.go
// (same pattern as auth_resolve_test.go): Ginkgo v2.32.1 rejects a second
// RunSpecs call in one binary (RerunningSuite → os.Exit(1)), so no suite is
// registered here.
var _ = Describe("review max duration resolution", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("resolves to 1500s (25m) when unset", func() {
		defaults, err := libargument.DefaultValues(ctx, &application{})
		Expect(err).NotTo(HaveOccurred())
		app := &application{}
		err = libargument.Fill(ctx, app, defaults)
		Expect(err).NotTo(HaveOccurred())
		Expect(app.MaxReviewDuration.Duration()).To(Equal(1500 * time.Second))
	})

	It("equals the parsed value when set", func() {
		app := &application{}
		err := libargument.ParseEnv(ctx, app, []string{"REVIEW_MAX_DURATION=30m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(app.MaxReviewDuration.Duration()).To(Equal(1800 * time.Second))
	})

	It("fails startup on an unparseable value", func() {
		app := &application{}
		err := libargument.ParseEnv(ctx, app, []string{"REVIEW_MAX_DURATION=abc"})
		Expect(err).To(HaveOccurred())
	})

	It("parses a below-floor value but fails validation at startup", func() {
		app := &application{}
		err := libargument.ParseEnv(ctx, app, []string{"REVIEW_MAX_DURATION=5s"})
		Expect(err).NotTo(HaveOccurred())
		Expect(app.MaxReviewDuration.Duration()).To(Equal(5 * time.Second))
		err = prpkg.ValidateReviewMaxDuration(ctx, app.MaxReviewDuration)
		Expect(err).To(HaveOccurred())
	})
})
