Final response MUST be a single JSON object with this schema:

```json
{
  "verdict": "pass | fail",
  "reason": "1-2 sentence explanation of the verdict",
  "concerns_addressed": [
    {"concern": "security: rate-limit on new endpoint", "status": "addressed"},
    {"concern": "correctness: context propagation", "status": "missed"}
  ],
  "hallucinations": [
    {"file": "pkg/foo.go", "line": 99, "issue": "line 99 not in diff"}
  ],
  "verdict_consistency": "consistent | inconsistent: <reason>"
}
```

Field rules:
- `verdict`: required, exactly `pass` or `fail` (lowercase, no other values)
- `reason`: required, one or two sentences
- `concerns_addressed`: required, one entry per concern from `## Plan`;
  `status` is `addressed` or `missed`
- `hallucinations`: required, may be empty list
- `verdict_consistency`: required string

Output the JSON inside a fenced code block (```json ... ```). No prose before or after the fence. The fence renders the JSON readably in Obsidian and other markdown viewers; downstream consumers strip the fence before parsing.

The `verdict` field drives the next-phase transition:
- `pass` → task advances to `done`
- `fail` → task advances to `human_review` with the verdict reason
