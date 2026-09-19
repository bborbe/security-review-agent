// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"fmt"
	"os"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	"github.com/bborbe/security-review-agent/mocks"
	pkg "github.com/bborbe/security-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fatalBuildLog is the shape of a real failed Cloud Build log: several
// error-shaped lines before the fatal one, plus a cgo "# <import path>" line
// that agentlib ParseMarkdown would otherwise mistake for a section heading.
const fatalBuildLog = `Step #0 - "check-golang": go: downloading github.com/foo/bar
# github.com/foo/bar
Step #0 - "check-golang": ./main.go:12:2: undefined: baz
Step #0 - "check-golang": exit status 1
ERROR: build step 0 "golang:1.27" failed: step exited with non-zero status: 2
`

const fatalBuildLogLine = `ERROR: build step 0 "golang:1.27" failed: step exited with non-zero status: 2`

// excerptLines returns the lines inside the ```text fence of a section body.
func excerptLines(section *agentlib.Section) []string {
	const open = "```text\n"
	start := strings.Index(section.Body, open)
	Expect(start).To(BeNumerically(">=", 0), "section body has no text fence")
	rest := section.Body[start+len(open):]
	end := strings.Index(rest, "\n```")
	Expect(end).To(BeNumerically(">=", 0), "section body has no closing fence")
	return strings.Split(rest[:end], "\n")
}

// longBuildLog returns a log of n filler lines followed by the fatal line.
func longBuildLog(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "Step #0 - \"check-golang\": compiling package %d\n", i)
	}
	b.WriteString(fatalBuildLogLine)
	b.WriteString("\n")
	return b.String()
}

var _ = Describe("ExtractFailureCause", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("extracts the failure excerpt",
		func(log string, maxLines int, expected string) {
			got, err := pkg.ExtractFailureCause(ctx, log, maxLines)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(expected))
		},
		Entry("single ERROR line", "line a\nERROR: boom\n", 5, "line a\nERROR: boom"),
		Entry(
			"the LAST error line wins over earlier transient ones",
			fatalBuildLog,
			2,
			"Step #0 - \"check-golang\": exit status 1\n"+fatalBuildLogLine,
		),
		Entry("falls back to the last non-empty lines when no marker is present",
			"alpha\nbeta\ngamma\n", 2, "beta\ngamma"),
		Entry("treats a cap below 1 as 1", "line a\nERROR: boom\n", 0, "ERROR: boom"),
		Entry("returns empty for an empty log", "", 5, ""),
		Entry("returns empty for a whitespace-only log", "   \n\n", 5, ""),
	)

	It("escapes a leading # so the excerpt cannot split the task into sections", func() {
		got, err := pkg.ExtractFailureCause(ctx, fatalBuildLog, 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(" # github.com/foo/bar"))
		Expect(got).NotTo(ContainSubstring("\n# github.com/foo/bar"))
		Expect(got).To(ContainSubstring(fatalBuildLogLine))
	})
})

