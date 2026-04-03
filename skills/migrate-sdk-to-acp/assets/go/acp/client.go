// Package acp provides a Go client for the Agent Client Protocol (ACP).
// It manages stateful Claude Code agent sessions via JSON-RPC over stdin/stdout.
//
// Usage:
//
//	client := acp.NewClient("/path/to/project")
//	client.OnEvent(func(ev acp.Event) { fmt.Print(ev.Text) })
//	client.Start(ctx)
//	defer client.Kill()
//	client.Initialize()
//	client.Prompt("Read main.go and explain the architecture")
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// Event represents a parsed ACP notification for UI consumption.
type Event struct {
	Type       string `json:"type"`                  // start, thought, text, tool_start, tool_info, tool_done, usage, done
	Text       string `json:"text,omitempty"`         // For thought/text events
	Name       string `json:"name,omitempty"`         // Tool name
	Title      string `json:"title,omitempty"`        // Tool title (user-facing)
	Kind       string `json:"kind,omitempty"`         // Tool kind (think, execute, read, edit, search, fetch)
	ID         string `json:"id,omitempty"`           // Tool call ID
	Pct        int    `json:"pct,omitempty"`          // Context usage percentage
	Cost       string `json:"cost,omitempty"`         // Cost string (e.g. "$0.042")
	Prompt     string `json:"prompt,omitempty"`       // For start events
	StopReason string `json:"stopReason,omitempty"`   // For done events
}

// EventHandler receives parsed ACP events.
type EventHandler func(Event)

// Client manages a stateful ACP agent session.
type Client struct {
	cwd       string
	proc      *exec.Cmd
	stdin     *json.Encoder
	nextID    atomic.Int64
	sessionID string
	pending   sync.Map // id -> chan json.RawMessage
	onEvent   EventHandler
	cancel    context.CancelFunc
}

// NewClient creates an ACP client for the given working directory.
func NewClient(cwd string) *Client {
	return &Client{cwd: cwd}
}

// OnEvent registers an event handler for streaming notifications.
func (c *Client) OnEvent(handler EventHandler) {
	c.onEvent = handler
}

// Start spawns the ACP agent process.
func (c *Client) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	// Locate the ACP binary (assumes npm/bun install in project or globally)
	acpBin := "node_modules/.bin/claude-agent-acp"
	if _, err := os.Stat(acpBin); err != nil {
		acpBin = "claude-agent-acp"
	}

	c.proc = exec.CommandContext(ctx, "node", acpBin)
	c.proc.Dir = c.cwd

	// CRITICAL: strip env vars that conflict with OAuth
	env := os.Environ()
	filtered := env[:0]
	strip := map[string]bool{
		"ANTHROPIC_API_KEY":      true,
		"CLAUDECODE":             true,
		"CLAUDE_CODE_NEW_INIT":   true,
		"CLAUDE_CODE_ENTRYPOINT": true,
	}
	for _, e := range env {
		key := e[:strings.IndexByte(e, '=')]
		if !strip[key] {
			filtered = append(filtered, e)
		}
	}
	c.proc.Env = filtered

	stdinPipe, err := c.proc.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	c.stdin = json.NewEncoder(stdinPipe)

	stdoutPipe, err := c.proc.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := c.proc.Start(); err != nil {
		return fmt.Errorf("start acp: %w", err)
	}

	// Read ndjson from stdout in background
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large responses
		for scanner.Scan() {
			c.handleLine(scanner.Bytes())
		}
	}()

	return nil
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (c *Client) handleLine(line []byte) {
	var msg jsonRPCResponse
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}

	if msg.ID > 0 {
		if ch, ok := c.pending.LoadAndDelete(msg.ID); ok {
			ch.(chan json.RawMessage) <- line
		}
		return
	}

	if msg.Method != "" {
		c.handleNotification(msg.Params)
	}
}

