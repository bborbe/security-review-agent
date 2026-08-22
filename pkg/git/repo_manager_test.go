// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bborbe/github-pr-review-agent/pkg/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RepoManager", func() {
	var (
		ctx      context.Context
		reposDir string
		workDir  string
		manager  git.RepoManager
		origin   *testOrigin
	)

	BeforeEach(func() {
		ctx = context.Background()
		reposDir = GinkgoT().TempDir()
		workDir = GinkgoT().TempDir()
		manager = git.NewRepoManager(git.WorkdirConfig{
			ReposPath: reposDir,
			WorkPath:  workDir,
		}, "")
		origin = newTestOrigin()
		DeferCleanup(os.RemoveAll, origin.Path)
	})

	Describe("EnsureBareClone", func() {
		Context("fresh repos dir — bare clone does not yet exist", func() {
			It("clones bare repo and returns a valid path", func() {
				barePath, err := manager.EnsureBareClone(ctx, origin.URL)
				Expect(err).To(BeNil())
				Expect(barePath).NotTo(BeEmpty())

				out, gitErr := exec.Command("git", "-C", barePath, "rev-parse", "--git-dir").
					Output()
				Expect(gitErr).To(BeNil())
				Expect(strings.TrimSpace(string(out))).To(Equal("."))
			})
		})

		Context("valid bare already present", func() {
			It("fetches without re-cloning and returns the same path", func() {
				barePath1, err := manager.EnsureBareClone(ctx, origin.URL)
				Expect(err).To(BeNil())

				barePath2, err := manager.EnsureBareClone(ctx, origin.URL)
				Expect(err).To(BeNil())
				Expect(barePath2).To(Equal(barePath1))

				_, gitErr := exec.Command("git", "-C", barePath2, "rev-parse", "--git-dir").Output()
				Expect(gitErr).To(BeNil())
			})
		})

		Context("half-clone: directory exists but git rev-parse fails", func() {
			It("removes garbage directory and re-clones", func() {
				relPath, parseErr := git.ParseCloneURL(ctx, origin.URL)
				Expect(parseErr).To(BeNil())
				barePath := filepath.Join(reposDir, relPath)

				Expect(os.MkdirAll(barePath, 0750)).To(BeNil())
				Expect(
					os.WriteFile(filepath.Join(barePath, "garbage.txt"), []byte("junk"), 0600),
				).To(BeNil())

				result, err := manager.EnsureBareClone(ctx, origin.URL)
				Expect(err).To(BeNil())
				Expect(result).To(Equal(barePath))

				out, gitErr := exec.Command("git", "-C", barePath, "rev-parse", "--git-dir").
					Output()
				Expect(gitErr).To(BeNil())
				Expect(strings.TrimSpace(string(out))).To(Equal("."))
			})
		})

		Context("invalid clone URL", func() {
			It("returns a descriptive error", func() {
				_, err := manager.EnsureBareClone(ctx, "not-a-valid-url")
				Expect(err).NotTo(BeNil())
			})
		})
	})

	Describe("EnsureWorktree", func() {
		const taskID = "bd4d883b-1234-5678-abcd-123456789012"
		const taskID2 = "cd5e994c-2345-6789-bcde-234567890123"

		Context("new worktree with a branch ref", func() {
			It("creates worktree directory", func() {
				wPath, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
				Expect(err).To(BeNil())
				Expect(wPath).To(Equal(filepath.Join(workDir, taskID)))

				info, statErr := os.Stat(wPath)
				Expect(statErr).To(BeNil())
				Expect(info.IsDir()).To(BeTrue())
			})

			It("HEAD points at the branch ref", func() {
				wPath, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
				Expect(err).To(BeNil())

				out, gitErr := exec.Command("git", "-C", wPath, "rev-parse", "--abbrev-ref", "HEAD").
					Output()
				Expect(gitErr).To(BeNil())
				Expect(strings.TrimSpace(string(out))).To(Equal("feature-branch"))
			})
		})

		Context("worktree already exists", func() {
			It("returns the same path on the second call without error", func() {
				wPath1, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
				Expect(err).To(BeNil())

				wPath2, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", taskID)
				Expect(err).To(BeNil())
				Expect(wPath2).To(Equal(wPath1))
			})
		})

		Context("tag ref", func() {
			It("creates worktree at the tagged commit", func() {
				wPath, err := manager.EnsureWorktree(ctx, origin.URL, "v1.2.3", taskID)
				Expect(err).To(BeNil())

				out, gitErr := exec.Command("git", "-C", wPath, "rev-parse", "HEAD").Output()
				Expect(gitErr).To(BeNil())
				Expect(strings.TrimSpace(string(out))).To(Equal(origin.CommitSHA))
			})
		})

		Context("commit SHA ref", func() {
			It("creates worktree at the exact commit", func() {
				wPath, err := manager.EnsureWorktree(ctx, origin.URL, origin.CommitSHA, taskID2)
				Expect(err).To(BeNil())

				out, gitErr := exec.Command("git", "-C", wPath, "rev-parse", "HEAD").Output()
				Expect(gitErr).To(BeNil())
				Expect(strings.TrimSpace(string(out))).To(Equal(origin.CommitSHA))
			})
		})

		Context("invalid taskID", func() {
			It("returns error before touching disk", func() {
				_, err := manager.EnsureWorktree(ctx, origin.URL, "feature-branch", "not-a-uuid")
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("invalid task ID"))

				_, statErr := os.Stat(filepath.Join(workDir, "not-a-uuid"))
				Expect(os.IsNotExist(statErr)).To(BeTrue())
			})
		})

		Context("invalid ref", func() {
			It("returns error before touching disk", func() {
				_, err := manager.EnsureWorktree(ctx, origin.URL, "-bad-ref", taskID)
				Expect(err).NotTo(BeNil())
				Expect(err.Error()).To(ContainSubstring("invalid ref"))
			})
		})
	})

	Describe("PruneAllWorktrees", func() {
		Context("reposPath does not exist", func() {
			It("is a no-op and returns nil", func() {
				m := git.NewRepoManager(git.WorkdirConfig{
					ReposPath: filepath.Join(GinkgoT().TempDir(), "nonexistent"),
					WorkPath:  workDir,
				}, "")
				Expect(m.PruneAllWorktrees(ctx)).To(BeNil())
			})
		})

		Context("stale worktree admin entry", func() {
			It("removes the stale entry", func() {
				barePath, err := manager.EnsureBareClone(ctx, origin.URL)
				Expect(err).To(BeNil())

				staleDir, mkErr := os.MkdirTemp("", "stale-wt")
				Expect(mkErr).To(BeNil())
				DeferCleanup(os.RemoveAll, staleDir)

				addCmd := exec.Command(
					"git",
					"-C",
					barePath,
					"worktree",
					"add",
					staleDir,
					"feature-branch",
				)
				Expect(addCmd.Run()).To(BeNil())

				Expect(os.RemoveAll(staleDir)).To(BeNil())

				Expect(manager.PruneAllWorktrees(ctx)).To(BeNil())

				out, gitErr := exec.Command("git", "-C", barePath, "worktree", "list").Output()
				Expect(gitErr).To(BeNil())
				Expect(string(out)).NotTo(ContainSubstring(staleDir))
			})
		})
	})

	Describe("cmdEnv (subprocess env for git invocations)", func() {
		It(
			"returns nil when no token is configured so git inherits the parent env unchanged",
			func() {
				// The local-CLI / noop path constructs RepoManager with an empty token
				// because credentials come from the operator's own gh state in
				// ~/.config/gh/hosts.yml. Returning nil makes exec.Cmd inherit the
				// parent env so SSH_AUTH_SOCK / GIT_* / locale propagate as expected.
				m := git.NewRepoManager(
					git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
					"",
				)
				Expect(git.CmdEnv(m)).To(BeNil())
			},
		)

		It("returns an allowlist env containing GH_TOKEN when token is configured", func() {
			// The pod path passes the minted IAT so git subprocesses can
			// authenticate via the gh credential helper installed by
			// `gh auth setup-git`. Without GH_TOKEN in the git subprocess env,
			// the helper subprocess (`gh auth git-credential`) returns nothing
			// and git fails with "authentication required" — the failure mode
			// surfaced on bborbe/trading#136 even after PR #11 landed.
			const token = "ghs-TEST-IAT-NOT-REAL" //nolint:gosec // test literal, not a real credential
			m := git.NewRepoManager(
				git.WorkdirConfig{ReposPath: reposDir, WorkPath: workDir},
				token,
			)
			env := git.CmdEnv(m)
			Expect(env).To(ContainElement("GH_TOKEN=" + token))
			// HOME + PATH must be present so git can read ~/.gitconfig and resolve gh.
			homeFound, pathFound := false, false
			for _, e := range env {
				if strings.HasPrefix(e, "HOME=") {
					homeFound = true
				}
				if strings.HasPrefix(e, "PATH=") {
					pathFound = true
				}
			}
			Expect(homeFound).To(BeTrue(), "HOME must be in subprocess env (for ~/.gitconfig)")
			Expect(pathFound).To(BeTrue(), "PATH must be in subprocess env (to resolve git + gh)")
			// Defense in depth: env must NOT leak unrelated pod secrets. Only the
			// three allowlisted entries should be present.
			Expect(env).To(HaveLen(3))
		})
	})
})

