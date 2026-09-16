// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	pkg "github.com/bborbe/security-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TaskTypeSecurityReview", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("validates against the agentlib runtime regex ^[a-z0-9-]+$", func() {
		// The literal must satisfy the CRD-side validator's runtime constraint
		// (github.com/bborbe/agent TaskType.Validate). A struct-shape/constant-value
		// assertion alone does NOT prove this — the .Validate(ctx) call is the
		// level-1 boundary test.
		Expect(pkg.TaskTypeSecurityReview.Validate(ctx)).To(Succeed())
	})

	It("serializes to the exact literal security-review", func() {
		Expect(string(pkg.TaskTypeSecurityReview)).To(Equal("security-review"))
	})
})

var _ = Describe("TaskTypeBuildFix", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("validates against the agentlib runtime regex ^[a-z0-9-]+$", func() {
		Expect(pkg.TaskTypeBuildFix.Validate(ctx)).To(Succeed())
	})

	It("serializes to the exact literal build-fix", func() {
		Expect(string(pkg.TaskTypeBuildFix)).To(Equal("build-fix"))
	})
})
