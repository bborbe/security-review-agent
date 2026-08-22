// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the unexported resolveAuth. They run under
// the Ginkgo suite bootstrapped in main_test.go. Both cases are hermetic —
// useGitHubApp is false, so githubapp.MintIAT (a live HTTP mint) is never
// reached. The App-mode *success* path is covered by lib/githubapp's own
// httptest-backed MintIAT tests.
var _ = Describe("application.resolveAuth", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("errors naming APP_ID (and not GH_TOKEN) when no App credentials are configured", func() {
		app := &application{}
		token, err := app.resolveAuth(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
		Expect(err.Error()).NotTo(ContainSubstring("GH_TOKEN"))
		Expect(token).To(BeEmpty())
	})

	It(
		"errors before minting when App ID + Installation ID are set but no PEM is provided",
		func() {
			app := &application{AppID: 1, InstallationID: 2}
			token, err := app.resolveAuth(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("APP_ID"))
			Expect(err.Error()).NotTo(ContainSubstring("GH_TOKEN"))
			Expect(token).To(BeEmpty())
		},
	)
})
