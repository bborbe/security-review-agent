// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/agent/delivery"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/githubauth"
	libkafkamocks "github.com/bborbe/kafka/mocks"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Factory", func() {
	Describe("ExecutionTools", func() {
		tools := factory.ExecutionTools

		It("grants selector-mode in-session review tools", func() {
			Expect(tools).To(ContainElement("Read"))
			Expect(tools).To(ContainElement("Grep"))
			Expect(tools).To(ContainElement("Glob"))
			Expect(tools).To(ContainElement("Bash(git rev-parse:*)"))
			Expect(tools).To(ContainElement("Bash(command -v:*)"))
			Expect(tools).To(ContainElement("Bash(jq:*)"))
		})

		It("does NOT grant the ast-grep runner — the agent runs the funnel itself", func() {
			for _, tool := range tools {
				Expect(tool).NotTo(ContainSubstring("ast-grep-runner.sh"))
			}
		})

		It("keeps the anti-injection boundary — no write or network tools", func() {
			for _, tool := range tools {
				Expect(tool).NotTo(Equal("Write"))
				Expect(tool).NotTo(Equal("Edit"))
				Expect(tool).NotTo(ContainSubstring("curl"))
				Expect(tool).NotTo(ContainSubstring("wget"))
				Expect(tool).NotTo(ContainSubstring("Bash(bash:"))
			}
		})

		It(
			"permits read-only `git -C <workdir>` forms and keeps the write/network boundary",
			func() {
				const workdir = "/work/32d9ce02-d843-5ed5-9104-552c3ac37aa7"
				const sha = "3762333d68eed6fba2fc5208ec8bbac407532108"
				const bareDiff = "git diff " + sha + "...HEAD -- file.go"
				readOnly := []string{
					// with-args forms — matched by the scoped Bash(git -C * <subcmd> *) entries
					"git -C " + workdir + " diff " + sha +
						"...HEAD -- export/append/pkg/message-handler-event.go",
					"git -C " + workdir + " log --oneline -5",
					"git -C " + workdir + " show --stat",
					"git -C " + workdir + " status --short",
					"git -C " + workdir + " ls-files --others",
					"git -C " + workdir + " fetch origin main",
					"git -C " + workdir + " worktree list",
					"git -C " + workdir + " branch -a",
					"git -C " + workdir + " rev-parse --abbrev-ref HEAD",
					// no-arg forms — matched by the bare Bash(git -C * <subcmd>) entries
					"git -C " + workdir + " status",
					"git -C " + workdir + " branch",
				}
				denied := []string{
					// write/network boundary — no scoped -C entry exists for these subcommands
					"git -C " + workdir + " push origin main",
					"git -C " + workdir + " clean -fd",
					"git -C " + workdir + " checkout -b feature",
					"git -C " + workdir + " reset --hard",
					"git -C " + workdir + " merge main",
				}
				for _, command := range readOnly {
					Expect(allowedBy(factory.ExecutionTools, "Bash", command)).To(BeTrue())
				}
				Expect(allowedBy(factory.ExecutionTools, "Bash", bareDiff)).To(BeTrue())
				for _, command := range denied {
					Expect(allowedBy(factory.ExecutionTools, "Bash", command)).To(BeFalse())
				}

				// Each scoped -C entry is present; the bare catch-all must not exist.
				for _, entry := range []string{
					"Bash(git -C * diff *)",
					"Bash(git -C * log *)",
					"Bash(git -C * show *)",
					"Bash(git -C * status *)",
					"Bash(git -C * ls-files *)",
					"Bash(git -C * fetch *)",
					"Bash(git -C * worktree *)",
					"Bash(git -C * branch *)",
					"Bash(git -C * rev-parse *)",
					"Bash(git -C * diff)",
					"Bash(git -C * log)",
					"Bash(git -C * show)",
					"Bash(git -C * status)",
					"Bash(git -C * ls-files)",
					"Bash(git -C * fetch)",
					"Bash(git -C * worktree)",
					"Bash(git -C * branch)",
					"Bash(git -C * rev-parse)",
				} {
					Expect(factory.ExecutionTools).To(ContainElement(entry))
				}
				Expect(factory.ExecutionTools).NotTo(ContainElement("Bash(git -C:*)"))

				// Regression lock: without the scoped -C entries the -C read-only
				// forms flip to denied while the bare git diff form stays allowed.
				var baseline claudelib.AllowedTools
				for _, entry := range factory.ExecutionTools {
					if !strings.Contains(entry, "git -C") {
						baseline = append(baseline, entry)
					}
				}
				for _, command := range readOnly {
					Expect(allowedBy(baseline, "Bash", command)).To(BeFalse())
				}
				Expect(allowedBy(baseline, "Bash", bareDiff)).To(BeTrue())
			},
		)
	})

	Describe("CreateClaudeRunner", func() {
		It("returns a non-nil runner with empty env", func() {
			runner := factory.CreateClaudeRunner(
				"",
				"agent",
				"sonnet",
				map[string]string{},
				claudelib.AllowedTools{"Read"},
			)
			Expect(runner).NotTo(BeNil())
		})

		It("returns a non-nil runner with GH_TOKEN in env", func() {
			runner := factory.CreateClaudeRunner(
				"",
				"agent",
				"sonnet",
				map[string]string{"GH_TOKEN": "test-token"},
				claudelib.AllowedTools{"Read"},
			)
			Expect(runner).NotTo(BeNil())
		})
	})

	Describe("CreateAgent", func() {
		It("returns a non-nil agent with empty token and env", func() {
			var repoManager git.RepoManager
			currentDateTime := libtime.NewCurrentDateTime()
			agent := factory.CreateAgent(
				"",
				"agent",
				"sonnet",
				"",
				map[string]string{},
				repoManager,
				"standard",
				nil,
				nil,
				nil,
				currentDateTime,
				libtime.Duration(25*time.Minute),
			)
			Expect(agent).NotTo(BeNil())
		})

		It("returns a non-nil agent with token set in env", func() {
			var repoManager git.RepoManager
			currentDateTime := libtime.NewCurrentDateTime()
			agent := factory.CreateAgent(
				"",
				"agent",
				"sonnet",
				"test-token",
				map[string]string{"GH_TOKEN": "test-token"},
				repoManager,
				"standard",
				nil,
				nil,
				nil,
				currentDateTime,
				libtime.Duration(25*time.Minute),
			)
			Expect(agent).NotTo(BeNil())
		})

	})

	Describe("CreateFileResultDeliverer", func() {
		It("returns a non-nil deliverer", func() {
			deliverer := factory.CreateFileResultDeliverer("/tmp/task.md")
			Expect(deliverer).NotTo(BeNil())
		})
	})

	Describe("CreateDeliverer", func() {
		It("returns a non-nil deliverer", func() {
			syncProducer := &libkafkamocks.KafkaSyncProducer{}
			currentDateTime := libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime {
				return libtime.DateTime{}
			})
			deliverer := factory.CreateDeliverer(
				syncProducer,
				agentlib.TaskIdentifier("task-123"),
				"dev",
				"content",
				currentDateTime,
			)
			Expect(deliverer).NotTo(BeNil())
		})
	})

	Describe("CreateKafkaResultDeliverer", func() {
		It("returns a non-nil deliverer", func() {
			syncProducer := &libkafkamocks.KafkaSyncProducer{}
			currentDateTime := libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime {
				return libtime.DateTime{}
			})
			deliverer := factory.CreateKafkaResultDeliverer(
				syncProducer,
				"dev",
				agentlib.TaskIdentifier("task-123"),
				"original content",
				currentDateTime,
			)
			Expect(deliverer).NotTo(BeNil())
		})
	})

	Describe("Passthrough content generator wiring — failure body", func() {
		var gen delivery.ContentGenerator
		var ctx context.Context

		BeforeEach(func() {
			gen = delivery.NewPassthroughContentGenerator()
			ctx = context.Background()
		})

		Context("when result status is needs_input with empty Output", func() {
			It("produces a body containing ## Failure and the message", func() {
				result := agentlib.AgentResultInfo{
					Status:  agentlib.AgentStatusNeedsInput,
					Message: "GH_TOKEN unauthorized (HTTP 401)",
					Output:  "",
				}
				generated, err := gen.Generate(ctx, "", result)
				Expect(err).NotTo(HaveOccurred())
				Expect(generated).To(ContainSubstring("## Failure"))
				Expect(generated).To(ContainSubstring("GH_TOKEN unauthorized (HTTP 401)"))
			})
		})

		Context("when result status is failed with empty Output", func() {
			It("produces a body containing ## Failure and the message", func() {
				result := agentlib.AgentResultInfo{
					Status:  agentlib.AgentStatusFailed,
					Message: "claude CLI: 401 Invalid authentication credentials",
					Output:  "",
				}
				generated, err := gen.Generate(ctx, "", result)
				Expect(err).NotTo(HaveOccurred())
				Expect(generated).To(ContainSubstring("## Failure"))
				Expect(
					generated,
				).To(ContainSubstring("claude CLI: 401 Invalid authentication credentials"))
			})
		})

		// updated for lib v0.62.29: needs_input no longer writes phase: human_review in passthrough content generator (see github.com/bborbe/agent CHANGELOG v0.62.27 / v0.62.29)
		Context("when result status is needs_input with Output containing frontmatter", func() {
			It("writes ## Failure with the message and preserves existing phase", func() {
				result := agentlib.AgentResultInfo{
					Status:  agentlib.AgentStatusNeedsInput,
					Message: "GH_TOKEN unauthorized (HTTP 401)",
					Output:  "---\nstatus: in_progress\nphase: planning\n---\n",
				}
				generated, err := gen.Generate(ctx, "", result)
				Expect(err).NotTo(HaveOccurred())
				Expect(generated).To(ContainSubstring("## Failure"))
				Expect(generated).To(ContainSubstring("GH_TOKEN unauthorized (HTTP 401)"))
			})
		})
	})

	Describe("CreateAgentProvider", func() {
		var (
			ctx         context.Context
			repoManager git.RepoManager
			provider    agentlib.AgentProvider
		)
		BeforeEach(func() {
			ctx = context.Background()
			currentDateTime := libtime.NewCurrentDateTime()
			provider = factory.CreateAgentProvider(
				"",
				"agent",
				"sonnet",
				"",
				map[string]string{},
				repoManager,
				"standard",
				nil,
				currentDateTime,
				libtime.Duration(25*time.Minute),
			)
			Expect(provider).NotTo(BeNil())
		})

		It("returns a non-nil agent for pr-review task type", func() {
			agent, err := provider.Get(ctx, agentlib.TaskTypePRReview)
			Expect(err).NotTo(HaveOccurred())
			Expect(agent).NotTo(BeNil())
		})

		It("returns a non-nil agent for healthcheck task type", func() {
			agent, err := provider.Get(ctx, agentlib.TaskTypeHealthcheck)
			Expect(err).NotTo(HaveOccurred())
			Expect(agent).NotTo(BeNil())
		})

		It("returns an error naming the bogus value and both accepted task types", func() {
			agent, err := provider.Get(ctx, agentlib.TaskType("bogus"))
			Expect(err).To(HaveOccurred())
			Expect(agent).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("unknown task_type"))
			Expect(err.Error()).To(ContainSubstring("bogus"))
			Expect(err.Error()).To(ContainSubstring("pr-review"))
			Expect(err.Error()).To(ContainSubstring("healthcheck"))
		})
	})

	Describe("RunConfig.AuthSetup wiring", func() {
		It("pod path: NewGhAuthSetupGit produces the real impl type", func() {
			cfg := factory.RunConfig{
				AuthSetup: githubauth.NewGhAuthSetupGit("fake-token"),
			}
			Expect(cfg.AuthSetup).NotTo(BeNil())
			Expect(reflect.TypeOf(cfg.AuthSetup).String()).To(Equal("*githubauth.ghAuthSetupGit"))
		})

		It("local-CLI path: NewNoopAuthSetup produces the noop impl type", func() {
			cfg := factory.RunConfig{
				AuthSetup: githubauth.NewNoopAuthSetup(),
			}
			Expect(cfg.AuthSetup).NotTo(BeNil())
			Expect(reflect.TypeOf(cfg.AuthSetup).String()).To(Equal("*githubauth.noopAuthSetup"))
		})
	})
})

