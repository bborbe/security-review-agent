// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vault-cli normalize alias round-trips", func() {
	Describe("NormalizeTaskStatus", func() {
		It("maps legacy 'todo' to the canonical TaskStatusNext", func() {
			status, ok := domain.NormalizeTaskStatus("todo")
			Expect(ok).To(BeTrue())
			Expect(status).To(Equal(domain.TaskStatusNext))
		})
	})

	Describe("NormalizeTaskPhase", func() {
		It("maps legacy 'in_progress' to the canonical TaskPhaseExecution", func() {
			phase, ok := domain.NormalizeTaskPhase("in_progress")
			Expect(ok).To(BeTrue())
			Expect(phase).To(Equal(domain.TaskPhaseExecution))
		})
	})
})
