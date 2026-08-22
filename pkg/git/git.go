// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
)

// WorktreeManager manages git clones for isolated PR review.
//
//counterfeiter:generate -o ../../mocks/worktree-manager.go --fake-name WorktreeManager . WorktreeManager
type WorktreeManager interface {
	Fetch(ctx context.Context, repoPath string) error
	CreateClone(
		ctx context.Context,
		repoPath string,
		branch string,
		prNumber int,
	) (clonePath string, err error)
	RemoveClone(ctx context.Context, clonePath string) error
}

// NewWorktreeManager creates a WorktreeManager that uses git CLI.
func NewWorktreeManager() WorktreeManager {
	return &worktreeManager{}
}

type worktreeManager struct{}

// Fetch runs git fetch --all --prune in the repository.
func (m *worktreeManager) Fetch(ctx context.Context, repoPath string) error {
	if err := m.validateRepoPath(ctx, repoPath); err != nil {
		return errors.Wrap(ctx, err, "validate repo path failed")
	}

	if err := m.runGit(ctx, repoPath, "fetch", "--all", "--prune"); err != nil {
		return errors.Wrap(ctx, err, "git fetch failed")
	}

	return nil
}

// CreateClone creates a local clone for the given branch.
// Clone path is deterministic: /tmp/pr-reviewer-<repoName>-pr-<number>
// If a stale clone exists, it is removed first.
func (m *worktreeManager) CreateClone(
	ctx context.Context,
	repoPath string,
	branch string,
	prNumber int,
) (string, error) {
	if !isValidBranchName(branch) {
		return "", errors.Errorf(ctx, "invalid branch name: %s", branch)
	}

	if err := m.validateRepoPath(ctx, repoPath); err != nil {
		return "", errors.Wrap(ctx, err, "validate repo path failed")
	}

	clonePath := m.clonePath(repoPath, prNumber)

	// Remove stale clone if it exists
	if _, err := os.Stat(clonePath); err == nil {
		if err := m.RemoveClone(ctx, clonePath); err != nil {
			return "", errors.Wrap(ctx, err, "remove stale clone failed")
		}
	}

	// Clone repository locally (uses hardlinks for fast cloning)
	// #nosec G204 -- repoPath is validated by validateRepoPath, clonePath is constructed safely
	cmd := exec.CommandContext(ctx, "git", "clone", "--local", "--no-checkout", repoPath, clonePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", errors.Errorf(ctx, "git clone: %s", strings.TrimSpace(stderr.String()))
	}

	// Checkout the branch (detached HEAD)
	remoteBranch := "origin/" + branch
	// #nosec G204 -- clonePath is constructed safely, branch is validated by isValidBranchName
	checkoutCmd := exec.CommandContext(ctx, "git", "-C", clonePath, "checkout", remoteBranch)
	checkoutCmd.Stderr = &stderr

	if err := checkoutCmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "invalid reference") ||
			strings.Contains(stderr.String(), "pathspec") {
			return "", errors.Errorf(ctx, "branch not found: %s", branch)
		}
		return "", errors.Errorf(ctx, "git checkout: %s", strings.TrimSpace(stderr.String()))
	}

	return clonePath, nil
}

// RemoveClone removes a git clone directory.
// Idempotent: returns nil if clone doesn't exist.
func (m *worktreeManager) RemoveClone(
	ctx context.Context,
	clonePath string,
) error {
	// Check if clone exists
	if _, err := os.Stat(clonePath); os.IsNotExist(err) {
		return nil // idempotent
	}

	if err := os.RemoveAll(clonePath); err != nil {
		return errors.Wrap(ctx, err, "remove clone directory failed")
	}

	return nil
}

// validateRepoPath checks if the path exists and is a git repository.
func (m *worktreeManager) validateRepoPath(ctx context.Context, repoPath string) error {
	// Check path exists
	info, err := os.Stat(repoPath)
	if os.IsNotExist(err) {
		return errors.Errorf(ctx, "local path not found: %s", repoPath)
	}
	if err != nil {
		return errors.Wrap(ctx, err, "stat path failed")
	}

	if !info.IsDir() {
		return errors.Errorf(ctx, "not a directory: %s", repoPath)
	}

	// Check if it's a git repo
	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return errors.Errorf(ctx, "not a git repo: %s", repoPath)
	}

	return nil
}

// clonePath generates the deterministic clone path under os.TempDir().
// Uses repo basename to avoid collisions between different repos.
func (m *worktreeManager) clonePath(repoPath string, prNumber int) string {
	repoName := filepath.Base(repoPath)
	return filepath.Join(os.TempDir(), fmt.Sprintf("pr-reviewer-%s-pr-%d", repoName, prNumber))
}

// runGit executes a git command and returns an error with stderr.
func (m *worktreeManager) runGit(
	ctx context.Context,
	repoPath string,
	args ...string,
) error {
	// #nosec G204 -- repoPath is validated by validateRepoPath, args are hardcoded git commands
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.Errorf(ctx, "git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}

	return nil
}

var branchNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9._/@\-]+$`)

// isValidBranchName returns true if b is a safe, well-formed git branch name.
// Rejects empty strings, names starting with "-", and names containing "..".
func isValidBranchName(b string) bool {
	if b == "" {
		return false
	}
	if strings.HasPrefix(b, "-") {
		return false
	}
	if strings.Contains(b, "..") {
		return false
	}
	return branchNameRegexp.MatchString(b)
}
