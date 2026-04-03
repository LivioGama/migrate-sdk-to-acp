<div align="center">

# `migrate-claude-sdk-to-acp`

**Faster programmatic Claude — one process, many prompts.**

[![npm](https://img.shields.io/npm/v/%40agentclientprotocol%2Fclaude-agent-acp?style=flat-square&color=ff6600)](https://www.npmjs.com/package/@agentclientprotocol/claude-agent-acp)
[![Claude Code Plugin](https://img.shields.io/badge/Claude%20Code-Plugin-blueviolet?style=flat-square)](https://docs.anthropic.com/en/docs/claude-code)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square)](LICENSE)

A Claude Code plugin with migration guides and ready-to-use ACP client implementations for TypeScript, Go, and Python.

[Install](#install) · [Why ACP?](#why-acp) · [Demo](#demo) · [What You Get](#what-you-get) · [Protocol Docs](https://agentclientprotocol.com)

</div>

---

## Install

```bash
claude plugin marketplace add LivioGama/migrate-claude-sdk-to-acp
claude plugin install migrate-claude-sdk-to-acp
```

That's it. The skill auto-activates when you ask Claude Code about migrating from the CLI, the Agent SDK, or subprocess patterns.

> **One-session usage** without installing: `claude --plugin-dir /path/to/migrate-claude-sdk-to-acp`

---

## Why ACP?

With `claude -p`, every call spawns a new process — startup, authentication, initialization, then exit. If you're sending multiple prompts from code, you pay that overhead every time.

[ACP (Agent Client Protocol)](https://agentclientprotocol.com) keeps a **single long-lived process**. Sessions stay in memory. Send as many prompts as you want without re-spawning.

```
claude -p:   spawn → init → auth → prompt → exit → spawn → init → auth → prompt → exit
ACP:         spawn → init → auth → prompt → prompt → prompt → prompt → ...
```

<table>
<tr><th></th><th><code>claude -p</code></th><th>ACP</th></tr>
<tr><td><b>Process lifecycle</b></td><td>New process per call</td><td>Single long-lived process</td></tr>
<tr><td><b>Session state</b></td><td>Loaded from disk on each resume</td><td>Stays in memory across prompts</td></tr>
<tr><td><b>Startup cost</b></td><td>3-8s per invocation</td><td>3-8s once, then instant</td></tr>
<tr><td><b>Streaming</b></td><td>Available (<code>--output-format stream-json</code>)</td><td>Built-in typed events (thinking, text, tools, usage)</td></tr>
<tr><td><b>Multi-turn</b></td><td>Possible (<code>--continue</code>, <code>--resume</code>)</td><td>Native — same session, same process</td></tr>
<tr><td><b>Protocol</b></td><td>Ad-hoc stdout parsing</td><td>JSON-RPC 2.0 over stdin/stdout</td></tr>
</table>

### Measured Performance

Benchmark: Haiku model, "say pong" × 5 sequential calls on Apple Silicon:

| Method | Per-call avg | 5-call total | Notes |
|--------|-------------|-------------|-------|
| `claude -p` | 1534ms | 7668ms | New process each call |
| SDK `query()` | 6579ms | 32894ms | Wraps CLI with extra overhead |
| **SDK streaming / ACP** | **1300ms** (warm) | **10539ms** | 5.1s startup, then ~1300ms/call |

The per-call difference is modest (~15%) since most latency is the API round-trip. The real gains come at scale — at 50 calls, ACP is ~10% faster overall — and from architectural benefits: no API key management, conversation context preserved across turns, and mid-session control (model switching, interrupts, permission changes).

> **Note:** `claude -p` with `--continue`/`--resume` and `--output-format stream-json` can achieve similar functionality. ACP's advantage is performance for multi-prompt workflows and a standardized protocol (JSON-RPC) instead of ad-hoc output parsing.

---

## Demo

https://github.com/user-attachments/assets/0380ff65-2b6b-4192-895d-0deb29b4e19f

---

## What You Get

The plugin provides **complete, copy-paste ACP clients** and migration guides for three languages:

<table>
<tr>
<th width="33%">TypeScript / JavaScript</th>
<th width="33%">Go</th>
<th width="33%">Python</th>
</tr>
<tr>
<td>

```js
const client = new AcpClient(cwd);
client.start();
await client.initialize();
await client.prompt('Refactor auth');
await client.prompt('Now add tests');
```

Migrates from `query()` in `@anthropic-ai/claude-agent-sdk`

</td>
<td>

```go
client := acp.NewClient(cwd)
client.Start(ctx)
client.Initialize()
client.Prompt("Refactor auth")
client.Prompt("Now add tests")
```

Migrates from `exec.Command("claude", "-p", ...)`

</td>
<td>

```python
client = AcpClient(cwd)
client.start()
client.initialize()
client.prompt("Refactor auth")
client.prompt("Now add tests")
```

Migrates from `subprocess.run(["claude", ...])`

</td>
</tr>
</table>

Each client includes:
- Full JSON-RPC protocol handling
- Streaming event parsing (thinking, text, tool calls, usage)
- Session persistence across prompts
- Cancellation support

A web visualizer is also included in `assets/visualizer/` as inspiration for building your own streaming UI.

---

## ACP Adapter

This repo also contains the source for [`@agentclientprotocol/claude-agent-acp`](https://www.npmjs.com/package/@agentclientprotocol/claude-agent-acp) — the ACP adapter that bridges the Claude Agent SDK to the ACP protocol.

```bash
npm install @agentclientprotocol/claude-agent-acp
```

Learn more at [agentclientprotocol.com](https://agentclientprotocol.com).

---

<div align="center">

**Apache-2.0** · Made by [Livio Gamassia](https://github.com/LivioGama)

</div>
