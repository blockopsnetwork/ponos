package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type GitHubMCPClient struct {
	serverURL   string
	token       string
	client      *http.Client
	logger      *slog.Logger
	sessionID   string
	appID       string
	installID   string
	pemKey      string
	botName     string
}

// MCPRequest represents a JSON-RPC request to the MCP server
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// MCPResponse represents a JSON-RPC response from the MCP server
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents an error in MCP response
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ToolCallParams represents parameters for calling a tool
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// NewGitHubMCPClient creates a new GitHub MCP client
func NewGitHubMCPClient(serverURL, token, appID, installID, pemKey, botName string, logger *slog.Logger) *GitHubMCPClient {
	client := &GitHubMCPClient{
		serverURL: serverURL,
		token:     token,
		client:    &http.Client{},
		logger:    logger,
		appID:     appID,
		installID: installID,
		pemKey:    pemKey,
		botName:   botName,
	}
	
	// Initialize the MCP session
	if err := client.initialize(); err != nil {
		logger.Error("failed to initialize MCP session", "error", err)
	}
	
	return client
}

// initialize establishes an MCP session with the server
func (g *GitHubMCPClient) initialize() error {
	request := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    g.getBotClientName(),
				"version": "1.0.0",
			},
		},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal initialize request: %v", err)
	}

	httpReq, err := http.NewRequest("POST", g.serverURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create initialize request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	
	// Get access token (either direct token or generated from GitHub App)
	accessToken, err := g.getAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get access token: %v", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	
	httpReq.Header.Set("User-Agent", g.getUserAgent())

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send initialize request: %v", err)
	}
	defer resp.Body.Close()

	// Extract session ID from response headers
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		g.sessionID = sessionID
		g.logger.Info("MCP session initialized", "sessionID", sessionID)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read initialize response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("initialize failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	g.logger.Info("MCP client initialized successfully")
	return nil
}

// CallTool calls a tool on the remote MCP server
func (g *GitHubMCPClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	params := ToolCallParams{
		Name:      toolName,
		Arguments: args,
	}

	request := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}


	httpReq, err := http.NewRequestWithContext(ctx, "POST", g.serverURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	
	// Get access token (either direct token or generated from GitHub App)
	accessToken, err := g.getAccessToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %v", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	
	httpReq.Header.Set("User-Agent", g.getUserAgent())
	
	// Include session ID if we have one
	if g.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", g.sessionID)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}


	// Handle session expiry - retry with new session
	if resp.StatusCode == 400 && string(respBody) == "Invalid session ID\n" {
		g.logger.Info("session expired, reinitializing...")
		if err := g.initialize(); err != nil {
			return nil, fmt.Errorf("failed to reinitialize session: %v", err)
		}
		// Retry the request with new session
		return g.CallTool(ctx, toolName, args)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mcpResp MCPResponse
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

	// Check if the result indicates an error
	if isError, ok := result["isError"].(bool); ok && isError {
		if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
			if firstItem, ok := content[0].(map[string]interface{}); ok {
				if errorText, ok := firstItem["text"].(string); ok {
					return nil, fmt.Errorf("MCP tool error: %s", errorText)
				}
			}
		}
		return nil, fmt.Errorf("MCP tool returned error")
	}

	return result, nil
}

// GetFileContent retrieves file content from a GitHub repository
func (g *GitHubMCPClient) GetFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	args := map[string]interface{}{
		"owner": owner,
		"repo":  repo,
		"path":  path,
	}

	result, err := g.CallTool(ctx, "get_file_contents", args)
	if err != nil {
		return "", fmt.Errorf("failed to get file contents: %v", err)
	}


	// GitHub MCP returns content as an array with multiple items
	if contentArray, ok := result["content"].([]interface{}); ok {
		// Look for the resource item in the content array
		for _, item := range contentArray {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemType, ok := itemMap["type"].(string); ok && itemType == "resource" {
					if resource, ok := itemMap["resource"].(map[string]interface{}); ok {
						if text, ok := resource["text"].(string); ok {
							return text, nil
						}
					}
				}
			}
		}
	}
	
	// Fallback to direct content field
	if content, ok := result["content"].(string); ok {
		return content, nil
	}

	return "", fmt.Errorf("content not found in response, available fields: %v", getKeys(result))
}

// Helper function to get keys from a map for debugging
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// CreateBranch creates a new branch from the main branch
func (g *GitHubMCPClient) CreateBranch(ctx context.Context, owner, repo, branchName string) error {
	args := map[string]interface{}{
		"owner":  owner,
		"repo":   repo,
		"branch": branchName,
		"source": "main",
	}

	_, err := g.CallTool(ctx, "create_branch", args)
	if err != nil {
		return fmt.Errorf("failed to create branch: %v", err)
	}

	return nil
}


