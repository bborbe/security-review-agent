Final response MUST be a single JSON object with this schema:

```json
{
  "verdict": "approve | request-changes",
  "summary": "1-2 sentence overall assessment",
  "comments": [
    {
      "file": "path/to/file.go",
      "line": 42,
      "severity": "critical | major | minor | nit",
      "message": "..."
    }
  ],
  "concerns_addressed": [
    "security: rate-limit added in handler.go:45",
    "correctness: context propagation fixed in query.go:88"
  ]
}
```

Field rules:
- `verdict`: required, one of the listed values
- `summary`: required, single short paragraph
- `comments`: required, may be empty list for `approve` with no nits
- Each comment requires `file`, `line`, `severity`, `message`
- `concerns_addressed`: required, lists each concern from `## Plan` with
  resolution status: `addressed` (a code change or comment resolves it),
  `not an issue` (examined and confirmed), or `not verified` (investigation
  stopped at the time budget before the concern could be examined). A concern
  listed as `not verified` makes the review incomplete: an `approve` verdict
  carrying any `not verified` concern is fail-closed to `request-changes`.

Output the JSON inside a fenced code block (```json ... ```). No prose before or after the fence. The fence renders the JSON readably in Obsidian and other markdown viewers; downstream consumers strip the fence before parsing.
