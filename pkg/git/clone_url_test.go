// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	"context"

	"github.com/bborbe/github-pr-review-agent/pkg/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseCloneURL", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable(
		"valid URLs",
		func(rawURL, expected string) {
			result, err := git.ParseCloneURL(ctx, rawURL)
			Expect(err).To(BeNil())
			Expect(result).To(Equal(expected))
		},
		Entry(
			"URL with .git suffix",
			"https://github.com/bborbe/maintainer.git",
			"github.com/bborbe/maintainer.git",
		),
		Entry(
			"URL without .git suffix",
			"https://github.com/bborbe/maintainer",
			"github.com/bborbe/maintainer.git",
		),
		Entry(
			"SCP-style SSH URL",
			"git@github.com:bborbe/maintainer.git",
			"github.com/bborbe/maintainer.git",
		),
		Entry(
			"SCP-style SSH URL without .git suffix",
			"git@github.com:bborbe/maintainer",
			"github.com/bborbe/maintainer.git",
		),
		Entry(
			"SCP-style SSH URL on non-GitHub host",
			"git@bitbucket.example.com:team/repo.git",
			"bitbucket.example.com/team/repo.git",
		),
	)

	DescribeTable("invalid URLs",
		func(rawURL string) {
			_, err := git.ParseCloneURL(ctx, rawURL)
			Expect(err).NotTo(BeNil())
		},
		Entry("empty string", ""),
		Entry("no host", "/owner/repo.git"),
		Entry("path traversal in owner (2-segment)", "https://github.com/../repo.git"),
		Entry("path traversal in owner (3-segment)", "https://github.com/../foo/bar.git"),
		Entry("dot in owner", "https://github.com/./repo.git"),
		Entry("empty segment from double slash", "https://github.com//repo.git"),
		Entry("only one path segment", "https://github.com/owner"),
		Entry("three path segments", "https://github.com/owner/repo/extra"),
		Entry("shell metacharacter in repo", "https://github.com/owner/repo;rm -rf /"),
		Entry("SCP-style with path traversal", "git@github.com:../repo.git"),
		Entry("SCP-style with shell metacharacter", "git@github.com:owner/repo;rm -rf /"),
		Entry("SCP-style with only one path segment", "git@github.com:owner"),
	)
})

var _ = Describe("ParseCloneURLParts", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable(
		"valid URLs return correct parts",
		func(rawURL string, expected git.CloneURLParts) {
			result, err := git.ParseCloneURLParts(ctx, rawURL)
			Expect(err).To(BeNil())
			Expect(result).NotTo(BeNil())
			Expect(*result).To(Equal(expected))
		},
		Entry(
			"HTTPS URL with .git suffix",
			"https://github.com/bborbe/maintainer.git",
			git.CloneURLParts{Host: "github.com", Owner: "bborbe", Repo: "maintainer"},
		),
		Entry(
			"SCP SSH URL with .git suffix",
			"git@github.com:bborbe/maintainer.git",
			git.CloneURLParts{Host: "github.com", Owner: "bborbe", Repo: "maintainer"},
		),
	)

	DescribeTable(
		"invalid URLs return error",
		func(rawURL string) {
			result, err := git.ParseCloneURLParts(ctx, rawURL)
			Expect(err).NotTo(BeNil())
			Expect(result).To(BeNil())
		},
		Entry("empty string", ""),
		Entry("single-segment path", "https://github.com/owner"),
		Entry("invalid segment characters", "https://github.com/owner/repo;rm -rf /"),
	)
})
