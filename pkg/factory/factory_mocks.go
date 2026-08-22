// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6@v6.12.2 -generate

//counterfeiter:generate -o ../../mocks/result-deliverer.go --fake-name ResultDeliverer github.com/bborbe/agent.ResultDeliverer
