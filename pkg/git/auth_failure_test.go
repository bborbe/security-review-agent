// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	"errors"

	"github.com/bborbe/github-pr-review-agent/pkg/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsGitAuthFailure", func() {
	DescribeTable(
		"returns true for known auth-failure substrings",
		func(msg string) {
			Expect(git.IsGitAuthFailure(errors.New(msg))).To(BeTrue())
		},
		Entry(
			"no username prompt disabled",
			"git clone --bare: fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		),
		Entry(
			"authentication failed",
			"git clone --bare: remote: Authentication failed for 'https://github.com/bborbe/trading.git/'",
		),
		Entry("repository not found", "git clone --bare: remote: Repository not found."),
		Entry("403 error", "git clone --bare: The requested URL returned error: 403"),
		Entry("401 error", "git clone --bare: The requested URL returned error: 401"),
	)

	DescribeTable(
		"returns false for non-auth errors",
		func(msg string) {
			Expect(git.IsGitAuthFailure(errors.New(msg))).To(BeFalse())
		},
		Entry(
			"DNS failure",
			"git clone --bare: unable to access 'https://github.com/bborbe/foo.git/': Could not resolve host: github.com",
		),
		Entry("connection refused", "git clone --bare: fatal: unable to connect to github.com"),
		Entry(
			"ref not found",
			"git clone --bare: error: pathspec 'no-such-ref' did not match any file",
		),
		Entry("generic error", "some other error"),
	)

	It("returns false for nil error", func() {
		Expect(git.IsGitAuthFailure(nil)).To(BeFalse())
	})
})
