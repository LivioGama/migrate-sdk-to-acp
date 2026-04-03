"""ACP client for Python -- stateful Claude Code agent sessions.

Usage:
    from acp_client import AcpClient, AcpEvent

    client = AcpClient("/path/to/project")
    client.on_event(lambda ev: print(ev.text, end=""))
    client.start()
    client.initialize()
    client.prompt("Read main.py and explain the architecture")
    client.prompt("Now add type hints")  # session remembers context
    client.kill()
"""

import json
import os
import subprocess
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Optional


@dataclass
class AcpEvent:
    """Parsed ACP notification for UI consumption."""

    type: str  # start, thought, text, tool_start, tool_info, tool_done, usage, done
    text: str = ""
    name: str = ""
    title: str = ""
    kind: str = ""
    id: str = ""
    pct: int = 0
    cost: str = ""
    prompt: str = ""
    stop_reason: str = ""


EventHandler = Callable[[AcpEvent], None]


class AcpClient:
    """Manages a stateful ACP agent session via JSON-RPC over stdin/stdout."""

    def __init__(self, cwd: str):
        self.cwd = cwd
        self._proc: Optional[subprocess.Popen] = None
        self._pending: dict[int, threading.Event] = {}
        self._results: dict[int, dict] = {}
        self._next_id = 0
        self._lock = threading.Lock()
        self.session_id: Optional[str] = None
        self._on_event: Optional[EventHandler] = None

    def on_event(self, handler: EventHandler) -> None:
        """Register an event handler for streaming notifications."""
        self._on_event = handler

    def start(self) -> None:
        """Spawn the ACP agent process."""
        acp_bin = Path(self.cwd) / "node_modules" / ".bin" / "claude-agent-acp"
        if not acp_bin.exists():
            acp_bin = "claude-agent-acp"  # Fallback to PATH

        # CRITICAL: strip env vars that conflict with OAuth
        env = {
            k: v
            for k, v in os.environ.items()
            if k
            not in {
                "ANTHROPIC_API_KEY",
                "CLAUDECODE",
                "CLAUDE_CODE_NEW_INIT",
                "CLAUDE_CODE_ENTRYPOINT",
            }
        }

        self._proc = subprocess.Popen(
            ["node", str(acp_bin)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=self.cwd,
            env=env,
            text=True,
            bufsize=1,  # Line-buffered
        )

        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()

    def _read_loop(self) -> None:
        """Read ndjson lines from stdout and dispatch."""
        assert self._proc and self._proc.stdout
        for line in self._proc.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue

            msg_id = msg.get("id")
            if msg_id and msg_id in self._pending:
                self._results[msg_id] = msg
                self._pending[msg_id].set()
            elif msg.get("method"):
                self._handle_notification(msg)

    def _handle_notification(self, msg: dict) -> None:
        """Parse ACP notification into typed AcpEvent."""
        if not self._on_event:
            return

        update = msg.get("params", {}).get("update", {})
        kind = update.get("sessionUpdate", "")
        content = update.get("content", {})
        meta_tool = (
            update.get("_meta", {}).get("claudeCode", {}).get("toolName", "")
        )

        event: Optional[AcpEvent] = None

        if kind == "agent_thought_chunk" and content.get("text"):
            event = AcpEvent(type="thought", text=content["text"])

        elif kind == "agent_message_chunk" and content.get("type") == "text":
            event = AcpEvent(type="text", text=content.get("text", ""))

        elif kind == "tool_call":
            name = meta_tool or update.get("title", "tool")
            event = AcpEvent(
                type="tool_start",
                name=name,
                title=update.get("title", ""),
                kind=update.get("kind", ""),
                id=update.get("toolCallId", ""),
            )

        elif kind == "tool_call_update":
            name = meta_tool
            if update.get("status") == "completed":
                event = AcpEvent(
                    type="tool_done", name=name, id=update.get("toolCallId", "")
                )
            elif update.get("title"):
                event = AcpEvent(
                    type="tool_info",
                    name=name,
                    title=update["title"],
                    id=update.get("toolCallId", ""),
                )

        elif kind == "usage_update":
            size = update.get("size", 0)
            pct = round((update.get("used", 0) / size) * 100) if size else 0
            amount = update.get("cost", {}).get("amount", 0)
            cost = f"${amount:.3f}" if amount else ""
            event = AcpEvent(type="usage", pct=pct, cost=cost)

        if event:
            self._on_event(event)

    def _send(self, method: str, params: dict | None = None) -> dict:
        """Send a JSON-RPC request and wait for the response."""
        assert self._proc and self._proc.stdin

        with self._lock:
            self._next_id += 1
            msg_id = self._next_id

        event = threading.Event()
        self._pending[msg_id] = event

        request = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": msg_id,
                "method": method,
                "params": params or {},
            }
        )
        self._proc.stdin.write(request + "\n")
        self._proc.stdin.flush()

        if not event.wait(timeout=120):
            self._pending.pop(msg_id, None)
            raise TimeoutError(f"ACP request timed out: {method}")

        result = self._results.pop(msg_id, {})
        self._pending.pop(msg_id, None)
        return result

    def initialize(self) -> None:
        """Perform the ACP handshake. Must be called first."""
        self._send(
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {"textFiles": True},
            },
        )

    def create_session(self) -> None:
        """Start a new agent session."""
        resp = self._send(
            "session/new",
            {
                "cwd": self.cwd,
                "permissionMode": "bypassPermissions",
                "mcpServers": [],
            },
        )
        self.session_id = resp.get("result", {}).get("sessionId")

    def prompt(self, text: str) -> dict:
        """Send a prompt. Blocks until response. Events stream via on_event handler."""
        if not self.session_id:
            self.create_session()

        if self._on_event:
            self._on_event(AcpEvent(type="start", prompt=text))

        resp = self._send(
            "session/prompt",
            {
                "sessionId": self.session_id,
                "prompt": [{"type": "text", "text": text}],
            },
        )

        stop_reason = resp.get("result", {}).get("stopReason", "end_turn")
        if self._on_event:
            self._on_event(AcpEvent(type="done", stop_reason=stop_reason))

        return resp

    def cancel(self) -> None:
        """Send a cancel notification (keeps session alive)."""
        assert self._proc and self._proc.stdin
        request = json.dumps(
            {
                "jsonrpc": "2.0",
                "method": "cancel",
                "params": {"sessionId": self.session_id},
            }
        )
        self._proc.stdin.write(request + "\n")
        self._proc.stdin.flush()

    def kill(self) -> None:
        """Terminate the ACP process."""
        if self._proc:
            self._proc.kill()
            self._proc = None
        self.session_id = None


