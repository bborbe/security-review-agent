// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseRepoAllowlist", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns nil for empty string", func() {
		result, err := pkg.ParseRepoAllowlist(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("parses a single valid entry", func() {
		result, err := pkg.ParseRepoAllowlist(ctx, "github.com/bborbe/maintainer")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal([]string{"github.com/bborbe/maintainer"}))
	})

	It("parses multiple valid entries", func() {
		result, err := pkg.ParseRepoAllowlist(
			ctx,
			"github.com/bborbe/maintainer,github.com/bborbe/agent",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(
			result,
		).To(Equal([]string{"github.com/bborbe/maintainer", "github.com/bborbe/agent"}))
	})

	It("strips whitespace around entries", func() {
		result, err := pkg.ParseRepoAllowlist(
			ctx,
			" github.com/bborbe/maintainer , github.com/bborbe/agent ",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(
			result,
		).To(Equal([]string{"github.com/bborbe/maintainer", "github.com/bborbe/agent"}))
	})

	It("silently drops trailing comma (empty entry)", func() {
		result, err := pkg.ParseRepoAllowlist(ctx, "github.com/bborbe/maintainer,")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal([]string{"github.com/bborbe/maintainer"}))
	})

	It("accepts wildcard entry without error", func() {
		result, err := pkg.ParseRepoAllowlist(ctx, "github.com/bborbe/*")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal([]string{"github.com/bborbe/*"}))
	})

	It("accepts malformed two-segment entry without error (library handles at match time)", func() {
		result, err := pkg.ParseRepoAllowlist(ctx, "bborbe/maintainer")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal([]string{"bborbe/maintainer"}))
	})
})
