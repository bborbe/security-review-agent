// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HasUnverifiedConcerns", func() {
	fence := func(body string) string {
		return "# Code Review\n\n```json\n" + body + "\n```\n"
	}

	DescribeTable("detects ## Plan concerns flagged as not verified",
		func(reviewBody string, expected bool) {
			Expect(pkg.HasUnverifiedConcerns(reviewBody)).To(Equal(expected))
		},
		// flagged lowercase `not verified` → the model stopped at the time budget.
		Entry(
			"flagged lowercase not verified",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit not verified"]}`,
			),
			true,
		),
		// case-insensitive match on the canonical wording.
		Entry(
			"flagged uppercase NOT VERIFIED",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit NOT VERIFIED"]}`,
			),
			true,
		),
		// `unverified` variant is matched too (robust to model phrasing).
		Entry(
			"unverified variant",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit unverified"]}`,
			),
			true,
		),
		// Benign "not verified" concern that self-describes as not-applicable
		// (config/docs-only diff, no code to verify) must NOT fail-close — the
		// model is explaining WHY verification was unnecessary, not that it ran
		// out of time budget. Regression 2026-08-23 (bborbe/math#18): the bare
		// "not verified" substring matched this and demoted approve → false
		// CHANGES_REQUESTED.
		Entry(
			"benign not-applicable concern does not fail-close",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: not verified (code logic not applicable — this is a config/changelog-only diff with no Go code changes)"]}`,
			),
			false,
		),
		// REGRESSION 2026-08-24 (bborbe/nuke#68): a benign "not verified" concern
		// that explains the verification gap as contextual (source not in this
		// repo → could not be cross-checked) escaped the benignUnverifiedPattern
		// whitelist and demoted a clean approve → false CHANGES_REQUESTED on
		// github-pr-review-agent v0.6.2 (re-review 5 min later posted APPROVED;
		// metric confirmed correct). The gate must key on the concern's own
		// blocker tiering, not a fixed phrase list — non-blocking explained gaps
		// pass.
		Entry(
			"benign not verified (source not in repo — could not be cross-checked)",
			fence(
				`{"verdict":"approve","concerns_addressed":["correctness: metric name agent_controller_results_written_total{result=\"not_found\"} — searched entire repo (go.mod, all source, all alerts) and the metric only appears in this new alert and the CHANGELOG. The agent-task-controller source is not in this repository, so the metric name and label value could not be cross-checked against the actual controller code. Not verified."]}`,
			),
			false,
		),
		// POSITIVE CONTROL 2026-08-24 (bborbe/nuke#73): a "not verified" concern
		// that IS a MUST-tier blocker (metric existence unconfirmed; without it
		// the alerts will never fire; must verify before deploying) must still
		// fail-close — the tier-keyed gate preserves true request-changes.
		Entry(
			"MUST-tier unverified blocker (alerts will never fire)",
			fence(
				`{"verdict":"approve","concerns_addressed":["correctness: expression references github_build_watcher_rate_limit_remaining — not verified: build-watcher source not present in this monorepo, metric existence cannot be confirmed; without it the alerts will never fire. Must verify metric is exported before deploying."]}`,
			),
			true,
		),
		// No flagged concern → the gate must not over-trigger.
		Entry(
			"no flags",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit addressed in handler.go:45"]}`,
			),
			false,
		),
		// Empty concerns list → no over-trigger.
		Entry("empty concerns_addressed",
			fence(`{"verdict":"approve","concerns_addressed":[]}`), false),
		// Missing concerns_addressed key → no over-trigger.
		Entry("no concerns_addressed key",
			fence(`{"verdict":"approve","reason":"clean"}`), false),
		// Prose without a verdict block → nothing to examine.
		Entry("prose without a verdict block",
			"LGTM. All checks pass.", false),
		// Malformed verdict JSON → unmarshal fails → no over-trigger.
		Entry("malformed JSON",
			fence(`{"verdict":"approve","concerns_addressed":["unterminated`), false),
	)
})