func (c *Client) handleNotification(raw json.RawMessage) {
	if c.onEvent == nil {
		return
	}

	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
			Kind       string `json:"kind"`
			Status     string `json:"status"`
			Used       int    `json:"used"`
			Size       int    `json:"size"`
			Cost       struct {
				Amount float64 `json:"amount"`
			} `json:"cost"`
			Meta struct {
				ClaudeCode struct {
					ToolName string `json:"toolName"`
				} `json:"claudeCode"`
			} `json:"_meta"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}

	u := params.Update
	switch u.SessionUpdate {
	case "agent_thought_chunk":
		if u.Content.Text != "" {
			c.onEvent(Event{Type: "thought", Text: u.Content.Text})
		}
	case "agent_message_chunk":
		if u.Content.Type == "text" {
			c.onEvent(Event{Type: "text", Text: u.Content.Text})
		}
	case "tool_call":
		name := u.Meta.ClaudeCode.ToolName
		if name == "" {
			name = u.Title
		}
		c.onEvent(Event{Type: "tool_start", Name: name, Title: u.Title, Kind: u.Kind, ID: u.ToolCallID})
	case "tool_call_update":
		name := u.Meta.ClaudeCode.ToolName
		if u.Status == "completed" {
			c.onEvent(Event{Type: "tool_done", Name: name, ID: u.ToolCallID})
		} else if u.Title != "" {
			c.onEvent(Event{Type: "tool_info", Name: name, Title: u.Title, ID: u.ToolCallID})
		}
	case "usage_update":
		pct := 0
		if u.Size > 0 {
			pct = (u.Used * 100) / u.Size
		}
		cost := ""
		if u.Cost.Amount > 0 {
			cost = fmt.Sprintf("$%.3f", u.Cost.Amount)
		}
		c.onEvent(Event{Type: "usage", Pct: pct, Cost: cost})
	}
}

func (c *Client) send(method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan json.RawMessage, 1)
	c.pending.Store(id, ch)

	req := jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.stdin.Encode(req); err != nil {
		c.pending.Delete(id)
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	resp := <-ch
	return resp, nil
}

// Initialize performs the ACP handshake. Must be called before any other method.
func (c *Client) Initialize() error {
	_, err := c.send("initialize", map[string]interface{}{
		"protocolVersion":    1,
		"clientCapabilities": map[string]interface{}{"textFiles": true},
	})
	return err
}

// CreateSession starts a new agent session.
func (c *Client) CreateSession() error {
	resp, err := c.send("session/new", map[string]interface{}{
		"cwd":            c.cwd,
		"permissionMode": "bypassPermissions",
		"mcpServers":     []interface{}{},
	})
	if err != nil {
		return err
	}

	var result struct {
		Result struct {
			SessionID string `json:"sessionId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return err
	}
	c.sessionID = result.Result.SessionID
	return nil
}

// Prompt sends a prompt and blocks until the response completes.
// Streaming notifications are delivered via the OnEvent handler concurrently.
func (c *Client) Prompt(text string) error {
	if c.sessionID == "" {
		if err := c.CreateSession(); err != nil {
			return err
		}
	}

	if c.onEvent != nil {
		c.onEvent(Event{Type: "start", Prompt: text})
	}

	resp, err := c.send("session/prompt", map[string]interface{}{
		"sessionId": c.sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": text}},
	})
	if err != nil {
		return err
	}

	var result struct {
		Result struct {
			StopReason string `json:"stopReason"`
		} `json:"result"`
	}
	_ = json.Unmarshal(resp, &result)

	if c.onEvent != nil {
		reason := result.Result.StopReason
		if reason == "" {
			reason = "end_turn"
		}
		c.onEvent(Event{Type: "done", StopReason: reason})
	}
	return nil
}

// Cancel sends a cancel notification (keeps session alive).
func (c *Client) Cancel() error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "cancel",
		Params:  map[string]string{"sessionId": c.sessionID},
	}
	return c.stdin.Encode(req)
}

// Kill terminates the ACP process.
func (c *Client) Kill() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.proc != nil && c.proc.Process != nil {
		c.proc.Process.Kill()
	}
	c.sessionID = ""
}