// CreateCommit creates a commit with multiple file changes using push_files
func (g *GitHubMCPClient) CreateCommit(ctx context.Context, owner, repo, branch, message string, files []FileUpdate) (string, error) {
	args := map[string]interface{}{
		"owner":   owner,
		"repo":    repo,
		"branch":  branch,
		"message": message,
		"files":   files,
	}

	result, err := g.CallTool(ctx, "push_files", args)
	if err != nil {
		return "", fmt.Errorf("failed to push files: %v", err)
	}

	// Extract commit SHA from the result - try multiple possible locations
	if sha, ok := result["sha"].(string); ok {
		return sha, nil
	}
	if commit, ok := result["commit"].(map[string]interface{}); ok {
		if sha, ok := commit["sha"].(string); ok {
			return sha, nil
		}
	}
	if head, ok := result["head"].(map[string]interface{}); ok {
		if sha, ok := head["sha"].(string); ok {
			return sha, nil
		}
	}
	
	// Handle GitHub MCP response format: content[0].text contains JSON string
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if firstItem, ok := content[0].(map[string]interface{}); ok {
			if textData, ok := firstItem["text"].(string); ok {
				// Parse the JSON string inside the text field
				var gitResponse map[string]interface{}
				if json.Unmarshal([]byte(textData), &gitResponse) == nil {
					if object, ok := gitResponse["object"].(map[string]interface{}); ok {
						if sha, ok := object["sha"].(string); ok {
							return sha, nil
						}
					}
				}
			}
			// Fallback to direct SHA field
			if sha, ok := firstItem["sha"].(string); ok {
				return sha, nil
			}
		}
	}

	return "unknown-sha", nil
}

// CreatePullRequest creates a pull request
func (g *GitHubMCPClient) CreatePullRequest(ctx context.Context, owner, repo, head, base, title, body string) (string, error) {
	args := map[string]interface{}{
		"owner": owner,
		"repo":  repo,
		"head":  head,
		"base":  base,
		"title": title,
		"body":  body,
	}

	result, err := g.CallTool(ctx, "create_pull_request", args)
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %v", err)
	}

	// Extract PR URL from the result
	if url, ok := result["html_url"].(string); ok {
		return url, nil
	}
	if pr, ok := result["pull_request"].(map[string]interface{}); ok {
		if url, ok := pr["html_url"].(string); ok {
			return url, nil
		}
	}
	
	// Handle GitHub MCP response format: content[0].text contains JSON string
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if firstItem, ok := content[0].(map[string]interface{}); ok {
			if textData, ok := firstItem["text"].(string); ok {
				// Parse the JSON string inside the text field
				var prResponse map[string]interface{}
				if json.Unmarshal([]byte(textData), &prResponse) == nil {
					// Try both html_url and url fields
					if url, ok := prResponse["html_url"].(string); ok {
						return url, nil
					}
					if url, ok := prResponse["url"].(string); ok {
						return url, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("PR URL not found in response")
}

// UpdateFile updates a single file in a repository
func (g *GitHubMCPClient) UpdateFile(ctx context.Context, owner, repo, path, content, message, branch string) error {
	args := map[string]interface{}{
		"owner":   owner,
		"repo":    repo,
		"path":    path,
		"content": content,
		"message": message,
		"branch":  branch,
	}

	_, err := g.CallTool(ctx, "create_or_update_file", args)
	if err != nil {
		return fmt.Errorf("failed to update file: %v", err)
	}

	return nil
}

// getBotClientName returns the appropriate client name for MCP initialization
func (g *GitHubMCPClient) getBotClientName() string {
	if g.botName != "" {
		return g.botName
	}
	return "ponos"
}

// getUserAgent returns the appropriate User-Agent header
func (g *GitHubMCPClient) getUserAgent() string {
	if g.botName != "" {
		return g.botName + "/1.0"
	}
	return "ponos/1.0"
}

// getAccessToken returns an access token, either from GITHUB_TOKEN or by generating one from GitHub App credentials
func (g *GitHubMCPClient) getAccessToken() (string, error) {
	// If we have a direct token, use it
	if g.token != "" {
		return g.token, nil
	}
	
	// If we have GitHub App credentials, generate an installation access token
	if g.appID != "" && g.installID != "" && g.pemKey != "" {
		return g.generateInstallationToken()
	}
	
	return "", fmt.Errorf("no authentication credentials provided")
}

// generateInstallationToken creates a GitHub App installation access token
func (g *GitHubMCPClient) generateInstallationToken() (string, error) {
	// Parse the PEM key
	block, _ := pem.Decode([]byte(g.pemKey))
	if block == nil {
		return "", fmt.Errorf("failed to parse PEM block containing the key")
	}
	
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse RSA private key: %v", err)
	}
	
	// Create JWT claims
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(time.Minute * 10).Unix(), // GitHub requires max 10 minutes
		"iss": g.appID,
	}
	
	// Create and sign the JWT
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	jwtToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %v", err)
	}
	
	// Exchange JWT for installation access token
	return g.exchangeJWTForAccessToken(jwtToken)
}

// exchangeJWTForAccessToken exchanges a JWT for an installation access token
func (g *GitHubMCPClient) exchangeJWTForAccessToken(jwtToken string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", g.installID)
	
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}
	
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", g.getUserAgent())
	
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to exchange JWT: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get access token (status %d): %s", resp.StatusCode, string(body))
	}
	
	var tokenResp struct {
		Token string `json:"token"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %v", err)
	}
	
	return tokenResp.Token, nil
}