// testOrigin holds a temporary non-bare git repository used as the remote in RepoManager tests.
type testOrigin struct {
	Path      string
	URL       string
	CommitSHA string
}

// newTestOrigin creates a temporary git repository that RepoManager can clone from.
// The repo is placed under /tmp explicitly (NOT os.TempDir(), which resolves to a
// multi-component path like /var/folders/.../T on macOS) so the resulting path has
// exactly 2 components (/tmp/gittestXXXXX) and ParseCloneURL derives a valid 2-segment
// relative path from the file://localhost/tmp/gittestXXXXX URL.
func newTestOrigin() *testOrigin {
	dir, err := os.MkdirTemp("/tmp", "gittest")
	Expect(err).To(BeNil())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })

	runCmd(dir, "git", "init")
	runCmd(dir, "git", "config", "user.email", "test@example.com")
	runCmd(dir, "git", "config", "user.name", "Test User")

	Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0600)).To(BeNil())
	runCmd(dir, "git", "add", "README.md")
	runCmd(dir, "git", "commit", "-m", "Initial commit")

	runCmd(dir, "git", "checkout", "-b", "feature-branch")
	Expect(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("Feature\n"), 0600)).To(BeNil())
	runCmd(dir, "git", "add", "feature.txt")
	runCmd(dir, "git", "commit", "-m", "Add feature")
	runCmd(dir, "git", "tag", "v1.2.3")

	shaBytes, shaErr := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	Expect(shaErr).To(BeNil())
	commitSHA := strings.TrimSpace(string(shaBytes))

	// dir must have exactly 2 path components for file://localhost URL to yield
	// a 2-segment path that ParseCloneURL accepts (e.g., /tmp/gittestXXXX → "tmp/gittestXXXX").
	components := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	Expect(len(components)).To(Equal(2),
		"test origin path must have exactly 2 path components; got %d (%s). "+
			"os.TempDir() must resolve to a single-component path (e.g., /tmp) for this test.",
		len(components), dir,
	)

	url := "file://localhost/" + strings.Join(components, "/")

	return &testOrigin{
		Path:      dir,
		URL:       url,
		CommitSHA: commitSHA,
	}
}
