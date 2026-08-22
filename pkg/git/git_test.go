// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bborbe/github-pr-review-agent/pkg/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WorktreeManager", func() {
	var (
		ctx     context.Context
		manager git.WorktreeManager
	)

	BeforeEach(func() {
		ctx = context.Background()
		manager = git.NewWorktreeManager()
	})

	Describe("Fetch", func() {
		Context("with non-existent path", func() {
			It("returns error", func() {
				err := manager.Fetch(ctx, "/nonexistent/path")
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("local path not found: /nonexistent/path"))
			})
		})

		Context("with non-git directory", func() {
			var tmpDir string

			BeforeEach(func() {
				tmpDir = GinkgoT().TempDir()
			})

			It("returns error", func() {
				err := manager.Fetch(ctx, tmpDir)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("not a git repo: " + tmpDir))
			})
		})

		Context("with valid git repository", func() {
			var repoPath string

			BeforeEach(func() {
				repoPath = initGitRepo()
			})

			It("succeeds", func() {
				err := manager.Fetch(ctx, repoPath)
				Expect(err).To(BeNil())
			})
		})
	})

	Describe("CreateClone", func() {
		var repoPath string

		BeforeEach(func() {
			repoPath = initGitRepoWithRemote()
			createRemoteBranch(repoPath, "feature-branch")
		})

		Context("with valid branch", func() {
			It("returns deterministic clone path under temp dir", func() {
				clonePath, err := manager.CreateClone(ctx, repoPath, "feature-branch", 123)
				Expect(err).To(BeNil())
				repoName := filepath.Base(repoPath)
				Expect(
					clonePath,
				).To(Equal(filepath.Join(os.TempDir(), "pr-reviewer-"+repoName+"-pr-123")))

				// Verify clone directory exists
				info, err := os.Stat(clonePath)
				Expect(err).To(BeNil())
				Expect(info.IsDir()).To(BeTrue())
			})
		})

		Context("with branch already checked out in main repo", func() {
			BeforeEach(func() {
				// Checkout the branch in the main working tree
				runCmd(repoPath, "git", "checkout", "feature-branch")
			})

			It("creates clone successfully regardless of main repo state", func() {
				clonePath, err := manager.CreateClone(ctx, repoPath, "feature-branch", 999)
				Expect(err).To(BeNil())
				repoName := filepath.Base(repoPath)
				Expect(
					clonePath,
				).To(Equal(filepath.Join(os.TempDir(), "pr-reviewer-"+repoName+"-pr-999")))

				// Verify clone directory exists
				info, err := os.Stat(clonePath)
				Expect(err).To(BeNil())
				Expect(info.IsDir()).To(BeTrue())

				// Verify clone has the correct content (same as branch)
				readmeContent, err := os.ReadFile(filepath.Join(clonePath, "feature.txt"))
				Expect(err).To(BeNil())
				Expect(string(readmeContent)).To(Equal("Feature content\n"))
			})
		})

		Context("with stale clone from previous run", func() {
			BeforeEach(func() {
				// Create initial clone
				_, err := manager.CreateClone(ctx, repoPath, "feature-branch", 456)
				Expect(err).To(BeNil())
			})

			It("removes stale clone and creates fresh one", func() {
				clonePath, err := manager.CreateClone(ctx, repoPath, "feature-branch", 456)
				Expect(err).To(BeNil())
				repoName := filepath.Base(repoPath)
				Expect(
					clonePath,
				).To(Equal(filepath.Join(os.TempDir(), "pr-reviewer-"+repoName+"-pr-456")))

				// Verify clone exists
				_, err = os.Stat(clonePath)
				Expect(err).To(BeNil())
			})
		})

		Context("with non-existent branch", func() {
			It("returns error", func() {
				_, err := manager.CreateClone(ctx, repoPath, "nonexistent-branch", 789)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("branch not found: nonexistent-branch"))
			})
		})

		Context("with non-existent repo path", func() {
			It("returns error", func() {
				_, err := manager.CreateClone(ctx, "/nonexistent/path", "feature-branch", 999)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("local path not found: /nonexistent/path"))
			})
		})
	})

	Describe("RemoveClone", func() {
		var repoPath string

		BeforeEach(func() {
			repoPath = initGitRepoWithRemote()
			createRemoteBranch(repoPath, "test-branch")
		})

		Context("with non-existent clone", func() {
			It("returns nil (idempotent)", func() {
				nonExistentPath := filepath.Join(os.TempDir(), "pr-reviewer-nonexistent")
				err := manager.RemoveClone(ctx, nonExistentPath)
				Expect(err).To(BeNil())
			})
		})

		Context("with existing clone", func() {
			var clonePath string

			BeforeEach(func() {
				var err error
				clonePath, err = manager.CreateClone(ctx, repoPath, "test-branch", 111)
				Expect(err).To(BeNil())
			})

			It("removes clone successfully", func() {
				// Verify clone exists
				_, err := os.Stat(clonePath)
				Expect(err).To(BeNil())

				// Remove clone
				err = manager.RemoveClone(ctx, clonePath)
				Expect(err).To(BeNil())

				// Verify clone is gone
				_, err = os.Stat(clonePath)
				Expect(os.IsNotExist(err)).To(BeTrue())
			})
		})
	})

	Describe("Full integration cycle", func() {
		var repoPath string

		BeforeEach(func() {
			repoPath = initGitRepoWithRemote()
			createRemoteBranch(repoPath, "pr-branch")
		})

		It("fetch → create → verify → remove → verify gone", func() {
			// Fetch (simulates updating branches)
			err := manager.Fetch(ctx, repoPath)
			Expect(err).To(BeNil())

			// Create clone
			clonePath, err := manager.CreateClone(ctx, repoPath, "pr-branch", 42)
			Expect(err).To(BeNil())
			repoName := filepath.Base(repoPath)
			Expect(
				clonePath,
			).To(Equal(filepath.Join(os.TempDir(), "pr-reviewer-"+repoName+"-pr-42")))

			// Verify clone exists and has standalone git directory
			gitDir := filepath.Join(clonePath, ".git")
			info, err := os.Stat(gitDir)
			Expect(err).To(BeNil())
			Expect(info.IsDir()).To(BeTrue()) // Should be a directory, not a file

			// Remove clone
			err = manager.RemoveClone(ctx, clonePath)
			Expect(err).To(BeNil())

			// Verify clone is gone
			_, err = os.Stat(clonePath)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})
})

