Final response MUST be a single JSON object with this schema:

```json
{
  "pr_url": "https://github.com/owner/repo/pull/123",
  "pr_title": "...",
  "base_branch": "main",
  "head_branch": "feature/...",
  "files_changed": ["path/to/file.go", "..."],
  "scope": "feature | bugfix | refactor | test | docs | mixed",
  "focus_areas": ["security", "performance", "correctness", "tests"],
  "concerns": [
    {"area": "security", "file": "pkg/auth/handler.go", "note": "new endpoint without rate limit"},
    {"area": "correctness", "file": "pkg/db/query.go", "note": "missing context cancellation"}
  ]
}
```

Field rules:
- `pr_url`, `pr_title`, `base_branch`, `head_branch`: required strings
- `files_changed`: required, list of file paths from the diff
- `scope`: required, one of the listed values
- `focus_areas`: required, ordered by priority (most important first)
- `concerns`: required, may be empty list if nothing stands out

JSON safety: All string values in the JSON output MUST be valid JSON strings.
Double quotes that appear inside a string value (e.g. in code snippets) MUST
be escaped as `\"`. Examples:

- Go snippet `name != ""`  → write as `"note": "name != \"\""`
- Go snippet `if s == ""`  → write as `"note": "if s == \"\""`

Single quotes (`'`) and backticks (`` ` ``) do NOT need escaping.
If in doubt, rephrase the note to avoid literal double quotes.

Output the JSON inside a fenced code block (```json ... ```). No prose before or after the fence. The fence renders the JSON readably in Obsidian and other markdown viewers; downstream consumers strip the fence before parsing.
