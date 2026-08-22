Based on my analysis, I have enough information to compile the review. The test agent couldn't execute due to permission restrictions, but I can review the code directly.

**Selector Mode Review — f2f8413 vs b044a72**

**Changes summary:**
- 5 files changed: CHANGELOG.md, pkg/export_test.go, pkg/llmjson.go (new), pkg/llmjson_test.go (new), pkg/steps_planning.go

**Code Review:**

1. **`pkg/llmjson.go`** — The 3-strategy parser is well-designed:
   - Strategy 1 (direct parse) handles pure JSON responses
   - Strategy 2 (fence-strip) handles markdown-wrapped JSON
   - Strategy 3 (last balanced brace) handles prose-then-JSON (dev run #2 shape)
   - `lastJSONBlock` correctly finds the last `}` then walks backward to find matching `{`
   - Error messages consistently include `"unmarshal llm json response"` marker

2. **`pkg/llmjson_test.go`** — Comprehensive table-driven tests cover all 5 real-world LLM response shapes plus 2 error cases. Edge case coverage is solid, including prose with unrelated braces before the real JSON.

3. **`pkg/export_test.go`** — `LLMJSONProbe` type and `ParseLLMJSONProbe` wrapper correctly expose the generic for external test package use.

4. **`pkg/steps_planning.go`** — Old inline `parseJSONResponse` + `jsonFenceRegexp` removed; replaced by a comment pointing to llmjson.go. The extraction into a shared package is clean and both call sites (planning at line 182, execution at line 272) route correctly.

5. **CHANGELOG.md** — Unreleased entry correctly describes the fix and its motivation.

**Concerns identified:**
- None critical. The implementation correctly handles the documented edge cases.

```json
{
  "verdict": "approve",
  "summary": "Clean extraction of parseJSONResponse into a shared package with comprehensive test coverage. The 3-strategy approach (direct parse → fence-strip → last balanced brace) correctly handles all documented LLM response shapes including the dev run #2 prose-then-JSON pattern. Both call sites (PlanOutput and executionReport) now route through the shared helper.",
  "comments": [],
  "concerns_addressed": [
    "correctness: 3-strategy JSON extraction in pkg/llmjson.go:41-68 — lastJSONBlock correctly handles nested brace edge cases by finding the last '}' and walking backward to depth=0",
    "correctness: pkg/steps_planning.go:285-287 comment confirms refactoring to shared llmjson.go, both call sites (steps_planning.go:182, steps_execution.go:272) route correctly",
    "tests: pkg/llmjson_test.go:23-71 table-driven tests cover pure JSON, fenced JSON, prose+JSON, prose with unrelated braces, garbage input, and unbalanced brace"
  ]
}
```
