// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the read → parse → resolve → dispatch
// ordering of the local CLI entry point. They drive prepareTaskAuth — the
// production method Run calls — so the ordering under test is the ordering that
// ships, not a re-implementation of it. They run under the Ginkgo suite
// bootstrapped in main_test.go; Ginkgo v2.32.1 rejects a second RunSpecs call in
// one binary, so no suite is registered here.
//
// Every application literal carries no App credentials, so resolveAuth fails
// before githubapp.MintIAT (a live HTTP mint) is reached. Nothing here mints.
var _ = Describe("run-task task type dispatch ordering", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	writeTask := func(content string) string {
		path := filepath.Join(GinkgoT().TempDir(), "task.md")
		Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
		return path
	}

	It("reads the task, resolves no credentials for build-fix, then dispatches", func() {
		app := &application{TaskFilePath: writeTask(
			"---\ntask_type: build-fix\nassignee: build-fixer-agent\n" +
				"build_id: 88027472-8599-454f-8112-569739235dad\n---\n# Build Failure\n",
		)}
		taskContent, token, agent, err := app.prepareTaskAuth(ctx, nil, "/tmp/repos", "/tmp/work")
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(BeEmpty())
		Expect(taskContent).To(ContainSubstring("build-fix"))
		Expect(agent).NotTo(BeNil())
	})

	It("still fails fast for a security-review task with no credentials", func() {
		app := &application{TaskFilePath: writeTask(
			"---\ntask_type: security-review\nassignee: security-reviewer-agent\n" +
				"clone_url: git@github.com:owner/repo.git\n---\n# Security Review\n",
		)}
		taskContent, token, agent, err := app.prepareTaskAuth(ctx, nil, "/tmp/repos", "/tmp/work")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(token).To(BeEmpty())
		Expect(taskContent).To(BeEmpty())
		Expect(agent).To(BeNil())
	})

	It("starts a healthcheck task with no credentials", func() {
		app := &application{TaskFilePath: writeTask(
			"---\ntask_type: healthcheck\n---\n# Healthcheck\n",
		)}
		taskContent, token, agent, err := app.prepareTaskAuth(ctx, nil, "/tmp/repos", "/tmp/work")
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(BeEmpty())
		Expect(taskContent).To(ContainSubstring("healthcheck"))
		Expect(agent).NotTo(BeNil())
	})

	It("treats a task without task_type as the security route and requires credentials", func() {
		app := &application{TaskFilePath: writeTask(
			"---\nclone_url: git@github.com:owner/repo.git\n---\n# Security Review\n",
		)}
		taskContent, token, agent, err := app.prepareTaskAuth(ctx, nil, "/tmp/repos", "/tmp/work")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(token).To(BeEmpty())
		Expect(taskContent).To(BeEmpty())
		Expect(agent).To(BeNil())
	})

	It("takes the stricter branch for an unknown task_type", func() {
		app := &application{TaskFilePath: writeTask(
			"---\ntask_type: unknown\n---\n# Unknown\n",
		)}
		_, token, agent, err := app.prepareTaskAuth(ctx, nil, "/tmp/repos", "/tmp/work")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(token).To(BeEmpty())
		Expect(agent).To(BeNil())
	})

	It("fails when the task file cannot be read", func() {
		app := &application{TaskFilePath: filepath.Join(GinkgoT().TempDir(), "absent.md")}
		_, _, _, err := app.prepareTaskAuth(ctx, nil, "/tmp/repos", "/tmp/work")
		Expect(err).To(HaveOccurred())
	})
})
