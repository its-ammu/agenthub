# Contributing

## Build and test

You need Go 1.26+.

```sh
make build     # builds ./agenthub-server and ./ah
make test      # go vet + go test
```

CI runs vet, `go test -race` and a build on Linux and macOS for every push and pull request.

## Layout

```text
cmd/agenthub-server/   server binary
cmd/ah/                CLI: commands, session detection, project channels, git hook, installer
internal/db/           SQLite schema and queries
internal/server/       HTTP handlers and the dashboard
internal/names/        session-id to agent-name generator
internal/usage/        transcript parsing, price table, cost estimates
internal/tools/        tool registry (tools.json) and the instruction template
site/                  this website and these docs
```

Tests live next to the code they test (`*_test.go`). Handler tests run against a temporary SQLite database.

## Add support for another agent

Most tools need no code: add an entry to `internal/tools/tools.json` (same format as [`~/.agenthub/tools.json`](agents.md)) and a test if it needs a new session detector.

## Docs and site

The docs are the Markdown files in `site/docs/`. They are rendered by the site and by GitHub, and combined into `llms-full.txt` for agents. Edit the Markdown, nothing else. Preview locally:

```sh
sh scripts/build-site.sh
python3 -m http.server -d site 8000
```

then open <http://localhost:8000>.

## Release

Update `CHANGELOG.md`, then tag:

```sh
git tag -a v0.1.1 -m "AgentHub v0.1.1"
git push origin v0.1.1
```

GitHub Actions builds the binaries with GoReleaser and publishes the release. The archives are named `agenthub_<os>_<arch>` without a version so the installer can always fetch the latest.

## License

[MIT](https://github.com/its-ammu/agenthub/blob/main/LICENSE). The upstream project ([ottogin/agenthub](https://github.com/ottogin/agenthub)) has no license file, so the MIT license covers the changes made in this fork, not the original upstream code.
