include ../../../../Makefile.variables

# cmd binaries are CLI tools (installed via `go install`), not k8s services.
# Stub buca/apply so the parent Makefile.folder recursion succeeds as a no-op
# instead of failing with "No rule to make target 'apply'".
.PHONY: buca
buca:
	@:

.PHONY: apply
apply:
	@:

TASK_FILE ?= dummy-task.md
BRANCH := dev
CLAUDE_CONFIG_DIR ?= $(HOME)/.claude-agent
AGENT_DIR ?= $(shell cd ../.. && pwd)/agent
MODEL ?= sonnet

# Test PR for git-checkout execution.
# Override CLONE_URL / REF / BASE_REF / TASK_ID to target a different PR.
CLONE_URL ?= https://github.com/bborbe/maintainer.git
REF       ?= $(shell gh pr view 2 --repo bborbe/maintainer --json headRefOid -q .headRefOid)
BASE_REF  ?= master
TASK_ID   ?= 22222222-aaaa-bbbb-cccc-333333333333
PR_URL    ?= https://github.com/bborbe/maintainer/pull/2

generate-dummy-task:
	@echo '---' > $(TASK_FILE)
	@echo 'status: in_progress' >> $(TASK_FILE)
	@echo 'phase: in_progress' >> $(TASK_FILE)
	@echo 'title: PR Review Smoke Test' >> $(TASK_FILE)
	@echo 'assignee: pr-reviewer-agent' >> $(TASK_FILE)
	@echo 'task_identifier: $(TASK_ID)' >> $(TASK_FILE)
	@echo 'clone_url: $(CLONE_URL)' >> $(TASK_FILE)
	@echo 'ref: $(REF)' >> $(TASK_FILE)
	@echo 'base_ref: $(BASE_REF)' >> $(TASK_FILE)
	@echo '---' >> $(TASK_FILE)
	@echo '' >> $(TASK_FILE)
	@echo 'Review the pull request at $(PR_URL).' >> $(TASK_FILE)
	@echo '' >> $(TASK_FILE)
	@echo '## Plan' >> $(TASK_FILE)
	@echo '' >> $(TASK_FILE)
	@echo 'Diff $(BASE_REF)...HEAD, inspect each change for correctness, security, and style; report verdict.' >> $(TASK_FILE)
	@echo "generated $(TASK_FILE)"

run-dummy-task: $(TASK_FILE)
	@GH_TOKEN=$$(gh auth token) go run -mod=mod . \
		-task-file=$(TASK_FILE) \
		-branch=$(BRANCH) \
		-agent-dir=$(AGENT_DIR) \
		-claude-config-dir=$(CLAUDE_CONFIG_DIR) \
		-model=$(MODEL) \
		-v=2