# ── Async variant ─────────────────────────────────────────────────────────────


class AsyncAcpClient:
    """Async ACP client using asyncio subprocess."""

    def __init__(self, cwd: str):
        self.cwd = cwd
        self._proc = None
        self._pending: dict[int, "asyncio.Future"] = {}
        self._next_id = 0
        self.session_id: Optional[str] = None

    async def start(self) -> None:
        import asyncio

        env = {
            k: v
            for k, v in os.environ.items()
            if k
            not in {
                "ANTHROPIC_API_KEY",
                "CLAUDECODE",
                "CLAUDE_CODE_NEW_INIT",
                "CLAUDE_CODE_ENTRYPOINT",
            }
        }
        acp_bin = Path(self.cwd) / "node_modules" / ".bin" / "claude-agent-acp"
        if not acp_bin.exists():
            acp_bin = "claude-agent-acp"
        self._proc = await asyncio.create_subprocess_exec(
            "node",
            str(acp_bin),
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=self.cwd,
            env=env,
        )
        asyncio.create_task(self._read_loop())

    async def _read_loop(self) -> None:
        while self._proc and self._proc.stdout:
            line = await self._proc.stdout.readline()
            if not line:
                break
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            msg_id = msg.get("id")
            if msg_id and msg_id in self._pending:
                self._pending.pop(msg_id).set_result(msg)

    async def _send(self, method: str, params: dict | None = None) -> dict:
        import asyncio

        self._next_id += 1
        msg_id = self._next_id
        fut = asyncio.get_event_loop().create_future()
        self._pending[msg_id] = fut
        request = json.dumps(
            {"jsonrpc": "2.0", "id": msg_id, "method": method, "params": params or {}}
        )
        self._proc.stdin.write((request + "\n").encode())
        await self._proc.stdin.drain()
        return await asyncio.wait_for(fut, timeout=120)

    async def initialize(self) -> None:
        await self._send(
            "initialize",
            {"protocolVersion": 1, "clientCapabilities": {"textFiles": True}},
        )

    async def create_session(self) -> None:
        resp = await self._send(
            "session/new",
            {
                "cwd": self.cwd,
                "permissionMode": "bypassPermissions",
                "mcpServers": [],
            },
        )
        self.session_id = resp.get("result", {}).get("sessionId")

    async def prompt(self, text: str) -> dict:
        if not self.session_id:
            await self.create_session()
        return await self._send(
            "session/prompt",
            {
                "sessionId": self.session_id,
                "prompt": [{"type": "text", "text": text}],
            },
        )

    async def kill(self) -> None:
        if self._proc:
            self._proc.kill()
            self._proc = None
        self.session_id = None
