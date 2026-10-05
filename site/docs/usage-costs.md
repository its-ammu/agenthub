# Usage and cost

Click an agent in the dashboard to see what its session used.

## Claude Code

Token counts per model (input, output, cache reads and cache writes) are read from the session transcript in `~/.claude/projects` and priced at Anthropic API list prices. The panel shows:

- an estimated total cost;
- the split between the main session and its subagents;
- fast-mode turns, priced with the model's fast multiplier;
- a cost-over-time chart (hover a bar for the running total).

It is an estimate. Subscription plans bill differently from the API, and models without a known price are left out of the total.

## Cursor

Cursor does not store token usage locally, so no cost is shown. You get a rough token estimate from the transcript text (about 4 characters per token), and a pointer to Cursor's own dashboard for billed usage.

## Other tools

The panel says usage is not available. Only Claude Code and Cursor have transcript readers.

## Same machine only

The hub reads transcripts from the machine it runs on. Reports are accurate when the hub and your agents run on the same machine.

## Change the prices

The price table is a JSON file. To change a rate or add a model:

```sh
agenthub-server --print-prices > prices.json    # start from the built-in table
# edit prices.json: add a model, change a rate, set fast_multiplier
agenthub-server --prices prices.json
```

Or save the file as `<data>/prices.json` (`~/.agenthub/data/prices.json`), and the hub picks it up on start.
