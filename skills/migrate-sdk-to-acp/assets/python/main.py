"""Example: using the ACP client from Python for multi-turn agent conversations.

Prerequisites:
    npm install @agentclientprotocol/claude-agent-acp

Run:
    python main.py
"""

import os

from acp_client import AcpClient, AcpEvent


def handle_event(ev: AcpEvent) -> None:
    match ev.type:
        case "thought":
            print(f"  [thinking] {ev.text}", end="")
        case "text":
            print(ev.text, end="")
        case "tool_start":
            print(f"\n> {ev.name}: {ev.title}")
        case "tool_done":
            print(f"  [done] {ev.name}")
        case "usage":
            print(f"  [context: {ev.pct}% | {ev.cost}]")
        case "done":
            print("\n---")


def main():
    cwd = os.getcwd()
    client = AcpClient(cwd)
    client.on_event(handle_event)
    client.start()

    try:
        client.initialize()

        # Multi-turn conversation -- session remembers context
        client.prompt("Read main.py and explain the architecture")
        client.prompt("Now add type hints to all functions")
        client.prompt("Write pytest tests for what you just changed")
    finally:
        client.kill()


if __name__ == "__main__":
    main()
