// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
)

// MinReviewMaxDuration is the floor for REVIEW_MAX_DURATION. A value below
// this would kill a Claude phase run almost immediately; operators must
// instead keep the soft budget below the K8s Job ActiveDeadlineSeconds
// (executor default 1800s) with headroom for salvage + Kafka delivery.
const MinReviewMaxDuration = libtime.Duration(60 * time.Second)

// ValidateReviewMaxDuration fails fast when the configured soft time budget is
// below the 60s floor (which also rejects zero and negative values). Called by
// both entry points at startup so a malformed or near-zero REVIEW_MAX_DURATION
// cannot silently disable the budget or route every review to human_review.
func ValidateReviewMaxDuration(ctx context.Context, d libtime.Duration) error {
	if d.Duration() < MinReviewMaxDuration.Duration() {
		return errors.Errorf(
			ctx,
			"REVIEW_MAX_DURATION must be at least %s, got %s",
			MinReviewMaxDuration,
			d,
		)
	}
	return nil
}
