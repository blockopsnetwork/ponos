package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	mcpHTTPTimeout = 30 * time.Second
)

type MCPHTTPClient struct {
	serverURL string
	client    *http.Client
	logger    *slog.Logger
	sessionID string
	botName   string
}

type MCPHTTPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type MCPHTTPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPHTTPError `json:"error,omitempty"`
}

type MCPHTTPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type MCPToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func NewMCPHTTPClient(serverURL, token, appID, installID, pemKey, botName string, logger *slog.Logger) *MCPHTTPClient {
	if serverURL == "" {
		serverURL = "http://localhost:3001"
	}
	client := &MCPHTTPClient{
		serverURL: serverURL,
		botName:   botName,
		client: &http.Client{
			Timeout: mcpHTTPTimeout,
		},
		logger: logger,
	}
		
	return client
}

func (c *MCPHTTPClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	if c.sessionID == "" {
		if err := c.initializeSession(); err != nil {
			return nil, fmt.Errorf("failed to initialize session: %v", err)
		}
	}

	request := MCPHTTPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: MCPToolCallParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.serverURL+"/messages", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.URL.RawQuery = "sessionId=" + c.sessionID

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return c.parseMCPResponse(respBody)
}

func (c *MCPHTTPClient) GetFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	args := map[string]interface{}{
		"owner": owner,
		"repo":  repo,
		"path":  path,
	}

	result, err := c.CallTool(ctx, "get_file_contents", args)
	if err != nil {
		return "", fmt.Errorf("failed to get file contents: %v", err)
	}

	if contentArray, ok := result["content"].([]interface{}); ok {
		for _, item := range contentArray {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if textData, ok := itemMap["text"].(string); ok {
					var githubResp map[string]interface{}
					if err := json.Unmarshal([]byte(textData), &githubResp); err == nil {
						if content, ok := githubResp["content"].(string); ok {
							return content, nil
						}
					}
					return textData, nil
				}
			}
		}
	}

	return "", fmt.Errorf("content not found in response")
}

func (c *MCPHTTPClient) CreateBranch(ctx context.Context, owner, repo, branchName string) error {
	args := map[string]interface{}{
		"owner":  owner,
		"repo":   repo,
		"branch": branchName,
		"from_branch": "main",
	}

	_, err := c.CallTool(ctx, "create_branch", args)
	if err != nil {
		return fmt.Errorf("failed to create branch: %v", err)
	}

	return nil
}

func (c *MCPHTTPClient) CreateCommit(ctx context.Context, owner, repo, branch, message string, files []FileUpdate) (string, error) {
	mcpFiles := make([]map[string]interface{}, len(files))
	for i, file := range files {
		mcpFiles[i] = map[string]interface{}{
			"path":    file.Path,
			"content": file.Content,
		}
	}

	args := map[string]interface{}{
		"owner":   owner,
		"repo":    repo,
		"branch":  branch,
		"message": message,
		"files":   mcpFiles,
	}

	result, err := c.CallTool(ctx, "push_files", args)
	if err != nil {
		return "", fmt.Errorf("failed to push files: %v", err)
	}

	if contentArray, ok := result["content"].([]interface{}); ok && len(contentArray) > 0 {
		if firstItem, ok := contentArray[0].(map[string]interface{}); ok {
			if textData, ok := firstItem["text"].(string); ok {
				var gitResponse map[string]interface{}
				if json.Unmarshal([]byte(textData), &gitResponse) == nil {
					if object, ok := gitResponse["object"].(map[string]interface{}); ok {
						if sha, ok := object["sha"].(string); ok {
							return sha, nil
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("commit SHA not found in response")
}

func (c *MCPHTTPClient) CreatePullRequest(ctx context.Context, owner, repo, head, base, title, body string) (string, error) {
	args := map[string]interface{}{
		"owner": owner,
		"repo":  repo,
		"head":  head,
		"base":  base,
		"title": title,
		"body":  body,
	}

	result, err := c.CallTool(ctx, "create_pull_request", args)
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %v", err)
	}

	if contentArray, ok := result["content"].([]interface{}); ok && len(contentArray) > 0 {
		if firstItem, ok := contentArray[0].(map[string]interface{}); ok {
			if textData, ok := firstItem["text"].(string); ok {
				var prResponse map[string]interface{}
				if json.Unmarshal([]byte(textData), &prResponse) == nil {
					if url, ok := prResponse["html_url"].(string); ok {
						return url, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("PR URL not found in response")
}

func (c *MCPHTTPClient) UpdateFile(ctx context.Context, owner, repo, path, content, message, branch string) error {
	args := map[string]interface{}{
		"owner":   owner,
		"repo":    repo,
		"path":    path,
		"content": content,
		"message": message,
		"branch":  branch,
	}

	_, err := c.CallTool(ctx, "create_or_update_file", args)
	if err != nil {
		return fmt.Errorf("failed to update file: %v", err)
	}

	return nil
}

func (c *MCPHTTPClient) parseMCPResponse(respBody []byte) (map[string]interface{}, error) {
	var mcpResp MCPHTTPResponse
	if err := json.Unmarshal(respBody, &mcpResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	result, ok := mcpResp.Result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result type")
	}

	return result, nil
}

func (c *MCPHTTPClient) initializeSession() error {
	req, err := http.NewRequest("GET", c.serverURL+"/sse", nil)
	if err != nil {
		return fmt.Errorf("failed to create SSE request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to SSE endpoint: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 1024)
	n, err := resp.Body.Read(buf)
	if err != nil && n == 0 {
		return fmt.Errorf("failed to read SSE response: %v", err)
	}

	response := string(buf[:n])
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if strings.Contains(data, "sessionId=") {
				parts := strings.Split(data, "sessionId=")
				if len(parts) > 1 {
					c.sessionID = parts[1]
					c.logger.Info("MCP session initialized", "sessionID", c.sessionID)
					return nil
				}
			}
		}
	}

	return fmt.Errorf("failed to extract sessionId from SSE response: %s", response)
}
