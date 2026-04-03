# ACP Visualizer Template

A self-contained SSE server + dark-theme web UI for streaming ACP agent events to a browser in real time.

## Files

- `assets/visualizer/voice-server.mjs` -- Node.js HTTP server with ACP client and SSE broadcasting
- `assets/visualizer/voice-ui.html` -- Dark-theme UI with voice input, markdown rendering, tool call visualization

## Setup

1. Install ACP: `npm install @agentclientprotocol/claude-agent-acp`
2. Edit `voice-server.mjs` to set `CWD` to the directory the agent works in
3. Run: `node voice-server.mjs`
4. Open `http://localhost:7444` in Chrome (Web Speech API requires Chrome)

## Architecture

```
Browser (voice-ui.html)
  |-- Web Speech API --> POST /prompt
  |-- EventSource /stream <-- SSE events

Server (voice-server.mjs)
  |-- AcpClient --> spawn claude-agent-acp
  |-- JSON-RPC stdin/stdout
  |-- Parse notifications --> broadcast SSE
```

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Serves the UI HTML |
| GET | `/stream` | SSE event stream |
| POST | `/prompt` | Send prompt (JSON body: `{"prompt": "..."}`) |

## Adapting for Go/Python

The UI (`voice-ui.html`) is pure HTML/JS with SSE -- it works with any backend that serves:
- `GET /stream` returning `text/event-stream` with `data: {"type":"..."}` events
- `POST /prompt` accepting `{"prompt": "..."}` JSON

Build the same two endpoints in Go (`net/http`) or Python (`http.server` / FastAPI) and serve the same HTML file.
