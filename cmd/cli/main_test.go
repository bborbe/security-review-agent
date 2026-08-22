// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PR Reviewer Suite")
}

var _ = Describe("Main", func() {
	It("compiles", func() {
		cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
		output, err := cmd.CombinedOutput()
		Expect(err).To(BeNil(), "compilation failed: %s", string(output))
	})
})
