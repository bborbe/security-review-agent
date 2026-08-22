// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"time"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateReviewMaxDuration", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("budget validation",
		func(d libtime.Duration, expectErr bool) {
			err := pkg.ValidateReviewMaxDuration(ctx, d)
			if expectErr {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("REVIEW_MAX_DURATION"))
				Expect(
					err.Error(),
				).To(ContainSubstring("1m"))
				// the 60s floor, formatted by libtime.Duration.String
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("25m is above the floor", libtime.Duration(25*time.Minute), false),
		Entry("10m is above the floor", libtime.Duration(10*time.Minute), false),
		Entry("60s equals the floor", libtime.Duration(60*time.Second), false),
		Entry("59s is below the floor", libtime.Duration(59*time.Second), true),
		Entry("0 is below the floor", libtime.Duration(0), true),
		Entry("-1m is below the floor", libtime.Duration(-1*time.Minute), true),
	)
})
