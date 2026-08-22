// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/github-pr-review-agent/pkg/githubposter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ReadAutoApprove", func() {
	var ctx context.Context
	var tmpDir string

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "pr-reviewer-config-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, tmpDir)
	})

	DescribeTable(
		"maintainer.yaml variants",
		func(content string, writeFile bool, expected bool, expectErr bool, errContains string) {
			if writeFile {
				err := os.WriteFile(
					filepath.Join(tmpDir, ".maintainer.yaml"),
					[]byte(content),
					0600,
				)
				Expect(err).NotTo(HaveOccurred())
			}
			autoApprove, err := githubposter.ReadAutoApprove(ctx, tmpDir)
			if expectErr {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errContains))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(autoApprove).To(Equal(expected))
			}
		},
		Entry("file missing -> false, no error",
			"", false, false, false, ""),
		Entry("prReviewer.autoApprove: true -> true",
			"prReviewer:\n  autoApprove: true\n", true, true, false, ""),
		Entry("prReviewer.autoApprove: false -> false",
			"prReviewer:\n  autoApprove: false\n", true, false, false, ""),
		Entry("prReviewer key absent -> false",
			"release:\n  autoRelease: true\n", true, false, false, ""),
		Entry("only release populated (no prReviewer) -> false",
			"release:\n  autoRelease: true\n", true, false, false, ""),
		Entry("malformed YAML -> wrapped error",
			"prReviewer:\n  autoApprove: [unclosed\n", true, false, true, "parse .maintainer.yaml"),
	)
})
