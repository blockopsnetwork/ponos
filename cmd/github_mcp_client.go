package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/golang-jwt/jwt/v5"
)

const (
	mcpProtocolVersion    = "2024-11-05"
	jwtMaxDuration        = 10 * time.Minute
	tokenCacheDuration    = 1 * time.Hour
	tokenRefreshBuffer    = 5 * time.Minute
	githubAPIURL          = "https://api.github.com"
	defaultConnectTimeout = 10 * time.Second
	defaultRequestTimeout = 60 * time.Second
	keepAliveTimeout      = 30 * time.Second
	expectContinueTimeout = 5 * time.Second
	idleConnTimeout       = 90 * time.Second
	initializeTimeout     = 30 * time.Second
	defaultLocalhost      = "http://localhost:3001"
	jsonRPCVersion        = "2.0"
	clientVersion         = "1.0.0"
	gitHubConflictStatus  = "422"
	rateLimitRemaining    = "X-RateLimit-Remaining"
	rateLimitReset        = "X-RateLimit-Reset"
)

func BuildGitHubMCPClient(cfg *config.Config, logger *slog.Logger) *GitHubMCPClient {
	return NewGitHubMCPClient(
		cfg.GitHubMCPURL,
		cfg.GitHubToken,
		cfg.GitHubAppID,
		cfg.GitHubInstallID,
		cfg.GitHubPEMKey,
		cfg.GitHubBotName,
		logger,
	)
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
	baseURL, sseURL := normalizeServerURL(serverURL)

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   defaultConnectTimeout,
			KeepAlive: keepAliveTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   defaultConnectTimeout,
		ResponseHeaderTimeout: defaultRequestTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
		IdleConnTimeout:       idleConnTimeout,
		Proxy:                 http.ProxyFromEnvironment,
	}

	client := &GitHubMCPClient{
		baseURL:        baseURL,
		sseURL:         sseURL,
		token:          token,
		client:         &http.Client{Transport: transport},
		logger:         logger,
		appID:          appID,
		installID:      installID,
		pemKey:         pemKey,
		botName:        botName,
		clientName:     clientName,
		userAgent:      userAgent,
		pending:        make(map[int]chan mcpEnvelope),
		requestTimeout: defaultRequestTimeout,
		connectTimeout: defaultConnectTimeout,
	}

	if err := client.connectAndInitialize(); err != nil {
		logger.Error("failed to initialize MCP session", "error", err)
	}

	return client
}

func normalizeServerURL(serverURL string) (string, string) {
	trimmed := strings.TrimSpace(serverURL)
	if trimmed == "" {
		trimmed = defaultLocalhost
	}

	trimmed = strings.TrimRight(trimmed, "/")
	base := trimmed
	if strings.HasSuffix(trimmed, "/sse") {
		base = strings.TrimSuffix(trimmed, "/sse")
	} else {
		trimmed = trimmed + "/sse"
	}

	if base == "" {
		base = defaultLocalhost
	}

	return base, trimmed
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
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "reference already exists") ||
			strings.Contains(errMsg, "already exists") ||
			strings.Contains(errMsg, gitHubConflictStatus) {
			g.logger.Info("Branch already exists, continuing", "branch", branchName, "owner", owner, "repo", repo)
			return nil
		}
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

func (g *GitHubMCPClient) connectAndInitialize() error {
	if err := g.startEventStream(); err != nil {
		return err
	}
	if err := g.initializeSession(); err != nil {
		return err
	}
	g.logger.Info("MCP client initialized successfully", "session", g.sessionID)
	return nil
}

