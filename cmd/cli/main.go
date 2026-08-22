// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/bitbucket"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/github"

	prurl "github.com/bborbe/maintainer/prurl"
)

func main() {
	// Parse flags
	verbose := flag.Bool("v", false, "enable verbose output")
	commentOnly := flag.Bool("comment-only", false, "skip verdict, post as plain comment")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: pr-reviewer [--version] [-v] [--comment-only] <pr-url>\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *versionFlag {
		fmt.Printf("pr-reviewer %s\n", prpkg.Version)
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer cancel()

	if err := run(ctx, *verbose, *commentOnly); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, verbose bool, commentOnly bool) error {
	// Parse args
	if flag.NArg() < 1 {
		return errors.Errorf(ctx, "usage: pr-reviewer [-v] [--comment-only] <pr-url>")
	}
	rawURL := flag.Arg(0)

	// Log version
	logVerbose(verbose, "pr-reviewer %s", prpkg.Version)

	// Parse PR URL
	logVerbose(verbose, "parsing URL: %s", rawURL)
	prInfo, err := prurl.ParsePRURL(ctx, rawURL)
	if err != nil {
		return err
	}

	// Load config
	configPath := "~/.config/maintainer/pr-reviewer.yaml"
	logVerbose(verbose, "loading config: %s", configPath)
	loader := prpkg.NewFileLoader(configPath)
	cfg, err := loader.Load(ctx)
	if err != nil {
		return err
	}

	// Find local repo information
	repoInfo, err := cfg.FindRepo(ctx, prInfo.RepoURL)
	if err != nil {
		return err
	}

	// Expand home directory in path
	repoPath := prpkg.ExpandHome(repoInfo.Path)
	logVerbose(verbose, "repo: %s", repoPath)

	// Route based on platform
	switch prInfo.Platform {
	case prurl.PlatformGitHub:
		return runGitHub(ctx, verbose, commentOnly, cfg, prInfo, repoPath, repoInfo)
	case prurl.PlatformBitbucket:
		return runBitbucket(ctx, verbose, commentOnly, cfg, prInfo, repoPath, repoInfo)
	default:
		return errors.Errorf(ctx, "unsupported platform: %s", prInfo.Platform)
	}
}

// runGitHub handles the GitHub PR review workflow.
func runGitHub(
	ctx context.Context,
	verbose bool,
	commentOnly bool,
	cfg *prpkg.Config,
	prInfo *prurl.PRInfo,
	repoPath string,
	repoInfo *prpkg.RepoInfo,
) error {
	// Resolve token and create client
	resolvedToken := cfg.ResolvedGitHubToken()
	logTokenStatus(
		verbose,
		"github token",
		cfg.GitHub.Token,
		resolvedToken,
		prpkg.DefaultGitHubToken,
	)
	ghClient := github.NewGHClient(resolvedToken)

	// Get PR branch names
	logAlways("fetching PR #%d metadata...", prInfo.Number)
	branches, err := ghClient.GetPRBranches(ctx, prInfo.Owner, prInfo.Repo, prInfo.Number)
	if err != nil {
		return errors.Wrap(ctx, err, "get PR branches failed")
	}
	logVerbose(verbose, "source branch: %s, target branch: %s", branches.Source, branches.Target)

	// Create clone and run review
	clonePath, cleanup, err := createCloneAndFetch(
		ctx,
		verbose,
		repoPath,
		branches.Source,
		prInfo.Number,
	)
	if err != nil {
		return err
	}
	defer cleanup()

	// Create reviewer
	reviewer := prpkg.NewDockerReviewer(cfg.ResolvedContainerImage())

	// Run review
	reviewCommand := buildReviewCommand(repoInfo.ReviewCommand, branches.Target)
	reviewText, result, err := runReview(
		ctx,
		reviewer,
		clonePath,
		reviewCommand,
		cfg.ResolvedModel(),
		prInfo,
	)
	if err != nil {
		return err
	}

	// Submit review or post comment
	return submitGitHubReview(
		ctx,
		commentOnly,
		cfg.AutoApprove,
		result,
		ghClient,
		prInfo,
		reviewText,
	)
}

// runBitbucket handles the Bitbucket Server PR review workflow.
func runBitbucket(
	ctx context.Context,
	verbose bool,
	commentOnly bool,
	cfg *prpkg.Config,
	prInfo *prurl.PRInfo,
	repoPath string,
	repoInfo *prpkg.RepoInfo,
) error {
	// Resolve token and create client
	resolvedToken := cfg.ResolvedBitbucketToken()
	logTokenStatus(
		verbose,
		"bitbucket token",
		cfg.Bitbucket.Token,
		resolvedToken,
		prpkg.DefaultBitbucketToken,
	)
	if resolvedToken == "" {
		return errors.Errorf(ctx, "BITBUCKET_TOKEN not set")
	}
	bbClient := bitbucket.NewClient(resolvedToken)

	// Get PR branch names
	logAlways("fetching PR #%d metadata...", prInfo.Number)
	branches, err := bbClient.GetPRBranches(
		ctx,
		prInfo.Host,
		prInfo.Project,
		prInfo.Repo,
		prInfo.Number,
	)
	if err != nil {
		return errors.Wrap(ctx, err, "get PR branches failed")
	}
	logVerbose(verbose, "source branch: %s, target branch: %s", branches.Source, branches.Target)

	// Create clone and run review
	clonePath, cleanup, err := createCloneAndFetch(
		ctx,
		verbose,
		repoPath,
		branches.Source,
		prInfo.Number,
	)
	if err != nil {
		return err
	}
	defer cleanup()

	// Create reviewer
	reviewer := prpkg.NewDockerReviewer(cfg.ResolvedContainerImage())

	// Run review
	reviewCommand := buildReviewCommand(repoInfo.ReviewCommand, branches.Target)
	reviewText, result, err := runReview(
		ctx,
		reviewer,
		clonePath,
		reviewCommand,
		cfg.ResolvedModel(),
		prInfo,
	)
	if err != nil {
		return err
	}

	// Post comment and submit verdict
	return submitBitbucketReview(
		ctx,
		commentOnly,
		cfg.AutoApprove,
		result,
		bbClient,
		prInfo,
		reviewText,
		cfg.Bitbucket.Username,
	)
}

// createCloneAndFetch creates a local clone and fetches latest changes.
// Returns clone path and cleanup function.
func createCloneAndFetch(
	ctx context.Context,
	verbose bool,
	repoPath, branch string,
	prNumber int,
) (string, func(), error) {
	worktreeManager := git.NewWorktreeManager()

	// Fetch latest changes
	logAlways("fetching latest changes...")
	if err := worktreeManager.Fetch(ctx, repoPath); err != nil {
		return "", nil, errors.Wrap(ctx, err, "fetch failed")
	}

	// Create clone
	logAlways("creating clone for branch %s...", branch)
	clonePath, err := worktreeManager.CreateClone(ctx, repoPath, branch, prNumber)
	if err != nil {
		return "", nil, errors.Wrap(ctx, err, "create clone failed")
	}
	logVerbose(verbose, "created clone: %s", clonePath)

	cleanup := func() {
		if err := worktreeManager.RemoveClone(ctx, clonePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cleanup failed: %v\n", err)
		}
	}

	return clonePath, cleanup, nil
}

// runReview executes the Claude review and returns the review text and verdict.
func runReview(
	ctx context.Context,
	reviewer prpkg.Reviewer,
	worktreePath, reviewCommand, model string,
	prInfo *prurl.PRInfo,
) (string, prpkg.Result, error) {
	// Run review
	logAlways(
		"reviewing PR #%d (%s/%s) (this may take a few minutes)...",
		prInfo.Number,
		prInfo.Owner,
		prInfo.Repo,
	)
	reviewText, err := reviewer.Review(ctx, worktreePath, reviewCommand, model)
	if err != nil {
		return "", prpkg.Result{}, errors.Wrap(ctx, err, "review failed")
	}

	// Always print review to stdout
	fmt.Println(reviewText)

	// Parse verdict
	result := prpkg.ParseVerdict(reviewText)
	logAlways("verdict: %s (%s)", result.Verdict, result.Reason)

	// Strip JSON verdict block before posting to PR
	cleanedText := prpkg.StripJSONVerdict(reviewText)

	return cleanedText, result, nil
}

// submitGitHubReview submits the review to GitHub using the appropriate method.
func submitGitHubReview(
	ctx context.Context,
	commentOnly bool,
	autoApprove bool,
	result prpkg.Result,
	ghClient github.Client,
	prInfo *prurl.PRInfo,
	reviewText string,
) error {
	// --comment-only flag overrides verdict
	if commentOnly {
		return postGitHubComment(ctx, ghClient, prInfo, reviewText)
	}

	switch result.Verdict {
	case prpkg.VerdictApprove:
		return handleGitHubApprove(
			ctx,
			autoApprove,
			result,
			ghClient,
			prInfo,
			reviewText,
		)
	case prpkg.VerdictRequestChanges:
		return submitGitHubStructuredReview(
			ctx,
			result,
			ghClient,
			prInfo,
			reviewText,
		)
	default:
		return errors.Errorf(ctx, "unsupported verdict %q", result.Verdict)
	}
}

// postGitHubComment posts a plain comment to a GitHub PR.
func postGitHubComment(
	ctx context.Context,
	ghClient github.Client,
	prInfo *prurl.PRInfo,
	reviewText string,
) error {
	logAlways("posting comment...")
	if err := ghClient.PostComment(
		ctx,
		prInfo.Owner,
		prInfo.Repo,
		prInfo.Number,
		reviewText,
	); err != nil {
		return errors.Wrap(ctx, err, "post comment failed")
	}
	logAlways("done")
	return nil
}

// handleGitHubApprove handles the approve verdict based on autoApprove setting.
func handleGitHubApprove(
	ctx context.Context,
	autoApprove bool,
	result prpkg.Result,
	ghClient github.Client,
	prInfo *prurl.PRInfo,
	reviewText string,
) error {
	if !autoApprove {
		logAlways("skipping auto-approve (disabled in config)")
		return postGitHubComment(ctx, ghClient, prInfo, reviewText)
	}
	return submitGitHubStructuredReview(ctx, result, ghClient, prInfo, reviewText)
}

// submitGitHubStructuredReview submits a structured review (approve/request-changes).
func submitGitHubStructuredReview(
	ctx context.Context,
	result prpkg.Result,
	ghClient github.Client,
	prInfo *prurl.PRInfo,
	reviewText string,
) error {
	logAlways("submitting review: %s...", result.Verdict)
	if err := ghClient.SubmitReview(
		ctx,
		prInfo.Owner,
		prInfo.Repo,
		prInfo.Number,
		reviewText,
		result.Verdict,
	); err != nil {
		return errors.Wrap(ctx, err, "submit review failed")
	}
	logAlways("done")
	return nil
}

// submitBitbucketReview submits the review to Bitbucket with comment and verdict.
func submitBitbucketReview(
	ctx context.Context,
	commentOnly bool,
	autoApprove bool,
	result prpkg.Result,
	bbClient bitbucket.Client,
	prInfo *prurl.PRInfo,
	reviewText string,
	username string,
) error {
	// Always post comment first
	logAlways("posting comment...")
	if err := bbClient.PostComment(
		ctx,
		prInfo.Host,
		prInfo.Project,
		prInfo.Repo,
		prInfo.Number,
		reviewText,
	); err != nil {
		return errors.Wrap(ctx, err, "post comment failed")
	}

	// --comment-only flag skips verdict submission
	if commentOnly {
		logAlways("done")
		return nil
	}

	// Submit verdict for approve/request-changes
	if result.Verdict == prpkg.VerdictApprove {
		if !autoApprove {
			logAlways("skipping auto-approve (disabled in config)")
			logAlways("done")
			return nil
		}
		logAlways("approving PR...")
		if err := bbClient.Approve(
			ctx,
			prInfo.Host,
			prInfo.Project,
			prInfo.Repo,
			prInfo.Number,
		); err != nil {
			return errors.Wrap(ctx, err, "approve failed")
		}
		logAlways("done")
		return nil
	}

	if result.Verdict == prpkg.VerdictRequestChanges {
		if username == "" {
			logAlways("skipping needs-work verdict (bitbucket.username not configured)")
			logAlways("done")
			return nil
		}
		logAlways("marking PR as needs-work...")
		if err := bbClient.NeedsWork(
			ctx,
			prInfo.Host,
			prInfo.Project,
			prInfo.Repo,
			prInfo.Number,
			username,
		); err != nil {
			return errors.Wrap(ctx, err, "needs-work failed")
		}
		logAlways("done")
		return nil
	}

	logAlways("done")
	return nil
}

// buildReviewCommand constructs the review command with the target branch.
// If reviewCommand is set (custom override), use it as-is.
// Otherwise, construct "/pr-review <targetBranch>".
func buildReviewCommand(reviewCommand, targetBranch string) string {
	if reviewCommand != "" {
		return reviewCommand
	}
	return fmt.Sprintf("/pr-review %s", targetBranch)
}

// logTokenStatus logs the token source and whether it resolved to a value.
func logTokenStatus(verbose bool, label, configToken, resolvedToken, defaultToken string) {
	source := configToken
	if source == "" {
		source = defaultToken
	}
	if resolvedToken == "" {
		logVerbose(verbose, "%s: %s (not set, using default auth)", label, source)
	} else {
		logVerbose(verbose, "%s: %s (set, %d chars)", label, source, len(resolvedToken))
	}
}

// logAlways logs a message to stderr in both normal and verbose mode.
func logAlways(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// logVerbose logs a message to stderr only in verbose mode.
func logVerbose(verbose bool, format string, args ...interface{}) {
	if verbose {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}
