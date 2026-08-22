// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bitbucket_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/bborbe/github-pr-review-agent/pkg/bitbucket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client", func() {
	var (
		ctx    context.Context
		client bitbucket.Client
		token  string
	)

	BeforeEach(func() {
		ctx = context.Background()
		token = "test-token-12345"
	})

	Context("NewClient", func() {
		It("creates a non-nil client with token", func() {
			client = bitbucket.NewClient(token)
			Expect(client).NotTo(BeNil())
		})

		It("implements Client interface", func() {
			var _ bitbucket.Client = bitbucket.NewClient(token) //nolint:staticcheck
		})
	})

	Context("GetPRBranches", func() {
		var (
			server   *httptest.Server
			branches bitbucket.PRBranches
			err      error
		)

		JustBeforeEach(func() {
			client = bitbucket.NewClient(token)
			// Use server.URL directly (includes http:// scheme for testing)
			branches, err = client.GetPRBranches(ctx, server.URL, "PROJ", "repo", 123)
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("successful response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.Method).To(Equal(http.MethodGet))
						Expect(
							r.URL.Path,
						).To(Equal("/rest/api/1.0/projects/PROJ/repos/repo/pull-requests/123"))
						Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + token))

						response := map[string]interface{}{
							"fromRef": map[string]interface{}{
								"displayId": "feature/my-branch",
							},
							"toRef": map[string]interface{}{
								"displayId": "master",
							},
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(response)
					}),
				)
			})

			It("returns the source and target branch names", func() {
				Expect(err).To(BeNil())
				Expect(branches.Source).To(Equal("feature/my-branch"))
				Expect(branches.Target).To(Equal("master"))
			})
		})

		Context("401 Unauthorized", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
					}),
				)
			})

			It("returns authentication error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("authentication failed"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("403 Forbidden", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusForbidden)
					}),
				)
			})

			It("returns permission error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("404 Not Found", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusNotFound)
					}),
				)
			})

			It("returns not found error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("PR not found"))
				Expect(
					err.Error(),
				).To(ContainSubstring("projects/PROJ/repos/repo/pull-requests/123"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("empty source branch in response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						response := map[string]interface{}{
							"fromRef": map[string]interface{}{
								"displayId": "",
							},
							"toRef": map[string]interface{}{
								"displayId": "master",
							},
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(response)
					}),
				)
			})

			It("returns error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("missing source branch"))
			})
		})

		Context("empty target branch in response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						response := map[string]interface{}{
							"fromRef": map[string]interface{}{
								"displayId": "feature/my-branch",
							},
							"toRef": map[string]interface{}{
								"displayId": "",
							},
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(response)
					}),
				)
			})

			It("returns error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("missing target branch"))
			})
		})

		Context("invalid JSON response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte("invalid json"))
					}),
				)
			})

			It("returns parse error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("failed to parse"))
			})
		})

		Context("context cancellation", func() {
			It("respects cancelled context", func() {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel() // Cancel immediately

				// Create server after cancelling to ensure context is already cancelled
				testServer := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusOK)
					}),
				)
				defer testServer.Close()

				client := bitbucket.NewClient(token)
				_, err := client.GetPRBranches(cancelCtx, testServer.URL, "PROJ", "repo", 123)

				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("context canceled"))
			})
		})
	})

	Context("PostComment", func() {
		var (
			server *httptest.Server
			err    error
		)

		JustBeforeEach(func() {
			client = bitbucket.NewClient(token)
			// Use server.URL directly (includes http:// scheme for testing)
			err = client.PostComment(ctx, server.URL, "PROJ", "repo", 123, "Test comment")
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("successful response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.Method).To(Equal(http.MethodPost))
						Expect(
							r.URL.Path,
						).To(Equal("/rest/api/1.0/projects/PROJ/repos/repo/pull-requests/123/comments"))
						Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + token))
						Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))

						var body map[string]string
						err := json.NewDecoder(r.Body).Decode(&body)
						Expect(err).To(BeNil())
						Expect(body["text"]).To(Equal("Test comment"))

						w.WriteHeader(http.StatusCreated)
					}),
				)
			})

			It("posts comment successfully", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("401 Unauthorized", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
					}),
				)
			})

			It("returns authentication error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("authentication failed"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("403 Forbidden", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusForbidden)
					}),
				)
			})

			It("returns permission error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("404 Not Found", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusNotFound)
					}),
				)
			})

			It("returns not found error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("PR not found"))
				Expect(
					err.Error(),
				).To(ContainSubstring("projects/PROJ/repos/repo/pull-requests/123"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("context cancellation", func() {
			It("respects cancelled context", func() {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel() // Cancel immediately

				// Create server after cancelling to ensure context is already cancelled
				testServer := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusOK)
					}),
				)
				defer testServer.Close()

				client := bitbucket.NewClient(token)
				err := client.PostComment(
					cancelCtx,
					testServer.URL,
					"PROJ",
					"repo",
					123,
					"Test comment",
				)

				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("context canceled"))
			})
		})
	})

	Context("Approve", func() {
		var (
			server *httptest.Server
			err    error
		)

		JustBeforeEach(func() {
			client = bitbucket.NewClient(token)
			err = client.Approve(ctx, server.URL, "PROJ", "repo", 123)
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("successful response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.Method).To(Equal(http.MethodPost))
						Expect(
							r.URL.Path,
						).To(Equal("/rest/api/1.0/projects/PROJ/repos/repo/pull-requests/123/approve"))
						Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + token))

						w.WriteHeader(http.StatusOK)
					}),
				)
			})

			It("approves successfully", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("409 Conflict (already approved)", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusConflict)
					}),
				)
			})

			It("treats as success", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("401 Unauthorized", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
					}),
				)
			})

			It("returns authentication error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("authentication failed"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("403 Forbidden", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusForbidden)
					}),
				)
			})

			It("returns permission error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("404 Not Found", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusNotFound)
					}),
				)
			})

			It("returns not found error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("PR not found"))
				Expect(
					err.Error(),
				).To(ContainSubstring("projects/PROJ/repos/repo/pull-requests/123"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})
	})

	Context("buildURL via production host", func() {
		It("upgrades plain http:// to https:// for non-loopback hosts", func() {
			client := bitbucket.NewClient(token)
			// Exercise buildURL indirectly: a cancelled context prevents the actual
			// HTTP call, but the error must reference the https:// URL.
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel()
			_, err := client.GetPRBranches(
				cancelCtx,
				"http://bitbucket.example.com",
				"PROJ",
				"repo",
				1,
			)
			Expect(err).NotTo(BeNil())
			// The actual HTTP request URL must be https (upgraded from http).
			Expect(err.Error()).To(ContainSubstring("https://bitbucket.example.com"))
		})
	})

	Context("NeedsWork", func() {
		var (
			server *httptest.Server
			err    error
		)

		JustBeforeEach(func() {
			client = bitbucket.NewClient(token)
			err = client.NeedsWork(ctx, server.URL, "PROJ", "repo", 123, "test-user")
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("successful response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.Method).To(Equal(http.MethodPut))
						Expect(
							r.URL.Path,
						).To(
							Equal(
								"/rest/api/1.0/projects/PROJ/repos/repo/pull-requests/123/participants/test-user",
							),
						)
						Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + token))
						Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))

						var body map[string]interface{}
						err := json.NewDecoder(r.Body).Decode(&body)
						Expect(err).To(BeNil())
						Expect(body["approved"]).To(BeFalse())
						Expect(body["status"]).To(Equal("NEEDS_WORK"))
						user, ok := body["user"].(map[string]interface{})
						Expect(ok).To(BeTrue())
						Expect(user["slug"]).To(Equal("test-user"))

						w.WriteHeader(http.StatusOK)
					}),
				)
			})

			It("sets needs work status successfully", func() {
				Expect(err).To(BeNil())
			})
		})

		Context("401 Unauthorized", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
					}),
				)
			})

			It("returns authentication error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("authentication failed"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("403 Forbidden", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusForbidden)
					}),
				)
			})

			It("returns permission error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})

		Context("404 Not Found", func() {
			BeforeEach(func() {
				server = httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusNotFound)
					}),
				)
			})

			It("returns not found error", func() {
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("PR not found"))
				Expect(
					err.Error(),
				).To(ContainSubstring("projects/PROJ/repos/repo/pull-requests/123"))
				Expect(err.Error()).NotTo(ContainSubstring(token))
			})
		})
	})
})
