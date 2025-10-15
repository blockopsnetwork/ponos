package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
)

func main() {
	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	
	// Check if GITHUB_TOKEN is set
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN environment variable is required")
	}
	
	// Create remote MCP client
	client := NewRemoteMCPClient(token, "test-bot", logger)
	
	// Initialize the client
	logger.Info("Initializing remote MCP client...")
	if err := client.Initialize(context.Background()); err != nil {
		log.Fatalf("Failed to initialize remote MCP client: %v", err)
	}
	
	// Test getting file contents from a public repository
	logger.Info("Testing file content retrieval...")
	content, err := client.GetFileContent(context.Background(), "github", "github-mcp-server", "README.md")
	if err != nil {
		log.Fatalf("Failed to get file contents: %v", err)
	}
	
	// Show first 500 characters of the README
	if len(content) > 500 {
		content = content[:500] + "..."
	}
	
	fmt.Printf("✅ Successfully retrieved README.md from github/github-mcp-server:\n\n%s\n", content)
	logger.Info("Remote MCP client test completed successfully!")
}