// allowedBy reports whether command would be permitted for toolName by the
// given Claude-Code --allowedTools entries. It models the CLI's semantics for
// the three forms this suite uses: "Tool" (bare tool grant), "Tool(prefix:*)"
// (literal command prefix), and "Tool(pattern)" (glob where * matches any
// characters, including spaces). Test-only — the real matcher lives inside the
// Claude CLI, which a unit test cannot invoke.
func allowedBy(tools claudelib.AllowedTools, toolName, command string) bool {
	for _, entry := range tools {
		if entry == toolName {
			return true
		}
		if !strings.HasPrefix(entry, toolName+"(") || !strings.HasSuffix(entry, ")") {
			continue
		}
		pattern := strings.TrimSuffix(strings.TrimPrefix(entry, toolName+"("), ")")
		if rest, ok := strings.CutSuffix(pattern, ":*"); ok {
			if strings.HasPrefix(command, rest) {
				return true
			}
			continue
		}
		re := regexp.MustCompile("^" + globToRegexp(pattern) + "$")
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

// globToRegexp converts a Claude-Code tool pattern to an anchored regexp body,
// treating '*' as zero-or-more of any character and every other byte as
// literal.
func globToRegexp(pattern string) string {
	var b strings.Builder
	for _, part := range strings.Split(pattern, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	return strings.TrimSuffix(b.String(), ".*")
}
