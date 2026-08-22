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

var _ = Describe("isValidBranchName", func() {
	DescribeTable("branch name validation",
		func(branch string, expected bool) {
			// isValidBranchName is unexported; exercise it via CreateClone with a fake path.
			// A branch that fails validation returns an error before any git subprocess is invoked.
			ctx := context.Background()
			manager := git.NewWorktreeManager()
			_, err := manager.CreateClone(ctx, "/nonexistent/path", branch, 1)
			if expected {
				// Valid branch names should not fail on branch validation; error will be about repo path
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).NotTo(ContainSubstring("invalid branch name"))
			} else {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("invalid branch name"))
			}
		},
		Entry("empty string is invalid", "", false),
		Entry("starts with dash is invalid", "-bad", false),
		Entry("double-dot traversal is invalid", "branch/../../../etc/passwd", false),
		Entry("upload-pack injection is invalid", "--upload-pack=cmd", false),
		Entry("simple branch name is valid", "main", true),
		Entry("feature branch with slash is valid", "feature/my-branch", true),
		Entry("branch with underscore is valid", "my_feature", true),
		Entry("branch with dot is valid", "release-1.2", true),
	)
})

var _ = Describe("CreateClone branch validation guard", func() {
	It("returns error before invoking git when branch starts with dash", func() {
		ctx := context.Background()
		manager := git.NewWorktreeManager()
		_, err := manager.CreateClone(ctx, "/tmp", "-dangerous-flag", 1)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("invalid branch name"))
	})
})
