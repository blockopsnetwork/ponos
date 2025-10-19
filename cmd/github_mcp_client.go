package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	mcpProtocolVersion = "2024-11-05"
	jwtMaxDuration     = 10 * time.Minute
	tokenCacheDuration = 1 * time.Hour
	tokenRefreshBuffer = 5 * time.Minute
	githubAPIURL       = "https://api.github.com"
)

type GitHubMCPClient struct {
	serverURL string
	token     string
	client    *http.Client
	logger    *slog.Logger
	sessionID string
	appID     string
	installID string
	pemKey    string
	botName   string

	clientName  string
	userAgent   string
	cachedToken string
	tokenExpiry time.Time
}

type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func NewGitHubMCPClient(serverURL, token, appID, installID, pemKey, botName string, logger *slog.Logger) *GitHubMCPClient {
	clientName := "ponos"
	if botName != "" {
		clientName = botName
	}

	userAgent := clientName + "/1.0"

	client := &GitHubMCPClient{
		serverURL:  serverURL,
		token:      token,
		client:     &http.Client{},
		logger:     logger,
		appID:      appID,
		installID:  installID,
		pemKey:     pemKey,
		botName:    botName,
		clientName: clientName,
		userAgent:  userAgent,
	}

	if err := client.initialize(); err != nil {
		logger.Error("failed to initialize MCP session", "error", err)
	}

	return client
}

func (g *GitHubMCPClient) initialize() error {
	request := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    g.clientName,
				"version": "1.0.0",
			},
		},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal initialize request: %v", err)
	}

	httpReq, err := g.createMCPRequest(nil, reqBody)
	if err != nil {
		return err
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send initialize request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("initialize failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		g.sessionID = sessionID
		g.logger.Info("MCP session initialized", "sessionID", sessionID)
	}

	g.logger.Info("MCP client initialized successfully")
	return nil
}

func (g *GitHubMCPClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	request := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: ToolCallParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	httpReq, err := g.createMCPRequest(ctx, reqBody)
	if err != nil {
		return nil, err
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

	if resp.StatusCode == 400 && string(respBody) == "Invalid session ID\n" {
		g.logger.Info("session expired, reinitializing...")
		if err := g.initialize(); err != nil {
			return nil, fmt.Errorf("failed to reinitialize session: %v", err)
		}
		return g.CallTool(ctx, toolName, args)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return g.parseMCPResponse(respBody)
}

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

	if contentArray, ok := result["content"].([]interface{}); ok {
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

	return "", fmt.Errorf("content not found in response")
}

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

	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if firstItem, ok := content[0].(map[string]interface{}); ok {
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

	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if firstItem, ok := content[0].(map[string]interface{}); ok {
			if textData, ok := firstItem["text"].(string); ok {
				var prResponse map[string]interface{}
				if json.Unmarshal([]byte(textData), &prResponse) == nil {
					if url, ok := prResponse["url"].(string); ok {
						return url, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("PR URL not found in response")
}

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

func (g *GitHubMCPClient) createMCPRequest(ctx context.Context, body []byte) (*http.Request, error) {
	var req *http.Request
	var err error

	if ctx != nil {
		req, err = http.NewRequestWithContext(ctx, "POST", g.serverURL, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest("POST", g.serverURL, bytes.NewReader(body))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", g.userAgent)

	accessToken, err := g.getAccessToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	if g.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", g.sessionID)
	}

	return req, nil
}

func (g *GitHubMCPClient) parseMCPResponse(respBody []byte) (map[string]interface{}, error) {
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

	if isError, ok := result["isError"].(bool); ok && isError {
		if errorText := g.extractErrorText(result); errorText != "" {
			return nil, fmt.Errorf("MCP tool error: %s", errorText)
		}
		return nil, fmt.Errorf("MCP tool returned error")
	}

	return result, nil
}

func (g *GitHubMCPClient) extractErrorText(result map[string]interface{}) string {
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if firstItem, ok := content[0].(map[string]interface{}); ok {
			if errorText, ok := firstItem["text"].(string); ok {
				return errorText
			}
		}
	}
	return ""
}

func (g *GitHubMCPClient) getAccessToken() (string, error) {
	if g.token != "" {
		return g.token, nil
	}

	if g.hasGitHubAppCredentials() {
		return g.getCachedOrGenerateToken()
	}

	return "", fmt.Errorf("no authentication credentials provided")
}

func (g *GitHubMCPClient) hasGitHubAppCredentials() bool {
	return g.appID != "" && g.installID != "" && g.pemKey != ""
}

func (g *GitHubMCPClient) getCachedOrGenerateToken() (string, error) {
	if g.cachedToken != "" && time.Now().Before(g.tokenExpiry.Add(-tokenRefreshBuffer)) {
		return g.cachedToken, nil
	}

	token, err := g.generateInstallationToken()
	if err != nil {
		return "", err
	}

	g.cachedToken = token
	g.tokenExpiry = time.Now().Add(tokenCacheDuration)

	return token, nil
}

func (g *GitHubMCPClient) generateInstallationToken() (string, error) {
	var pemKey string

	if strings.HasPrefix(g.pemKey, "/") || strings.HasPrefix(g.pemKey, "./") || strings.HasSuffix(g.pemKey, ".pem") {
		pemBytes, err := os.ReadFile(g.pemKey)
		if err != nil {
			return "", fmt.Errorf("failed to read PEM key file %s: %v", g.pemKey, err)
		}
		pemKey = string(pemBytes)
	} else {
		pemKey = strings.ReplaceAll(g.pemKey, "\\n", "\n")
	}

	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return "", fmt.Errorf("failed to parse PEM block containing the key - check that PEM key starts with -----BEGIN and ends with -----END")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsedKey, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return "", fmt.Errorf("failed to parse RSA private key (tried PKCS1 and PKCS8): PKCS1 error: %v, PKCS8 error: %v", err, err2)
		}
		var ok bool
		privateKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("parsed key is not an RSA private key")
		}
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(jwtMaxDuration).Unix(),
		"iss": g.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	jwtToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %v", err)
	}

	return g.exchangeJWTForAccessToken(jwtToken)
}

func (g *GitHubMCPClient) exchangeJWTForAccessToken(jwtToken string) (string, error) {
	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", githubAPIURL, g.installID)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", g.userAgent)

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