func (g *GitHubMCPClient) startEventStream() error {
	g.shutdownStream()

	endpointCh := make(chan string, 1)
	g.endpointCh = endpointCh

	ctx, cancel := context.WithCancel(context.Background())
	g.ctx = ctx
	g.cancel = cancel

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.sseURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create SSE request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", g.userAgent)

	token, err := g.getAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to open SSE stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return fmt.Errorf("SSE stream failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	g.sseBody = resp.Body
	go g.consumeSSE(resp.Body)

	select {
	case endpoint := <-endpointCh:
		resolved, err := g.resolveEndpoint(endpoint)
		if err != nil {
			return err
		}
		g.messagesURL = resolved
		if parsed, err := url.Parse(resolved); err == nil {
			g.sessionID = parsed.Query().Get("sessionId")
		}
		g.endpointCh = nil
		return nil
	case <-time.After(g.connectTimeout):
		return fmt.Errorf("timed out waiting for MCP endpoint")
	case <-ctx.Done():
		return fmt.Errorf("SSE context canceled before endpoint ready: %w", ctx.Err())
	}
}

func (g *GitHubMCPClient) resolveEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		return "", fmt.Errorf("received empty endpoint from MCP server")
	}

	decoded, err := url.QueryUnescape(endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to decode endpoint '%s': %w", endpoint, err)
	}

	baseURL := g.baseURL
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid MCP base URL %s: %w", baseURL, err)
	}

	rel, err := url.Parse(decoded)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint path %s: %w", decoded, err)
	}

	return base.ResolveReference(rel).String(), nil
}

func (g *GitHubMCPClient) initializeSession() error {
	ctx, cancel := context.WithTimeout(context.Background(), initializeTimeout)
	defer cancel()

	request := MCPRequest{
		JSONRPC: jsonRPCVersion,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    g.clientName,
				"version": clientVersion,
			},
		},
	}

	_, err := g.sendRequest(ctx, request)
	return err
}

func (g *GitHubMCPClient) ensureConnected() error {
	if g.messagesURL != "" {
		return nil
	}

	g.connectMu.Lock()
	defer g.connectMu.Unlock()

	if g.messagesURL != "" {
		return nil
	}

	return g.connectAndInitialize()
}

func (g *GitHubMCPClient) sendRequest(ctx context.Context, request MCPRequest) (mcpEnvelope, error) {
	if err := g.ensureConnected(); err != nil {
		return mcpEnvelope{}, err
	}

	if request.ID == 0 {
		request.ID = int(g.requestCounter.Add(1))
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return mcpEnvelope{}, fmt.Errorf("failed to marshal MCP request: %w", err)
	}

	respCh := g.registerPending(request.ID)

	if err := g.postMessage(payload); err != nil {
		g.removePending(request.ID)
		return mcpEnvelope{}, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, g.requestTimeout)
	defer cancel()

	select {
	case resp, ok := <-respCh:
		if !ok {
			return mcpEnvelope{}, fmt.Errorf("response channel closed for request %d", request.ID)
		}
		return resp, nil
	case <-waitCtx.Done():
		g.removePending(request.ID)
		return mcpEnvelope{}, waitCtx.Err()
	}
}

func (g *GitHubMCPClient) registerPending(id int) chan mcpEnvelope {
	ch := make(chan mcpEnvelope, 1)
	g.pendingMu.Lock()
	g.pending[id] = ch
	g.pendingMu.Unlock()
	return ch
}

func (g *GitHubMCPClient) removePending(id int) {
	g.pendingMu.Lock()
	if ch, ok := g.pending[id]; ok {
		delete(g.pending, id)
		close(ch)
	}
	g.pendingMu.Unlock()
}

func (g *GitHubMCPClient) postMessage(payload []byte) error {
	if g.messagesURL == "" {
		return fmt.Errorf("MCP message endpoint not ready")
	}

	req, err := http.NewRequest(http.MethodPost, g.messagesURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create POST request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", g.userAgent)

	token, err := g.getAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send MCP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		if rateErr := g.handleRateLimit(resp); rateErr != nil {
			return rateErr
		}
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

func (g *GitHubMCPClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	result, err := g.callToolOnce(ctx, toolName, args)
	if err == nil {
		return result, nil
	}

	g.logger.Warn("MCP tool call failed, attempting reconnect", "tool", toolName, "error", err)
	if err := g.reconnect(); err != nil {
		return nil, fmt.Errorf("failed to reconnect MCP client: %w", err)
	}

	return g.callToolOnce(ctx, toolName, args)
}

func (g *GitHubMCPClient) callToolOnce(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	request := MCPRequest{
		JSONRPC: jsonRPCVersion,
		Method:  "tools/call",
		Params: ToolCallParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	resp, err := g.sendRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	if result, ok := resp.Result.(map[string]interface{}); ok {
		return result, nil
	}

	if resp.Result == nil {
		return map[string]interface{}{}, nil
	}

	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("unexpected MCP result type %T", resp.Result)
	}

	var mapped map[string]interface{}
	if err := json.Unmarshal(data, &mapped); err != nil {
		return nil, fmt.Errorf("failed to decode MCP result: %w", err)
	}

	return mapped, nil
}

func (g *GitHubMCPClient) reconnect() error {
	g.connectMu.Lock()
	defer g.connectMu.Unlock()
	return g.connectAndInitialize()
}

func (g *GitHubMCPClient) shutdownStream() {
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	if g.sseBody != nil {
		g.sseBody.Close()
		g.sseBody = nil
	}
	g.messagesURL = ""
	g.sessionID = ""
}

func (g *GitHubMCPClient) consumeSSE(body io.ReadCloser) {
	reader := bufio.NewReader(body)
	var eventName string
	var dataLines []string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				g.logger.Error("error reading SSE stream", "error", err)
			}
			break
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(dataLines) > 0 || eventName != "" {
				data := strings.Join(dataLines, "\n")
				g.dispatchSSEEvent(eventName, data)
			}
			eventName = ""
			dataLines = dataLines[:0]
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[len("event:"):])
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(line[len("data:"):]))
		}
	}

	body.Close()
	g.failAllPending(fmt.Errorf("MCP SSE stream closed"))
	g.messagesURL = ""
}

