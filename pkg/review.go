// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bborbe/errors"
)

// Reviewer runs a code review using the claude CLI.
//
//counterfeiter:generate -o ../mocks/reviewer.go --fake-name Reviewer . Reviewer
type Reviewer interface {
	Review(ctx context.Context, worktreePath string, command string, model string) (string, error)
}

// NewDockerReviewer creates a Reviewer that invokes claude inside a Docker container.
func NewDockerReviewer(containerImage string) Reviewer {
	return &dockerReviewer{containerImage: containerImage}
}

type dockerReviewer struct {
	containerImage string
}

// Review runs claude inside a Docker container using the claude-yolo image.
// The container mounts the worktree, Claude config, and Go module cache.
// Uses YOLO_PROMPT_FILE, YOLO_MODEL, and YOLO_OUTPUT env vars
// (same pattern as dark-factory).
func (r *dockerReviewer) Review(
	ctx context.Context,
	worktreePath string,
	command string,
	model string,
) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrapf(ctx, err, "get home directory")
	}

	// Write prompt to temp file (same pattern as dark-factory)
	promptFile, err := os.CreateTemp("", "pr-reviewer-prompt-*.md")
	if err != nil {
		return "", errors.Wrapf(ctx, err, "create prompt file")
	}
	defer func() { _ = os.Remove(promptFile.Name()) }()

	if _, err := promptFile.WriteString(command); err != nil {
		_ = promptFile.Close()
		return "", errors.Wrapf(ctx, err, "write prompt file")
	}
	if err := promptFile.Close(); err != nil {
		return "", errors.Wrapf(ctx, err, "close prompt file")
	}

	// Build docker command using default entrypoint with env vars.
	// YOLO_OUTPUT=print makes entrypoint use claude --print for raw text output.
	// #nosec G204 -- paths from context, containerImage from config, model validated by claude CLI
	cmd := exec.CommandContext(
		ctx,
		"docker",
		"run",
		"--rm",
		"--cap-add=NET_ADMIN",
		"--cap-add=NET_RAW",
		"-e", "YOLO_PROMPT_FILE=/tmp/prompt.md",
		"-e", "YOLO_MODEL="+model,
		"-e", "YOLO_OUTPUT=print",
		"-v", promptFile.Name()+":/tmp/prompt.md:ro",
		"-v", fmt.Sprintf("%s:/workspace", worktreePath),
		"-v", fmt.Sprintf("%s/.claude-yolo:/home/node/.claude", home),
		"-v", fmt.Sprintf("%s/go/pkg:/home/node/go/pkg", home),
		r.containerImage,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", errors.Wrapf(
			ctx,
			err,
			"claude review failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	return strings.TrimSpace(stdout.String()), nil
}
