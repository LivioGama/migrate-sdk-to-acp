---
name: migrate-sdk-to-acp
description: Migrate code that spawns Claude CLI (claude -p), uses @anthropic-ai/claude-agent-sdk, or calls Claude from Go/Python subprocesses to the Agent Client Protocol (ACP). Use when replacing claude -p calls, subprocess.run(["claude",...]), exec.Command("claude",...), or SDK query() calls with stateful ACP sessions. Covers TypeScript, Go, and Python.
metadata:
  short-description: Migrate Claude CLI/SDK to ACP
---

# Migrate to ACP (Agent Client Protocol)

Migrate code from stateless Claude CLI/SDK calls to stateful ACP sessions with structured streaming.

## When to Use

- Replacing `query()` from `@anthropic-ai/claude-agent-sdk` (TypeScript/JavaScript)
- Replacing `claude -p` subprocess calls from Go or Python
- Replacing `subprocess.run(["claude", ...])` or `exec.Command("claude", ...)` patterns
- Adding streaming UI for Claude Code agent output (thinking, tool calls, text)
- Needing persistent sessions (multi-turn conversations)

## Prerequisites

```bash
# Required for all languages -- ACP is a Node.js binary
npm install @agentclientprotocol/claude-agent-acp
# or: bun add @agentclientprotocol/claude-agent-acp
```

Binary location: `node_modules/.bin/claude-agent-acp`

## Architecture: Before vs After

**Before (stateless):**
```
Go/Python/JS  -->  exec("claude -p 'do X'")  -->  parse stdout text  -->  done (no memory)
JS only       -->  import { query }           -->  async gen blocks    -->  done (no memory)
```

**After (ACP, stateful):**
```
Any language  -->  spawn ACP process  -->  JSON-RPC initialize  -->  session/new  -->  session/prompt  -->  ndjson notifications
```

## Protocol Reference

ACP communicates via **JSON-RPC 2.0 over stdin/stdout** (ndjson -- one JSON object per line).

### Lifecycle

```
1. Spawn:       node node_modules/.bin/claude-agent-acp
2. initialize:  {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"textFiles":true}}}
3. session/new: {"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/path","permissionMode":"bypassPermissions","mcpServers":[]}}
4. prompt:      {"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"abc","prompt":[{"type":"text","text":"Hello"}]}}
5. (receive many notifications, then response with id:3)
```

### Notification Events

Notifications have `method` but no `id`. Event type is in `params.update.sessionUpdate`:

| Event Kind | Description | Key Fields |
|------------|-------------|------------|
| `agent_thought_chunk` | Agent thinking | `params.update.content.text` |
| `agent_message_chunk` | Agent text output | `params.update.content.text` (when `content.type == "text"`) |
| `tool_call` | Tool started | `params.update.toolCallId`, `params.update._meta.claudeCode.toolName` |
| `tool_call_update` | Tool progress/done | `params.update.status` (`"completed"`), `params.update.toolCallId` |
| `usage_update` | Context & cost | `params.update.used`, `params.update.size`, `params.update.cost.amount` |

### Authentication: OAuth Only (No API Key)

**MANDATORY: ACP uses Claude OAuth tokens, never `ANTHROPIC_API_KEY`.**

ACP is designed to work with Claude Max subscriptions and Claude Pro/Team plans via OAuth. This means unlimited usage within your subscription -- no per-token API billing. The agent authenticates using cached OAuth credentials from `~/.claude/` (created by `claude login`).

**When spawning ACP, always strip these env vars:**
- `ANTHROPIC_API_KEY` -- MUST be removed. Causes auth conflicts and would bypass OAuth, incurring API costs instead of using your subscription.
- `CLAUDECODE` -- makes agent think it's a sub-instance
- `CLAUDE_CODE_NEW_INIT` -- same
- `CLAUDE_CODE_ENTRYPOINT` -- same

**Never set or pass `ANTHROPIC_API_KEY` in any ACP integration.** If the user's code previously used an API key with `claude -p`, that pattern must be removed during migration. ACP enforces OAuth-only auth by design.

### Tool Call Kinds

| Kind | Tools | UI Treatment |
|------|-------|-------------|
| `think` | Agent, Task, TodoWrite | Italic/gray, collapsible |
| `execute` | Bash | Terminal-style, show command |
| `read` | Read, Glob, Grep, WebFetch | File icon, show path |
| `edit` | Write, Edit | Diff view, show path |
| `search` | Glob, Grep, WebSearch | Search icon |
| `fetch` | WebFetch, WebSearch | Globe icon |

### Cancellation

```
Option A: kill the process
Option B: send {"jsonrpc":"2.0","method":"cancel","params":{"sessionId":"..."}}
```

## Language-Specific Migration

Choose the reference for your language:

- **TypeScript/JavaScript**: See [references/typescript.md](references/typescript.md) -- SDK `query()` to ACP, event mapping, Electron/SSE patterns
- **Go**: See [references/go.md](references/go.md) -- `exec.Command("claude",...)` to ACP client with goroutine streaming
- **Python**: See [references/python.md](references/python.md) -- `subprocess.run(["claude",..])` to ACP client (sync + async)

## Visualizer

A self-contained SSE server + dark-theme UI template for streaming ACP events to a browser.
See [references/visualizer.md](references/visualizer.md) for setup instructions.
Template files in `assets/visualizer/`.

## Gotchas

1. **ACP is async ndjson** -- responses come as separate JSON lines, not a single response
2. **Session startup takes 3-8s** -- create session upfront, not on first prompt
3. **`initialize` must be called first** -- before any other method, with `protocolVersion: 1`
4. **`mcpServers: []`** -- pass empty array if unused, otherwise loads from project config
5. **Notifications have no `id`** -- fire-and-forget from agent
6. **Multiple prompts queue** -- ACP queues internally, no need to wait
7. **`_meta.claudeCode.toolName`** -- real tool name is here, not in `update.title`
8. **Go: large scanner buffer** -- tool results can be >64KB, use 1MB buffer
9. **Python: flush after write** -- always `flush()` stdin or use `bufsize=1`
10. **Python async: `drain()` after write** -- `asyncio.subprocess` needs explicit drain