func (g *GitHubMCPClient) dispatchSSEEvent(eventName, data string) {
	if eventName == "" {
		eventName = "message"
	}

	switch eventName {
	case "endpoint":
		if g.endpointCh != nil {
			select {
			case g.endpointCh <- data:
			default:
			}
		}
	case "message":
		g.handleIncomingMessage(data)
	default:
		g.logger.Debug("received unhandled SSE event", "event", eventName)
	}
}

func (g *GitHubMCPClient) handleIncomingMessage(data string) {
	var envelope mcpEnvelope
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		g.logger.Error("failed to decode MCP message", "error", err)
		return
	}

	if envelope.ID == nil {
		g.logger.Debug("received MCP notification", "method", envelope.Method)
		return
	}

	g.pendingMu.Lock()
	ch, ok := g.pending[*envelope.ID]
	if ok {
		delete(g.pending, *envelope.ID)
	}
	g.pendingMu.Unlock()

	if ok {
		ch <- envelope
		close(ch)
	}
}

func (g *GitHubMCPClient) failAllPending(err error) {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()

	for id, ch := range g.pending {
		delete(g.pending, id)
		idCopy := id
		ch <- mcpEnvelope{
			ID: &idCopy,
			Error: &MCPError{
				Code:    -1,
				Message: err.Error(),
			},
		}
		close(ch)
	}
}

func (g *GitHubMCPClient) handleRateLimit(resp *http.Response) error {
	if resp == nil {
		return nil
	}

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}

	if remaining := resp.Header.Get(rateLimitRemaining); remaining != "" && remaining != "0" {
		return nil
	}

	resetAt := parseRateLimitReset(resp.Header.Get(rateLimitReset))
	if !resetAt.IsZero() {
		formatted := resetAt.UTC().Format(time.RFC3339)
		g.logger.Warn("GitHub API rate limit exhausted", "resets_at", formatted)
		return fmt.Errorf("GitHub API rate limit exceeded; try again after %s", formatted)
	}

	g.logger.Warn("GitHub API rate limit exhausted", "resets_at", "unknown")
	return fmt.Errorf("GitHub API rate limit exceeded; try again later")
}

func parseRateLimitReset(header string) time.Time {
	if header == "" {
		return time.Time{}
	}

	if epoch, err := strconv.ParseInt(header, 10, 64); err == nil && epoch > 0 {
		return time.Unix(epoch, 0)
	}

	if ts, err := time.Parse(time.RFC3339, header); err == nil {
		return ts
	}

	return time.Time{}
}

func (g *GitHubMCPClient) getAccessToken() (string, error) {
	if g.token != "" {
		g.logAuthMode("personal_access_token")
		return g.token, nil
	}

	if g.hasGitHubAppCredentials() {
		g.logAuthMode("github_app_installation")
		return g.getCachedOrGenerateToken()
	}

	return "", fmt.Errorf("no authentication credentials provided")
}

func (g *GitHubMCPClient) logAuthMode(mode string) {
	if g.authModeLogged && g.authMode == mode {
		return
	}
	g.authMode = mode
	g.authModeLogged = true
	g.logger.Info("Using GitHub credentials", "mode", mode)
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
