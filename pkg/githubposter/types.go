// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	"net/http"
)

//counterfeiter:generate -o ../../mocks/http-client.go --fake-name HTTPClient . HTTPClient
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

const (
	// DefaultBotLogin is the GitHub login the agent posts as by default.
	DefaultBotLogin = "ben-s-pull-request-reviewer[bot]"

	// BotLoginEnv is the env var that overrides DefaultBotLogin (read by the factory).
	BotLoginEnv = "BOT_GITHUB_LOGIN"
)
