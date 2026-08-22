// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	"github.com/bborbe/github-pr-review-agent/pkg/githubposter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResolveBotLogin", func() {
	DescribeTable(
		"resolveBotLogin",
		func(env map[string]string, expected string) {
			Expect(factory.ResolveBotLogin(env)).To(Equal(expected))
		},
		Entry("env nil → DefaultBotLogin", nil, githubposter.DefaultBotLogin),
		Entry("env empty map → DefaultBotLogin", map[string]string{}, githubposter.DefaultBotLogin),
		Entry(
			"env has empty string → DefaultBotLogin",
			map[string]string{githubposter.BotLoginEnv: ""},
			githubposter.DefaultBotLogin,
		),
		Entry(
			"env has custom value → returns it verbatim",
			map[string]string{githubposter.BotLoginEnv: "custom-bot"},
			"custom-bot",
		),
	)
})
