// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"net/http"
	"time"

	prpkg "github.com/bborbe/security-review-agent/pkg"
	"github.com/bborbe/security-review-agent/pkg/githubposter"
	libtime "github.com/bborbe/time"
)

// CreatePrPoster wires a PrPoster backed by a scoped http.Client.
// token is the bot PAT (GH_TOKEN env); botLogin is the bot GitHub login
// (BOT_GITHUB_LOGIN env, default "ben-s-pull-request-reviewer[bot]" if empty). Pure plumbing; no logic.
func CreatePrPoster(
	token, botLogin string,
	currentDateTime libtime.CurrentDateTimeGetter,
) prpkg.PrPoster {
	return githubposter.NewPrPoster(
		&http.Client{Timeout: 15 * time.Second},
		token,
		botLogin,
		currentDateTime,
	)
}

// CreateReviewVerifier wires a ReviewVerifier backed by a scoped http.Client.
// token is the bot PAT; botLogin is the expected bot login.
func CreateReviewVerifier(
	token, botLogin string,
	currentDateTime libtime.CurrentDateTimeGetter,
) prpkg.ReviewVerifier {
	return githubposter.NewReviewVerifier(
		&http.Client{Timeout: 15 * time.Second},
		token,
		botLogin,
		currentDateTime,
	)
}
