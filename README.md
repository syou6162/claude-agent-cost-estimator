# claude-agent-cost-estimator

Estimate how much your current Claude Code / Claude Agent SDK usage would
cost at standard Anthropic API rates, by reading the JSONL transcripts in
`~/.claude/projects/<encoded-cwd>/`.

Motivation: starting **2026-06-15** Claude Agent SDK and `claude -p` usage
no longer counts toward Pro/Max plan limits. A per-plan monthly credit
(Pro $20, Max 5x $100, ...) is consumed first, and overflow is billed at
standard API rates ([support article](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)).
This tool lets you check, ahead of the cutover, what your existing
workloads would have cost.

## Build

```sh
go build -o cost-estimator ./cmd/cost-estimator
```

## Usage

```sh
./cost-estimator --cwd /path/to/some/repo                 # JSON to stdout
./cost-estimator --cwd /path/to/some/repo --pretty        # human-readable JSON
```

`--cwd` is required. Relative paths are accepted and automatically
resolved to absolute via `filepath.Abs` before lookup. The tool finds
the matching project under `${XDG_CONFIG_HOME:-~/.config}/claude/projects/`
or `~/.claude/projects/` (Claude encodes the cwd by replacing `/` and `.`
with `-`). Override the search paths with `--claude-config-dir <p1,p2,...>`
or the `CLAUDE_CONFIG_DIR` environment variable.

Output is a JSON array, one object per session:

```json
{
  "sessionId": "abc123",
  "projectPath": "/Users/foo/work/repo",
  "firstTimestamp": "2026-04-15T22:00:00.000Z",
  "lastTimestamp":  "2026-04-15T22:30:00.000Z",
  "models": [
    {
      "model": "claude-opus-4-7",
      "speed": "standard",
      "inputTokens": 12345,
      "outputTokens": 6789,
      "cacheCreationInputTokens": 4567,
      "cacheCreation5mInputTokens": 4567,
      "cacheCreation1hInputTokens": 0,
      "cacheReadInputTokens": 89012,
      "costUSD": 0.12345
    }
  ],
  "totalCostUSD": 0.12345,
  "knownCostUSD": 0.12345
}
```

## Aggregation with jq

```sh
# Monthly rollup (skipping sessions with unknown models)
./cost-estimator --cwd <cwd> \
  | jq '[.[] | select(.totalCostUSD != null)]
        | sort_by(.firstTimestamp[:7])
        | group_by(.firstTimestamp[:7])
        | map({month: .[0].firstTimestamp[:7], totalUSD: (map(.totalCostUSD) | add)})'

# Per-model rollup
./cost-estimator --cwd <cwd> \
  | jq '[.[].models[] | select(.costUSD != null)]
        | sort_by(.model)
        | group_by(.model)
        | map({model: .[0].model, totalUSD: (map(.costUSD) | add)})'

# Grand total
./cost-estimator --cwd <cwd> | jq '[.[].totalCostUSD | select(. != null)] | add'
```

## Caveats

- **Prices are hard-coded.** The table in `internal/pricing/pricing.go` is
  a snapshot of [the official pricing page](https://platform.claude.com/docs/en/about-claude/pricing)
  on 2026-05-16. Re-fetch and update when Anthropic changes prices.
- **1h cache writes are approximated as 5m when the transcript only has
  the flat `cache_creation_input_tokens` field.** The nested
  `cache_creation.ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens`
  form, when present, takes precedence and the flat field is ignored.
- **Unknown models become `costUSD: null`.** The session that contains
  them has `totalCostUSD: null` and a `knownCostUSD` field with the partial
  sum. A single deterministic WARN line is printed to stderr per unknown
  model. Update `modelAliases` / `modelPrefixRules` / `modelPricing` in
  `internal/pricing/pricing.go` to add support.
- **Excluded entries:** `role != "assistant"`, `message.model == "<synthetic>"`,
  and `isApiErrorMessage == true` rows are dropped before aggregation.
- **Dedup:** duplicate `(messageId, requestId)` rows keep the survivor
  with the larger `tokenTotal`; ties go to the row whose `speed` field is
  set (matches ccusage's `shouldReplaceEntryMetadata`).
- **Scope:** Anthropic API first-party rates only. Bedrock / Vertex AI /
  Batch API discounts / Data residency multipliers are out of scope.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | internal error (file read / scan / encode failure) |
| 2 | usage error: `--cwd` missing, project dir not found, or explicit `CLAUDE_CONFIG_DIR` with no valid `<path>/projects` |

## Comparing against ccusage

[`ccusage`](https://github.com/ryoppippi/ccusage) is the reference
implementation we modeled this tool after. To sanity-check the numbers:

```sh
cd /path/to/ccusage
pnpm install   # ccusage requires pnpm (preinstall only-allow pnpm)
bun run apps/ccusage/src/main.bun.ts claude session --json \
  | jq --arg p "<encoded-project-dir-name>" \
       '[.sessions[] | select(.projectPath | endswith($p))]'
```

Then diff against this tool's output for the same `--cwd`. See the
plan document under `.claude_work/plans/` for the exact comparison
preconditions (no unknown models, no `isApiErrorMessage`, nested
sessions only).

## Testing

```sh
go test ./...
```
