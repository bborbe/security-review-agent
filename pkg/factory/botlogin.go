// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import "github.com/bborbe/github-pr-review-agent/pkg/githubposter"

// ResolveBotLogin returns env[githubposter.BotLoginEnv] when set, else
// githubposter.DefaultBotLogin. Centralizes the fallback so all 3 call
// sites stay in sync.
func ResolveBotLogin(env map[string]string) string {
	if v := env[githubposter.BotLoginEnv]; v != "" {
		return v
	}
	return githubposter.DefaultBotLogin
}
