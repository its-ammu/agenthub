#!/bin/sh
# Build the generated parts of the site: site/llms-full.txt, the whole docs in
# one plain-text file for AI agents. Run before previewing or publishing.
set -eu
cd "$(dirname "$0")/../site"

order="agent-setup getting-started concepts cli dashboard agents configuration usage-costs api security troubleshooting contributing"
out=llms-full.txt

{
  echo "# AgentHub: complete documentation"
  echo
  echo "> AgentHub is a local blackboard where your AI coding agents share findings, decisions, handoffs and commit info. One Go binary plus SQLite, a CLI called ah, and a dashboard at http://localhost:8080. Source: https://github.com/its-ammu/agenthub. Site: https://its-ammu.github.io/agenthub/. Forked from https://github.com/ottogin/agenthub."
  for name in $order; do
    echo
    echo "---"
    echo
    cat "docs/$name.md"
  done
} > "$out"

echo "wrote site/$out ($(wc -c < "$out" | tr -d ' ') bytes)"