// Helper functions for test setup

func initGitRepo() string {
	tmpDir := GinkgoT().TempDir()

	// Initialize git repo
	runCmd(tmpDir, "git", "init")
	runCmd(tmpDir, "git", "config", "user.email", "test@example.com")
	runCmd(tmpDir, "git", "config", "user.name", "Test User")

	// Create initial commit
	testFile := filepath.Join(tmpDir, "README.md")
	err := os.WriteFile(testFile, []byte("# Test Repo\n"), 0600)
	Expect(err).To(BeNil())

	runCmd(tmpDir, "git", "add", "README.md")
	runCmd(tmpDir, "git", "commit", "-m", "Initial commit")

	return tmpDir
}

func initGitRepoWithRemote() string {
	// Create a bare "remote" repository
	remoteDir := GinkgoT().TempDir()
	runCmd(remoteDir, "git", "init", "--bare")

	// Clone it to create a local repo with proper remote tracking
	localDir := GinkgoT().TempDir()
	runCmd(localDir, "git", "clone", remoteDir, ".")
	runCmd(localDir, "git", "config", "user.email", "test@example.com")
	runCmd(localDir, "git", "config", "user.name", "Test User")

	// Create initial commit
	testFile := filepath.Join(localDir, "README.md")
	err := os.WriteFile(testFile, []byte("# Test Repo\n"), 0600)
	Expect(err).To(BeNil())

	runCmd(localDir, "git", "add", "README.md")
	runCmd(localDir, "git", "commit", "-m", "Initial commit")
	runCmd(localDir, "git", "push", "origin", "master")

	return localDir
}

func createRemoteBranch(repoPath, branchName string) {
	// Create a branch with some content
	runCmd(repoPath, "git", "checkout", "-b", branchName)

	// Add a file specific to this branch
	branchFile := filepath.Join(repoPath, "feature.txt")
	err := os.WriteFile(branchFile, []byte("Feature content\n"), 0600)
	Expect(err).To(BeNil())

	runCmd(repoPath, "git", "add", "feature.txt")
	runCmd(repoPath, "git", "commit", "-m", "Add feature")

	// Push to remote
	runCmd(repoPath, "git", "push", "origin", branchName)

	// Go back to master
	runCmd(repoPath, "git", "checkout", "master")
}

func runCmd(dir string, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	err := cmd.Run()
	Expect(err).To(BeNil())
}
