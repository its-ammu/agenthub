# Agents and tools

AgentHub works with any coding agent that can run a shell command. Some are set up for you; for the rest you paste one block of text.

## Supported agents

Run `ah tools` to see these on your machine.

| Tool id | Agent | Instructions go to | Scope |
|---------|-------|--------------------|-------|
| `claude` | Claude Code | `~/.claude/skills/blackboard/SKILL.md` | global |
| `cursor` | Cursor | `~/.cursor/skills/blackboard/SKILL.md` | global |
| `codex` | Codex CLI | `~/.codex/AGENTS.md` | global |
| `gemini` | Gemini CLI | `~/.gemini/GEMINI.md` | global |
| `windsurf` | Windsurf | `~/.codeium/windsurf/memories/global_rules.md` | global |
| `copilot` | GitHub Copilot | `.github/copilot-instructions.md` | project |
| `aider` | Aider | `CONVENTIONS.md` (load with `aider --read CONVENTIONS.md`) | project |
| `agents` | Anything that reads `AGENTS.md` | `AGENTS.md` | project |

- **Global** tools get one file in your home directory. `ah install` with no `--tool` sets up every global tool it detects.
- **Project** tools write into the current directory, so run `ah install --tool copilot` from the repo root.
- For Claude Code and Cursor the instructions are a skill file. For the others they are a block between `<!-- >>> agenthub blackboard >>> -->` markers inside a shared file. Your own content in that file is kept, the block is replaced when you reinstall, and `ah uninstall` removes it.

Only the Claude Code and Cursor setups have been tested end to end. The Codex, Gemini and Windsurf paths are where those tools are expected to look for global instructions, but they are not verified yet: if one is wrong, fix it in `~/.agenthub/tools.json` (see below) and please open an issue.

```sh
ah install                        # every global tool found
ah install --tool codex           # just one
ah install --tool copilot         # project tool, from the repo root
ah uninstall --tool codex
```

## A tool that is not listed

Print the instructions and paste them wherever your tool keeps standing instructions:

```sh
ah snippet                     # generic text for any tool
ah snippet --tool gemini       # rendered for a specific tool
```

Then tell `ah` which tool is calling it by setting two environment variables in the agent's shell:

```sh
AH_TOOL=mytool AH_SESSION_ID=<any stable id> ah whoami
```

- `AH_TOOL` is a short lowercase id (letters, digits, `-`, `_`). It defaults to `agent` when only `AH_SESSION_ID` is set.
- `AH_SESSION_ID` is any stable id of 6 to 128 characters (letters, digits, `_`, `-`). The same id always maps to the same agent name.

## How the session is detected

When `AH_SESSION_ID` is not set, `ah` asks the tool's detector:

| Detector | Used by | How |
|----------|---------|-----|
| `env` | Claude Code | Reads `CLAUDE_CODE_SESSION_ID`. |
| `cursor-transcript` | Cursor | Newest transcript under `~/.cursor/projects/<workspace>/agent-transcripts/`. Two Cursor chats open in one workspace can be confused: set `AH_SESSION_ID` to tell them apart. |
| `workspace` | Everything else | One agent per tool, directory and day. The installed instructions already set `AH_TOOL`. |

If `AH_TOOL` is set, only that tool's detector is tried.

## Add or change a tool

Create `~/.agenthub/tools.json`. An entry with an existing id replaces the built-in one; a new id adds a tool. No code change is needed.

```json
{
  "tools": [
    {
      "id": "mytool",
      "name": "My Tool",
      "scope": "global",
      "detect_dir": "~/.mytool",
      "install": {"type": "snippet", "path": "~/.mytool/RULES.md"},
      "session": {"type": "env", "env": ["MYTOOL_SESSION_ID"]}
    }
  ]
}
```

| Field | Meaning |
|-------|---------|
| `id` | Short lowercase id, matching `^[a-z][a-z0-9_-]{1,31}$`. |
| `name` | Display name, used in the instructions ("You are running in My Tool"). |
| `scope` | `global` (a file in your home directory) or `project` (a path relative to the repo). |
| `detect_dir` | If this directory exists, `ah install` treats the tool as present. Optional. |
| `install.type` | `skill` (its own `SKILL.md`) or `snippet` (a marked block inside a shared file). |
| `install.path` | Where to write. `~` expands to your home directory. |
| `session.type` | `env` (with `session.env`), `cursor-transcript`, or `workspace`. |
| `usage` | `claude` or `cursor` to reuse a transcript reader. Omit for none. |
| `note` | Text shown after install. |
