# TypeScript/JavaScript: SDK to ACP Migration

## Before (SDK direct)

```javascript
const { query } = await import('@anthropic-ai/claude-agent-sdk');

for await (const msg of query({
  prompt,
  options: {
    cwd,
    permissionMode: 'bypassPermissions',
    allowDangerouslySkipPermissions: true,
    allowedTools: ['Read', 'Edit', 'Write', 'Bash', 'Glob', 'Grep'],
    env: cleanEnv,
    signal,
  },
})) {
  if (msg.type === 'assistant') {
    for (const block of msg.message?.content || []) {
      if (block.type === 'text') onText(block.text);
    }
  }
}
```

## After (ACP Client)

```javascript
const { spawn } = require('node:child_process');
const readline = require('node:readline');

class AcpClient {
  constructor(cwd) {
    this.cwd = cwd;
    this.proc = null;
    this.pending = new Map();
    this.nextId = 1;
    this.sessionId = null;
    this._onEvent = null;
  }

  start() {
    const env = { ...process.env };
    delete env.ANTHROPIC_API_KEY;
    delete env.CLAUDECODE;
    delete env.CLAUDE_CODE_NEW_INIT;
    delete env.CLAUDE_CODE_ENTRYPOINT;

    const binPath = require('path').join(__dirname, '..', 'node_modules', '.bin', 'claude-agent-acp');
    this.proc = spawn('node', [binPath], {
      stdio: ['pipe', 'pipe', 'pipe'],
      env,
      cwd: this.cwd,
    });

    const rl = readline.createInterface({ input: this.proc.stdout });
    rl.on('line', (line) => {
      try {
        const msg = JSON.parse(line);
        if (msg.id && this.pending.has(msg.id)) {
          this.pending.get(msg.id).resolve(msg);
          this.pending.delete(msg.id);
        } else if (msg.method) {
          this._handleNotification(msg);
        }
      } catch {}
    });
  }

  _send(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = this.nextId++;
      this.pending.set(id, { resolve, reject });
      this.proc.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
      setTimeout(() => {
        if (this.pending.has(id)) { this.pending.delete(id); reject(new Error('timeout')); }
      }, 120000);
    });
  }

  _handleNotification(msg) {
    const update = msg.params?.update || {};
    const kind = update.sessionUpdate || '';
    let event = null;

    if (kind === 'agent_thought_chunk' && update.content?.text)
      event = { type: 'thought', text: update.content.text };
    else if (kind === 'agent_message_chunk' && update.content?.type === 'text')
      event = { type: 'text', text: update.content.text };
    else if (kind === 'tool_call') {
      const name = update._meta?.claudeCode?.toolName || update.title || 'tool';
      event = { type: 'tool_start', name, title: update.title, kind: update.kind, id: update.toolCallId };
    }
    else if (kind === 'tool_call_update') {
      if (update.status === 'completed')
        event = { type: 'tool_done', name: update._meta?.claudeCode?.toolName || '', id: update.toolCallId };
      else if (update.title)
        event = { type: 'tool_info', name: update._meta?.claudeCode?.toolName || '', title: update.title, id: update.toolCallId };
    }
    else if (kind === 'usage_update') {
      const pct = update.size ? Math.round((update.used / update.size) * 100) : 0;
      const cost = update.cost?.amount ? `$${update.cost.amount.toFixed(3)}` : '';
      event = { type: 'usage', pct, cost };
    }

    if (event && this._onEvent) this._onEvent(event);
  }

  async initialize() {
    await this._send('initialize', { protocolVersion: 1, clientCapabilities: { textFiles: true } });
  }

  async createSession() {
    const res = await this._send('session/new', {
      cwd: this.cwd,
      permissionMode: 'bypassPermissions',
      mcpServers: [],
    });
    this.sessionId = res.result?.sessionId;
  }

  async prompt(text) {
    if (!this.sessionId) await this.createSession();
    if (this._onEvent) this._onEvent({ type: 'start', prompt: text });
    const res = await this._send('session/prompt', {
      sessionId: this.sessionId,
      prompt: [{ type: 'text', text }],
    });
    if (this._onEvent) this._onEvent({ type: 'done', stopReason: res.result?.stopReason || 'end_turn' });
    return res;
  }

  onEvent(handler) { this._onEvent = handler; }
  kill() { this.proc?.kill(); this.proc = null; this.sessionId = null; }
}
```

## SDK to ACP Event Mapping

| SDK Pattern | ACP Notification | Event Shape |
|-------------|-----------------|-------------|
| `msg.type === 'assistant'` + `block.type === 'text'` | `agent_message_chunk` | `{ type: 'text', text }` |
| `block.type === 'thinking'` | `agent_thought_chunk` | `{ type: 'thought', text }` |
| `block.type === 'tool_use'` | `tool_call` | `{ type: 'tool_start', name, title, kind, id }` |
| `msg.type === 'tool_use_summary'` | `tool_call_update` (completed) | `{ type: 'tool_done', name, id }` |
| N/A | `usage_update` | `{ type: 'usage', pct, cost }` |

## Cancellation

```javascript
// Before (SDK):
const controller = new AbortController();
query({ prompt, options: { signal: controller.signal } });
controller.abort();

// After (ACP) -- kill process:
client.kill();

// After (ACP) -- cancel notification (keeps session):
client.proc.stdin.write(JSON.stringify({
  jsonrpc: '2.0', method: 'cancel', params: { sessionId: client.sessionId }
}) + '\n');
```

## Session Persistence (Multi-turn)

```javascript
await client.prompt('Read src/main/index.js and summarize');
await client.prompt('Now refactor the initialization to be async');  // remembers context
await client.prompt('Write tests for what you just changed');        // still has full context
```

## Wire Events to UI

**Electron:**
```javascript
client.onEvent((event) => {
  win.webContents.send('coding-log-event', event);
});
```

**HTTP SSE:**
```javascript
client.onEvent((event) => {
  const data = `data: ${JSON.stringify(event)}\n\n`;
  for (const res of sseClients) res.write(data);
});
```

## Configuration Mapping

| SDK Option | ACP Equivalent | Where |
|------------|---------------|-------|
| `cwd` | `session/new` params | createSession() |
| `permissionMode` | `session/new` params | createSession() |
| `allowedTools` | `session/new` params._meta.claudeCode.options.tools | createSession() |
| `env` | Process spawn env | start() |
| `signal` | `cancel` notification or `kill()` | Runtime |
