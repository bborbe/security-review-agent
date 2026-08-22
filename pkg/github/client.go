// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
)

// PRBranches holds the source and target branch names of a pull request.
type PRBranches struct {
	Source string
	Target string
}

// Client interacts with GitHub via the gh CLI.
//
//counterfeiter:generate -o ../../mocks/github-client.go --fake-name GitHubClient . Client
type Client interface {
	GetPRBranches(ctx context.Context, owner, repo string, number int) (PRBranches, error)
	PostComment(ctx context.Context, owner, repo string, number int, body string) error
	SubmitReview(
		ctx context.Context,
		owner, repo string,
		number int,
		body string,
		verdict prpkg.Verdict,
	) error
}

// NewGHClient creates a Client that uses the gh CLI.
// If token is non-empty, it will be passed to gh CLI via GH_TOKEN env var.
func NewGHClient(token string) Client {
	return &ghClient{token: token}
}

type ghClient struct {
	token string
}

// GetPRBranches fetches the source and target branch names for a pull request.
func (c *ghClient) GetPRBranches(
	ctx context.Context,
	owner, repo string,
	number int,
) (PRBranches, error) {
	repoArg := fmt.Sprintf("%s/%s", owner, repo)
	numberArg := strconv.Itoa(number)

	// #nosec G204 -- args are validated by caller, owner/repo from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "view",
		numberArg,
		"--repo", repoArg,
		"--json", "headRefName,baseRefName",
		"--jq", ".headRefName + \"\\n\" + .baseRefName",
	)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return PRBranches{}, errors.Wrapf(
			ctx,
			err,
			"gh pr view failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 || lines[0] == "" || lines[1] == "" {
		return PRBranches{}, errors.Errorf(ctx, "gh pr view returned incomplete branch info")
	}

	return PRBranches{
		Source: lines[0],
		Target: lines[1],
	}, nil
}

// PostComment posts a comment on a pull request.
func (c *ghClient) PostComment(
	ctx context.Context,
	owner, repo string,
	number int,
	body string,
) error {
	repoArg := fmt.Sprintf("%s/%s", owner, repo)
	numberArg := strconv.Itoa(number)

	// #nosec G204 -- args are validated by caller, owner/repo from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "comment",
		numberArg,
		"--repo", repoArg,
		"--body", body,
	)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.Wrapf(
			ctx,
			err,
			"gh pr comment failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	return nil
}

// SubmitReview submits a structured review (approve or request-changes) on a pull request.
func (c *ghClient) SubmitReview(
	ctx context.Context,
	owner, repo string,
	number int,
	body string,
	v prpkg.Verdict,
) error {
	// Defensive: rejects out-of-enum verdict values (the binary set is approve/request-changes)
	if v != prpkg.VerdictApprove && v != prpkg.VerdictRequestChanges {
		return errors.Errorf(
			ctx,
			"unsupported verdict for SubmitReview: %s",
			v,
		)
	}

	repoArg := fmt.Sprintf("%s/%s", owner, repo)
	numberArg := strconv.Itoa(number)

	args := []string{"pr", "review", numberArg, "--repo", repoArg}

	if v == prpkg.VerdictApprove {
		args = append(args, "--approve")
	} else {
		args = append(args, "--request-changes")
	}

	args = append(args, "--body", body)

	// #nosec G204 -- args are validated by caller, owner/repo from URL parsing
	cmd := exec.CommandContext(ctx, "gh", args...)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.Wrapf(ctx, err, "gh pr review failed: %s", strings.TrimSpace(stderr.String()))
	}

	return nil
}
