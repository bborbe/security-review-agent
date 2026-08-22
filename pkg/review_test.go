// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DockerReviewer", func() {
	var (
		ctx      context.Context
		reviewer pkg.Reviewer
	)

	BeforeEach(func() {
		ctx = context.Background()
		reviewer = pkg.NewDockerReviewer("test-image:latest")
	})

	Describe("Review", func() {
		Context("integration test with mock docker script", func() {
			var (
				tempDir      string
				mockScript   string
				originalPATH string
			)

			BeforeEach(func() {
				tempDir = GinkgoT().TempDir()
				binDir := filepath.Join(tempDir, "bin")
				err := os.Mkdir(binDir, 0750)
				Expect(err).To(BeNil())

				mockScript = filepath.Join(binDir, "docker")

				// Save original PATH
				originalPATH = os.Getenv("PATH")
			})

			AfterEach(func() {
				// Restore original PATH
				err := os.Setenv("PATH", originalPATH)
				Expect(err).To(BeNil())
			})

			Context("with successful docker execution", func() {
				BeforeEach(func() {
					// Create mock docker script that prints review text
					scriptContent := `#!/bin/sh
echo "Docker review output"
exit 0
`
					// #nosec G306 -- test file: mock executable script needs execute permissions
					err := os.WriteFile(mockScript, []byte(scriptContent), 0750)
					Expect(err).To(BeNil())

					// Prepend bin dir to PATH
					err = os.Setenv(
						"PATH",
						filepath.Join(tempDir, "bin")+string(os.PathListSeparator)+originalPATH,
					)
					Expect(err).To(BeNil())
				})

				It("returns review text from stdout", func() {
					worktreeDir := GinkgoT().TempDir()

					result, err := reviewer.Review(ctx, worktreeDir, "/code-review", "sonnet")
					Expect(err).To(BeNil())
					Expect(result).To(Equal("Docker review output"))
				})
			})

			Context("with docker returning non-zero exit code", func() {
				BeforeEach(func() {
					// Create mock docker script that fails
					scriptContent := `#!/bin/sh
echo "Error: container failed" >&2
exit 1
`
					// #nosec G306 -- test file: mock executable script needs execute permissions
					err := os.WriteFile(mockScript, []byte(scriptContent), 0750)
					Expect(err).To(BeNil())

					// Prepend bin dir to PATH
					err = os.Setenv(
						"PATH",
						filepath.Join(tempDir, "bin")+string(os.PathListSeparator)+originalPATH,
					)
					Expect(err).To(BeNil())
				})

				It("returns error with stderr content", func() {
					worktreeDir := GinkgoT().TempDir()

					_, err := reviewer.Review(ctx, worktreeDir, "/code-review", "sonnet")
					Expect(err).NotTo(BeNil())
					Expect(err.Error()).To(ContainSubstring("claude review failed"))
					Expect(err.Error()).To(ContainSubstring("Error: container failed"))
				})
			})

			Context("with custom model parameter", func() {
				BeforeEach(func() {
					// Create mock docker script that verifies YOLO_MODEL env var is passed
					scriptContent := `#!/bin/sh
# Check that YOLO_MODEL env var is passed via -e flag
for arg in "$@"; do
  if [ "$arg" = "YOLO_MODEL=opus" ]; then
    echo "Model parameter found"
    exit 0
  fi
done
echo "Model parameter not found: $@" >&2
exit 1
`
					// #nosec G306 -- test file: mock executable script needs execute permissions
					err := os.WriteFile(mockScript, []byte(scriptContent), 0750)
					Expect(err).To(BeNil())

					// Prepend bin dir to PATH
					err = os.Setenv(
						"PATH",
						filepath.Join(tempDir, "bin")+string(os.PathListSeparator)+originalPATH,
					)
					Expect(err).To(BeNil())
				})

				It("passes model parameter correctly", func() {
					worktreeDir := GinkgoT().TempDir()

					result, err := reviewer.Review(ctx, worktreeDir, "/code-review", "opus")
					Expect(err).To(BeNil())
					Expect(result).To(Equal("Model parameter found"))
				})
			})

			Context("with volume mounts", func() {
				BeforeEach(func() {
					// Create mock docker script that verifies volume mounts
					scriptContent := `#!/bin/sh
# Check for -v flags (volume mounts)
for arg in "$@"; do
  case "$arg" in
    -v)
      echo "Volume mount flag found"
      exit 0
      ;;
  esac
done
echo "No volume mounts found" >&2
exit 1
`
					// #nosec G306 -- test file: mock executable script needs execute permissions
					err := os.WriteFile(mockScript, []byte(scriptContent), 0750)
					Expect(err).To(BeNil())

					// Prepend bin dir to PATH
					err = os.Setenv(
						"PATH",
						filepath.Join(tempDir, "bin")+string(os.PathListSeparator)+originalPATH,
					)
					Expect(err).To(BeNil())
				})

				It("includes volume mounts", func() {
					worktreeDir := GinkgoT().TempDir()

					result, err := reviewer.Review(ctx, worktreeDir, "/code-review", "sonnet")
					Expect(err).To(BeNil())
					Expect(result).To(Equal("Volume mount flag found"))
				})
			})
		})
	})
})
