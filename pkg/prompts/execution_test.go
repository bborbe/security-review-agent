// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/pkg/prompts"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const sampleFindings = `{"stats":{"yamls_run":66,"findings_count":1,"elapsed_ms":10},` +
	`"findings_by_owner":{"go-error-assistant":[{"rule_id":"go-errors/no-fmt-errorf"}]},"errors":[]}`

var _ = Describe("BuildExecutionInstructions", func() {
	var (
		ctx        context.Context
		tmpDir     string
		cmdDir     string
		fakePlugin string
	)

	fakePlugin = "---\ndescription: Test plugin\nallowed-tools: Task\n---\n# PR Review\n\nProcedure body line 1.\nProcedure body line 2.\n"

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "prompts-test-*")
		Expect(err).NotTo(HaveOccurred())

		cmdDir = filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
		Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	writePlugin := func(content string) {
		Expect(
			os.WriteFile(filepath.Join(cmdDir, "pr-review.md"), []byte(content), 0600),
		).To(Succeed())
	}

	Describe("happy path", func() {
		It("returns two instructions with correct content", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
				true,
				sampleFindings,
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(instructions).To(HaveLen(2))
			Expect(instructions[0].Name).To(Equal("workflow"))
			Expect(instructions[1].Name).To(Equal("output-format"))

			workflow := instructions[0].Content
			Expect(workflow).To(ContainSubstring("TARGET_BRANCH**: main"))
			Expect(workflow).To(ContainSubstring("mode**: standard"))
			Expect(workflow).To(ContainSubstring("Procedure body line 1."))
			Expect(workflow).NotTo(ContainSubstring("description: Test plugin"))
			Expect(workflow).To(ContainSubstring("Final step — emit verdict JSON"))
			Expect(workflow).To(ContainSubstring("Severity map"))
			Expect(workflow).To(ContainSubstring("Verdict roll-up"))
		})
	})

	Describe("worktree cwd guidance", func() {
		It(
			"tells the model its cwd is the worktree and to use plain git, never `git -C <path>`",
			func() {
				writePlugin(fakePlugin)

				instructions, err := prompts.BuildExecutionInstructions(
					ctx,
					claudelib.ClaudeConfigDir(tmpDir),
					"standard",
					"main",
					true,
					sampleFindings,
					"",
					libtime.Duration(25*time.Minute),
				)
				Expect(err).NotTo(HaveOccurred())

				workflow := instructions[0].Content
				Expect(workflow).To(ContainSubstring("Working directory"))
				Expect(workflow).To(ContainSubstring("never `git -C <path>`"))
			},
		)
	})

	Describe("funnel injected (agent ran the funnel)", func() {
		It("injects the findings JSON and forbids re-running the runner", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"selector",
				"main",
				true,
				sampleFindings,
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			pluginRoot := filepath.Join(tmpDir, "plugins", "marketplaces", "coding")
			// Authoritative funnel JSON is embedded for the model to consume.
			Expect(workflow).To(ContainSubstring("ALREADY RUN"))
			Expect(workflow).To(ContainSubstring("go-errors/no-fmt-errorf"))
			// The funnel is over-inclusive by design: the model must verify each
			// finding against the actual file before reporting it, and must NOT be
			// told to treat findings as pre-confirmed (regression guard for the
			// github-pr-reviewer false-positive incident, 2026-08-19).
			Expect(workflow).To(ContainSubstring("OVER-INCLUSIVE"))
			Expect(workflow).To(ContainSubstring("VERIFY EACH FINDING"))
			Expect(workflow).NotTo(ContainSubstring("confirmed MUST-tier"))
			Expect(workflow).NotTo(ContainSubstring("do NOT re-derive, re-run, or second-guess"))
			// Guide read by literal path (Read tool), probe skipped.
			Expect(workflow).To(ContainSubstring(pluginRoot + "/docs/selector-mode-guide.md"))
			// The model must NOT be told to run the runner or redirect to a temp file
			// (the old prescribed form the allowlist could never match).
			Expect(workflow).NotTo(ContainSubstring("> /tmp/pr-review-findings.json"))
			Expect(workflow).NotTo(ContainSubstring("run exactly"))
			// Steer lands before the inlined procedure body.
			Expect(strings.Index(workflow, "Pre-computed mechanical funnel")).
				To(BeNumerically("<", strings.Index(workflow, "Procedure body line 1.")))
		})
	})

	Describe("funnel failed (agent could not run the funnel)", func() {
		It("injects a fail-closed status and forbids a silent approve", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"selector",
				"main",
				false,
				"",
				"ast-grep runner script not found",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			Expect(workflow).To(ContainSubstring("COULD NOT RUN"))
			Expect(workflow).To(ContainSubstring("ast-grep runner script not found"))
			Expect(workflow).To(ContainSubstring("request-changes"))
			Expect(workflow).To(ContainSubstring("UNAVAILABLE"))
			// No stale findings block when the funnel did not run.
			Expect(workflow).NotTo(ContainSubstring("ALREADY RUN"))
		})
	})

	Describe("frontmatter stripping", func() {
		It("strips YAML frontmatter keys and preserves body", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
				true,
				"{}",
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			Expect(workflow).NotTo(ContainSubstring("description:"))
			Expect(workflow).NotTo(ContainSubstring("allowed-tools:"))
			Expect(workflow).To(ContainSubstring("Procedure body line 1."))
			Expect(workflow).To(ContainSubstring("Procedure body line 2."))
		})
	})

	Describe("no frontmatter", func() {
		It("assembles body unchanged when no leading frontmatter", func() {
			noFrontmatter := "# PR Review\n\nBody without frontmatter.\n"
			writePlugin(noFrontmatter)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
				true,
				"{}",
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			Expect(workflow).To(ContainSubstring("Body without frontmatter."))
		})
	})

	Describe("plugin missing", func() {
		It("returns an error containing 'read plugin command file'", func() {
			// no file written — cmdDir exists but pr-review.md does not
			_, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
				true,
				"{}",
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("read plugin command file"))
		})
	})

	Describe("empty baseRef", func() {
		It("returns an error containing 'base_ref'", func() {
			_, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"",
				true,
				"{}",
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("base_ref"))
		})
	})

	Describe("empty reviewMode", func() {
		It("returns an error containing 'reviewMode'", func() {
			_, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"",
				"main",
				true,
				"{}",
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reviewMode"))
		})
	})

	Describe("time budget wrap-up contract", func() {
		It("assembles the wrap-up contract into the prompt the runner receives", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
				true,
				sampleFindings,
				"",
				libtime.Duration(25*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			// The budget reaches the prompt as a rendered duration string (25m).
			Expect(workflow).To(ContainSubstring("25m"))
			// The wrap-up wording: stop at the budget and flag every unexamined
			// ## Plan concern as `not verified`.
			Expect(workflow).To(ContainSubstring("not verified"))
			Expect(workflow).To(ContainSubstring("STOP"))
			Expect(workflow).To(ContainSubstring("time budget"))
		})
	})
})