var _ = Describe("buildFixStep", func() {
	var (
		ctx     context.Context
		fetcher *mocks.BuildLogFetcher
		step    agentlib.Step
		fixture string
	)

	BeforeEach(func() {
		ctx = context.Background()
		fetcher = &mocks.BuildLogFetcher{}
		step = pkg.NewBuildFixStepForTest(fetcher, "smedia-brogrammers", "europe-west3", 20)
		data, err := os.ReadFile("testdata/build-fix-task.md")
		Expect(err).NotTo(HaveOccurred())
		fixture = string(data)
	})

	It("constructs the production step without I/O", func() {
		Expect(pkg.NewBuildFixStep("smedia-brogrammers", "europe-west3")).NotTo(BeNil())
	})

	It("has name build-fix", func() {
		Expect(step.Name()).To(Equal("build-fix"))
	})

	It("ShouldRun always returns true", func() {
		md, err := agentlib.ParseMarkdown(ctx, fixture)
		Expect(err).NotTo(HaveOccurred())
		run, err := step.ShouldRun(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(run).To(BeTrue())
	})

	Describe("Run", func() {
		It("enriches the task with the failure analysis and routes to human_review", func() {
			fetcher.FetchLogReturns(fatalBuildLog, nil)
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())

			Expect(fetcher.FetchLogCallCount()).To(Equal(1))
			_, ref := fetcher.FetchLogArgsForCall(0)
			Expect(ref.BuildID).To(Equal("88027472-8599-454f-8112-569739235dad"))
			Expect(ref.Project).To(Equal("smedia-brogrammers"))
			Expect(ref.Region).To(Equal("europe-west3"))

			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))

			section, ok := md.FindSection("## Failure Analysis")
			Expect(ok).To(BeTrue())
			Expect(section.Body).To(ContainSubstring(fatalBuildLogLine))
			Expect(section.Body).To(ContainSubstring("88027472-8599-454f-8112-569739235dad"))
			Expect(section.Body).To(ContainSubstring("mdm-duplicate-finder"))
			Expect(md.Frontmatter["assignee"]).To(Equal(""))
		})

		It("unassigns the task so the cleared owner survives serialization", func() {
			fetcher.FetchLogReturns(fatalBuildLog, nil)
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())
			Expect(md.Frontmatter.Assignee()).To(Equal(agentlib.TaskAssignee("build-fixer-agent")))

			_, err = step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())

			marshaled, err := md.Marshal(ctx)
			Expect(err).NotTo(HaveOccurred())
			reparsed, err := agentlib.ParseMarkdown(ctx, marshaled)
			Expect(err).NotTo(HaveOccurred())
			Expect(reparsed.Frontmatter.Assignee()).To(BeEmpty())
		})

		It("records the pre-clear owner as previous_assignee when it unassigns", func() {
			fetcher.FetchLogReturns(fatalBuildLog, nil)
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())
			Expect(md.Frontmatter.Assignee()).To(Equal(agentlib.TaskAssignee("build-fixer-agent")))

			_, err = step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())

			marshaled, err := md.Marshal(ctx)
			Expect(err).NotTo(HaveOccurred())
			reparsed, err := agentlib.ParseMarkdown(ctx, marshaled)
			Expect(err).NotTo(HaveOccurred())
			Expect(reparsed.Frontmatter.Assignee()).To(BeEmpty())
			Expect(reparsed.Frontmatter["previous_assignee"]).To(Equal("build-fixer-agent"))
		})

		It("keeps exactly one ## Failure Analysis section across repeated runs", func() {
			fetcher.FetchLogReturns(fatalBuildLog, nil)
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())

			_, err = step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			afterFirst := len(md.Sections)

			_, err = step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(md.Sections)).To(Equal(afterFirst))

			marshaled, err := md.Marshal(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Count(marshaled, "## Failure Analysis")).To(Equal(1))
		})

		It("re-parses the enriched task into a single Failure Analysis section", func() {
			fetcher.FetchLogReturns(fatalBuildLog, nil)
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())

			_, err = step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())

			marshaled, err := md.Marshal(ctx)
			Expect(err).NotTo(HaveOccurred())
			reparsed, err := agentlib.ParseMarkdown(ctx, marshaled)
			Expect(err).NotTo(HaveOccurred())

			headings := make([]string, 0, len(reparsed.Sections))
			for _, section := range reparsed.Sections {
				headings = append(headings, section.Heading)
			}
			Expect(headings).To(ContainElement("## Failure Analysis"))
			for _, heading := range headings {
				Expect(heading).NotTo(ContainSubstring("github.com/foo/bar"))
			}
			Expect(strings.Count(marshaled, "## Failure Analysis")).To(Equal(1))
		})

		It("honors the injected excerpt cap", func() {
			fetcher.FetchLogReturns(longBuildLog(60), nil)

			small, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())
			smallStep := pkg.NewBuildFixStepForTest(fetcher, "p", "r", 3)
			_, err = smallStep.Run(ctx, small)
			Expect(err).NotTo(HaveOccurred())
			smallSection, ok := small.FindSection("## Failure Analysis")
			Expect(ok).To(BeTrue())
			smallLines := excerptLines(smallSection)
			Expect(len(smallLines)).To(BeNumerically("<=", 3))
			Expect(smallLines[len(smallLines)-1]).To(Equal(fatalBuildLogLine))

			large, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())
			largeStep := pkg.NewBuildFixStepForTest(fetcher, "p", "r", 20)
			_, err = largeStep.Run(ctx, large)
			Expect(err).NotTo(HaveOccurred())
			largeSection, ok := large.FindSection("## Failure Analysis")
			Expect(ok).To(BeTrue())
			Expect(len(excerptLines(largeSection))).To(BeNumerically(">", len(smallLines)))
		})

		It("records a partial fetch but still analyzes the returned log", func() {
			fetcher.FetchLogReturns("Step #0 - \"check-golang\": truncated\n"+fatalBuildLogLine,
				errors.New(context.Background(), "connection reset"))
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			section, ok := md.FindSection("## Failure Analysis")
			Expect(ok).To(BeTrue())
			Expect(section.Body).To(ContainSubstring("partial"))
		})

		It("fails when the log fetch fails with an empty log", func() {
			fetcher.FetchLogReturns("", errors.New(context.Background(), "connection refused"))
			md, err := agentlib.ParseMarkdown(ctx, fixture)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("88027472-8599-454f-8112-569739235dad"))
			_, ok := md.FindSection("## Failure Analysis")
			Expect(ok).To(BeFalse())
		})

		It("needs_input when the task carries no build_id and never fetches", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
task_type: build-fix
assignee: build-fixer-agent
---
# Build Failure: no-id
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("build_id"))
			Expect(fetcher.FetchLogCallCount()).To(Equal(0))
		})

		It("needs_input when build_id is present but empty", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
task_type: build-fix
build_id: ""
---
# Build Failure: empty-id
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(fetcher.FetchLogCallCount()).To(Equal(0))
		})
	})
})
