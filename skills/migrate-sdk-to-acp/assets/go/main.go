// Example: using the ACP client from Go for multi-turn agent conversations.
//
// Prerequisites:
//   npm install @agentclientprotocol/claude-agent-acp
//
// Run:
//   go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"example/acp"
)

func main() {
	cwd, _ := os.Getwd()
	client := acp.NewClient(cwd)

	// Stream events to terminal
	client.OnEvent(func(ev acp.Event) {
		switch ev.Type {
		case "thought":
			fmt.Printf("  [thinking] %s", ev.Text)
		case "text":
			fmt.Print(ev.Text)
		case "tool_start":
			fmt.Printf("\n> %s: %s\n", ev.Name, ev.Title)
		case "tool_done":
			fmt.Printf("  [done] %s\n", ev.Name)
		case "usage":
			fmt.Printf("  [context: %d%% | %s]\n", ev.Pct, ev.Cost)
		case "done":
			fmt.Println("\n---")
		}
	})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Kill()

	if err := client.Initialize(); err != nil {
		log.Fatal(err)
	}

	// Multi-turn conversation -- session remembers context
	if err := client.Prompt("Read main.go and explain the architecture"); err != nil {
		log.Fatal(err)
	}
	if err := client.Prompt("Now add error handling to the HTTP handlers"); err != nil {
		log.Fatal(err)
	}
}
