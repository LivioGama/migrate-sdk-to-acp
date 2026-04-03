# Go: Claude CLI to ACP Migration

## Before (Claude CLI subprocess)

```go
func RunClaude(ctx context.Context, prompt, cwd string) (string, error) {
    cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "text")
    cmd.Dir = cwd
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("claude: %w: %s", err, stderr.String())
    }
    return strings.TrimSpace(stdout.String()), nil
}
```

## After (ACP Client)

Full client implementation: `assets/go/acp_client.go`

Usage:
```go
client := acp.NewClient("/path/to/project")
client.OnEvent(func(ev acp.Event) {
    switch ev.Type {
    case "thought":    fmt.Printf("  [thinking] %s", ev.Text)
    case "text":       fmt.Print(ev.Text)
    case "tool_start": fmt.Printf("\n> %s: %s\n", ev.Name, ev.Title)
    case "tool_done":  fmt.Printf("  [done] %s\n", ev.Name)
    case "usage":      fmt.Printf("  [context: %d%% | %s]\n", ev.Pct, ev.Cost)
    case "done":       fmt.Println("\n---")
    }
})

ctx := context.Background()
client.Start(ctx)
defer client.Kill()
client.Initialize()

// Multi-turn -- session remembers context
client.Prompt("Read main.go and explain the architecture")
client.Prompt("Now add error handling to the HTTP handlers")
```

## Migration Mapping

| CLI Pattern | ACP Equivalent |
|-------------|---------------|
| `exec.Command("claude", "-p", prompt)` | `client.Prompt(prompt)` |
| `cmd.Dir = cwd` | `NewClient(cwd)` |
| `cmd.Output()` (blocking, text) | `client.Prompt()` + `OnEvent` for streaming |
| `ctx.Done()` for timeout | `client.Cancel()` or `client.Kill()` |
| Parse stdout text manually | Typed `Event` structs via `OnEvent` |
| No memory between calls | Session persists -- call `Prompt()` again |

## Go-Specific Notes

- Use a 1MB scanner buffer: `scanner.Buffer(make([]byte, 1024*1024), 1024*1024)` -- tool results can exceed the default 64KB
- `sync.Map` for pending requests (concurrent goroutine access)
- `atomic.Int64` for request ID generation
- Strip env vars with `strings.IndexByte(e, '=')` before spawning
