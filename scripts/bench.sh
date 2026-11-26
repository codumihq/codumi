#!/usr/bin/env bash
# Mede o mesmo pedido com e sem o Codumi, no Claude Code (modo só leitura).
# Uso: scripts/bench.sh <pasta-do-projeto> <saida-dir> "pergunta 1" "pergunta 2" …
# Cada pergunta roda duas vezes, numa sessão nova: sem MCP nenhum e com o
# Codumi (MCP + a mesma dica e os mesmos ganchos que o app usa). Grava as
# respostas e um results.tsv com tokens, custo em preço de API, voltas e tempo.
set -euo pipefail
proj=$1; out=$2; shift 2
mkdir -p "$out"
codumi=${CODUMI_BIN:-$(command -v codumi)}
model=${BENCH_MODEL:-sonnet}
hint='This project is indexed by Codumi (MCP tools mcp__codumi__*). Use it before grep, find, cat or sed: codumi_page for a screen (by route or component name), codumi_find for a text shown in the UI, codumi_refs for who calls something, codumi_route for an API route, codumi_table for a table/RPC and its RLS, codumi_note before editing a module, and codumi_impact before finishing. Read files only at the lines Codumi points to. If a Codumi tool finds nothing, try another Codumi tool before falling back to the shell.'
mcp=$(printf '{"mcpServers":{"codumi":{"command":"%s","args":["mcp"]}}}' "$codumi")
hooks=$(printf '{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"%s guard prompt","timeout":10}]}],"PreToolUse":[{"matcher":"Bash|Grep|Glob|Read|mcp__codumi__.*","hooks":[{"type":"command","command":"%s guard pre","timeout":10}]}],"PostToolUse":[{"matcher":"mcp__codumi__.*","hooks":[{"type":"command","command":"%s guard post","timeout":10}]}]}}' "$codumi" "$codumi" "$codumi")
printf 'n\tmode\ttokens\tnew_tokens\tcost_usd\tturns\tseconds\n' > "$out/results.tsv"
n=0
for q in "$@"; do
  n=$((n+1))
  for mode in sem com; do
    if [ $mode = com ]; then
      extra=(--mcp-config "$mcp" --append-system-prompt "$hint" --settings "$hooks")
    else
      extra=(--mcp-config '{"mcpServers":{}}')
    fi
    (cd "$proj" && claude -p "$q" --output-format json --model "$model" --permission-mode plan --strict-mcp-config "${extra[@]}") > "$out/q$n-$mode.json" || true
    python3 - "$out/q$n-$mode.json" "$n" "$mode" >> "$out/results.tsv" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
u = d.get("usage", {})
tot = u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0) + u.get("cache_read_input_tokens", 0) + u.get("output_tokens", 0)
new = u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0) + u.get("output_tokens", 0)
print(f"{sys.argv[2]}\t{sys.argv[3]}\t{tot}\t{new}\t{d.get('total_cost_usd', 0):.4f}\t{d.get('num_turns', 0)}\t{d.get('duration_ms', 0)/1000:.1f}")
PY
  done
done
cat "$out/results.tsv"
