# Python: Claude CLI to ACP Migration

## Before (Claude CLI subprocess)

```python
def run_claude(prompt: str, cwd: str) -> str:
    result = subprocess.run(
        ["claude", "-p", prompt, "--output-format", "text"],
        cwd=cwd, capture_output=True, text=True, timeout=120,
    )
    if result.returncode != 0:
        raise RuntimeError(f"claude failed: {result.stderr}")
    return result.stdout.strip()
```

## After (ACP Client)

Full client implementation: `assets/python/acp_client.py`

Usage:
```python
from acp_client import AcpClient, AcpEvent

client = AcpClient("/path/to/project")

def handle_event(ev: AcpEvent) -> None:
    match ev.type:
        case "thought":    print(f"  [thinking] {ev.text}", end="")
        case "text":       print(ev.text, end="")
        case "tool_start": print(f"\n> {ev.name}: {ev.title}")
        case "tool_done":  print(f"  [done] {ev.name}")
        case "usage":      print(f"  [context: {ev.pct}% | {ev.cost}]")
        case "done":       print("\n---")

client.on_event(handle_event)
client.start()
try:
    client.initialize()
    client.prompt("Read main.py and explain the architecture")
    client.prompt("Now add type hints to all functions")  # remembers context
finally:
    client.kill()
```

## Async Variant (asyncio)

Full async client available in `assets/python/acp_client.py` (see `AsyncAcpClient` class).

```python
async def main():
    client = AsyncAcpClient("/path/to/project")
    await client.start()
    await client.initialize()
    await client.prompt("Refactor main.py")
```

## Migration Mapping

| CLI Pattern | ACP Equivalent |
|-------------|---------------|
| `subprocess.run(["claude", "-p", prompt])` | `client.prompt(prompt)` |
| `cwd=cwd` in subprocess | `AcpClient(cwd)` |
| `result.stdout` (blocking, text) | `client.prompt()` + `on_event` for streaming |
| `timeout=120` | `client.cancel()` or `client.kill()` |
| Parse stdout manually | Typed `AcpEvent` dataclass via `on_event` |
| No memory between calls | Session persists -- call `prompt()` again |

## Python-Specific Notes

- Use `bufsize=1` (line-buffered) in `Popen` or call `flush()` after every `stdin.write()`
- `threading.Event` for request/response synchronization
- `threading.Lock` for thread-safe ID generation
- Python 3.10+ required for `match` statement in examples
- For asyncio: use `asyncio.create_subprocess_exec` and `await stdin.drain()` after writes
