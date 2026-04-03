import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import readline from 'node:readline';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PORT = process.env.PORT || 7444;
const CWD = process.env.ACP_CWD || process.cwd();

// ── ACP Client ───────────────────────────────────────────────────────────────

class AcpClient {
	constructor() {
		this.proc = null;
		this.pending = new Map();
		this.nextId = 1;
		this.sessionId = null;
		this.ready = false;
		this.sseClients = new Set();
	}

	start() {
		const agentPath = path.join(CWD, 'node_modules', '.bin', 'claude-agent-acp');
		const env = { ...process.env };
		delete env.ANTHROPIC_API_KEY;
		delete env.CLAUDECODE;
		delete env.CLAUDE_CODE_NEW_INIT;
		delete env.CLAUDE_CODE_ENTRYPOINT;

		this.proc = spawn('node', [agentPath], {
			stdio: ['pipe', 'pipe', 'pipe'],
			env,
			cwd: CWD,
		});

		const rl = readline.createInterface({ input: this.proc.stdout });
		rl.on('line', (line) => {
			try {
				const msg = JSON.parse(line);
				if (msg.id && this.pending.has(msg.id)) {
					const { resolve } = this.pending.get(msg.id);
					this.pending.delete(msg.id);
					resolve(msg);
				} else if (msg.method) {
					this._handleNotification(msg);
				}
			} catch {}
		});

		this.proc.stderr.on('data', (d) => {
			const text = d.toString().trim();
			if (text) console.error('[ACP]', text);
		});

		this.proc.on('exit', (code) => {
			console.log(`[ACP] Process exited: ${code}`);
			this.ready = false;
		});
	}

	_handleNotification(msg) {
		const update = msg.params?.update || {};
		const kind = update.sessionUpdate || 'unknown';

		if (kind === 'agent_thought_chunk' && update.content?.text) {
			this._broadcast({ type: 'thought', text: update.content.text });
		}

		if (kind === 'agent_message_chunk' && update.content?.type === 'text') {
			this._broadcast({ type: 'text', text: update.content.text });
		}

		if (kind === 'tool_call') {
			const name = update._meta?.claudeCode?.toolName || update.title || 'tool';
			this._broadcast({ type: 'tool_start', name, title: update.title, kind: update.kind, id: update.toolCallId });
		}

		if (kind === 'tool_call_update') {
			const name = update._meta?.claudeCode?.toolName || '';
			const status = update.status || '';
			if (status === 'completed') {
				this._broadcast({ type: 'tool_done', name, id: update.toolCallId });
			} else if (update.title) {
				this._broadcast({ type: 'tool_info', name, title: update.title, id: update.toolCallId });
			}
		}

		if (kind === 'usage_update') {
			const pct = update.size ? Math.round((update.used / update.size) * 100) : 0;
			const cost = update.cost?.amount ? `$${update.cost.amount.toFixed(3)}` : '';
			this._broadcast({ type: 'usage', pct, cost });
		}
	}

	_broadcast(event) {
		const data = `data: ${JSON.stringify(event)}\n\n`;
		for (const res of this.sseClients) {
			try { res.write(data); } catch {}
		}
	}

	addSSEClient(res) {
		this.sseClients.add(res);
		res.on('close', () => this.sseClients.delete(res));
	}

	send(method, params = {}) {
		return new Promise((resolve, reject) => {
			const id = this.nextId++;
			this.pending.set(id, { resolve, reject });
			const msg = JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n';
			this.proc.stdin.write(msg);
			setTimeout(() => {
				if (this.pending.has(id)) {
					this.pending.delete(id);
					reject(new Error('ACP request timeout'));
				}
			}, 120000);
		});
	}

	async initialize() {
		const res = await this.send('initialize', {
			protocolVersion: 1,
			clientCapabilities: { textFiles: true },
		});
		this.ready = true;
		console.log('[ACP] Initialized:', res.result?.agentInfo?.name || 'ok');
	}

	async createSession() {
		const res = await this.send('session/new', {
			cwd: CWD,
			permissionMode: 'bypassPermissions',
			mcpServers: [],
		});
		this.sessionId = res.result?.sessionId;
		console.log('[ACP] Session:', this.sessionId);
	}

	async prompt(text) {
		if (!this.sessionId) await this.createSession();
		this._broadcast({ type: 'start' });
		const res = await this.send('session/prompt', {
			sessionId: this.sessionId,
			prompt: [{ type: 'text', text }],
		});
		this._broadcast({ type: 'done', stopReason: res.result?.stopReason || 'end_turn' });
		return res;
	}

	kill() { this.proc?.kill(); }
}

// ── Server ───────────────────────────────────────────────────────────────────

const client = new AcpClient();

async function startServer() {
	console.log('[Voice Swarm] Starting agent...');
	client.start();
	await client.initialize();
	await client.createSession();
	console.log('[Voice Swarm] Agent ready');

	const server = http.createServer(async (req, res) => {
		// SSE stream for real-time output
		if (req.url === '/stream') {
			res.writeHead(200, {
				'Content-Type': 'text/event-stream',
				'Cache-Control': 'no-cache',
				'Connection': 'keep-alive',
				'Access-Control-Allow-Origin': '*',
			});
			res.write('data: {"type":"connected"}\n\n');
			client.addSSEClient(res);
			return;
		}

		// Send prompt
		if (req.method === 'POST' && req.url === '/prompt') {
			let body = '';
			req.on('data', c => body += c);
			req.on('end', async () => {
				try {
					const { prompt } = JSON.parse(body);
					if (!prompt || prompt.trim().length < 3) {
						res.writeHead(400, { 'Content-Type': 'application/json' });
						res.end(JSON.stringify({ ok: false, error: 'Empty prompt' }));
						return;
					}
					console.log(`[Prompt] ${prompt.slice(0, 80)}`);
					const result = await client.prompt(prompt.trim());
					res.writeHead(200, { 'Content-Type': 'application/json' });
					res.end(JSON.stringify({ ok: true, stopReason: result.result?.stopReason }));
				} catch (err) {
					console.error('[Error]', err.message);
					res.writeHead(500, { 'Content-Type': 'application/json' });
					res.end(JSON.stringify({ ok: false, error: err.message }));
				}
			});
			return;
		}

		// Serve UI
		if (req.url === '/' || req.url === '/index.html') {
			res.writeHead(200, { 'Content-Type': 'text/html' });
			res.end(fs.readFileSync(path.join(__dirname, 'voice-ui.html'), 'utf8'));
			return;
		}

		res.writeHead(404);
		res.end('Not found');
	});

	server.listen(PORT, () => {
		console.log(`[Voice Swarm] http://localhost:${PORT}`);
	});
}

startServer().catch(err => { console.error('Fatal:', err); process.exit(1); });
