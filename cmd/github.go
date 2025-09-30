package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
	"gopkg.in/yaml.v3"
)

const (
	DeployDashboardCmd        = "/deploy-dashboard"
	DeployAPICmd              = "/deploy-api" 
	DeployProxyCmd            = "/deploy-proxy"
	UpdatePolkadotToLatestCmd = "/update-chain"
	UpdateNetworkCmd          = "/update-network"

	DashboardRepo = "blockops-sh/user-dashboard-client-v2"
	APIRepo       = "blockops-sh/api-core-service"
)

type RepoConfig struct {
	Name          string
	DefaultBranch string
	SourceBranch  string
}

type GitHubDeployHandler struct {
	bot         *Bot
	repoConfigs map[string]RepoConfig
	mcpClient   *GitHubMCPClient
	docker      *DockerOperations
	yaml        *YAMLOperations
}

type fileInfo struct {
	owner string
	repo  string
	path  string
}

type fileCommitData struct {
	owner   string
	repo    string
	path    string
	sha     string
	newYAML string
}

type imageUpgrade struct {
	file   string
	oldImg string
	newImg string
}

type FileUpdate struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type dockerTagResult struct {
	ImageToTag map[string]string
	Error      error
}

type dockerTag struct {
	Name        string `json:"name"`
	LastUpdated string `json:"last_updated"`
}

type dockerTagsResp struct {
	Results []dockerTag `json:"results"`
}

type NetworkUpdateRequest struct {
	DetectedNetworks []string
	ReleaseTag       string
	CommitMessage    string
	PRTitle          string
	PRBody           string
	BranchPrefix     string
}

type NetworkUpdateResult struct {
	PRUrl         string
	CommitURL     string
	UpdatedFiles  []string
	ImageUpgrades []imageUpgrade
	Success       bool
	Error         error
}


func NewGitHubDeployHandler(bot *Bot) *GitHubDeployHandler {
	// Initialize MCP client for GitHub operations
	mcpClient := NewGitHubMCPClient(
		"https://api.githubcopilot.com/mcp/",
		bot.config.GitHubToken,
		bot.logger,
	)

	return &GitHubDeployHandler{
		bot:       bot,
		mcpClient: mcpClient,
		docker:    NewDockerOperations(),
		yaml:     NewYAMLOperations(),
		repoConfigs: map[string]RepoConfig{
			DeployDashboardCmd: {
				Name:          DashboardRepo,
				DefaultBranch: "main",
				SourceBranch:  "development",
			},
			DeployAPICmd: {
				Name:          APIRepo,
				DefaultBranch: "main", 
				SourceBranch:  "development",
			},
		},
	}
}

func (h *GitHubDeployHandler) HandleDeploy(command, text, userID, channelID string) *SlashCommandResponse {
	params := strings.Fields(text)
	if len(params) < 3 {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Usage: " + command + " <branch> <environment> <service>",
		}
	}

	branch := params[0]
	environment := params[1] 
	service := params[2]

	if _, exists := h.repoConfigs[command]; !exists {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Unknown deployment command: " + command,
		}
	}

	go h.startDeployment(service, branch, environment, userID, channelID)

	return &SlashCommandResponse{
		ResponseType: "in_channel",
		Blocks:       createDeploymentStartBlocks(service, branch, environment, userID),
	}
}

func (h *GitHubDeployHandler) startDeployment(service, branch, environment, userID, channelID string) {
	// For now, just simulate deployment success
	blocks := createSuccessBlocks("Deployment completed!",
		fmt.Sprintf("*Service:* %s\n*Branch:* %s\n*Environment:* %s\n*Deployed by:* <@%s>",
			service, branch, environment, userID))

	if _, _, err := h.bot.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...)); err != nil {
		h.bot.logger.Error("failed to send deployment success message", "error", err, "channel", channelID)
	}
}

func (h *GitHubDeployHandler) HandleChainUpdate(updateType, text, userID string) *SlashCommandResponse {
	params := strings.Fields(text)
	if len(params) != 1 {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("Usage: /%s <chain>. Only 'polkadot' is supported.", updateType),
		}
	}
	chain := strings.ToLower(params[0])
	if chain != "polkadot" {
		go h.notifyUpdateChainError("rpc-updates", chain, userID)
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Only 'polkadot' is supported for now.",
		}
	}

	switch updateType {
	case "update-chain":
		go h.startPolkadotUpdate(userID)
	case "update-network":
		go h.startNetworkUpdate(userID)
	default:
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Unsupported update type.",
		}
	}

	return &SlashCommandResponse{
		ResponseType: "in_channel",
		Blocks:       createUpdateStartBlocks(chain, userID),
	}
}

func (h *GitHubDeployHandler) HandleUpdateChain(text, userID string) *SlashCommandResponse {
	return h.HandleChainUpdate("update-chain", text, userID)
}

func (h *GitHubDeployHandler) HandleUpdateNetwork(text, userID string) *SlashCommandResponse {
	return h.HandleChainUpdate("update-network", text, userID)
}

func (h *GitHubDeployHandler) startPolkadotUpdate(userID string) {
	ctx := context.Background()
	req := NetworkUpdateRequest{
		DetectedNetworks: []string{"polkadot"},
		ReleaseTag:       "",
		CommitMessage:    "ponos: update polkadot image tags to latest stable",
		PRTitle:          "Update polkadot image tags to latest stable",
		PRBody:           "Automated update of polkadot Docker image tags to latest stable versions.",
		BranchPrefix:     "ponos/update-polkadot",
	}

	result, err := h.updateNetworkImages(ctx, req)
	if err != nil {
		h.notifyError("rpc-updates", fmt.Sprintf("Polkadot update failed: %v", err))
		return
	}

	// Send success notification
	blocks := createSuccessBlocks("Polkadot update completed!",
		fmt.Sprintf("*Pull Request:* <%s|View PR>\n*Commit:* <%s|View Commit>",
			result.PRUrl, result.CommitURL))

	if _, _, err := h.bot.client.PostMessage("rpc-updates", slack.MsgOptionBlocks(blocks...)); err != nil {
		h.bot.logger.Error("failed to send polkadot update success message", "error", err)
	}
}

func (h *GitHubDeployHandler) startNetworkUpdate(userID string) {
	ctx := context.Background()
	req := NetworkUpdateRequest{
		DetectedNetworks: []string{"polkadot"},
		ReleaseTag:       "",
		CommitMessage:    "ponos: update all network image tags to latest stable",
		PRTitle:          "Update network image tags to latest stable",
		PRBody:           "Automated update of network Docker image tags to latest stable versions.",
		BranchPrefix:     "ponos/update-network",
	}

	result, err := h.updateNetworkImages(ctx, req)
	if err != nil {
		h.notifyError("rpc-updates", fmt.Sprintf("Network update failed: %v", err))
		return
	}

	// Send success notification
	blocks := createSuccessBlocks("Network update completed!",
		fmt.Sprintf("*Pull Request:* <%s|View PR>\n*Commit:* <%s|View Commit>",
			result.PRUrl, result.CommitURL))

	if _, _, err := h.bot.client.PostMessage("rpc-updates", slack.MsgOptionBlocks(blocks...)); err != nil {
		h.bot.logger.Error("failed to send network update success message", "error", err)
	}
}

func (h *GitHubDeployHandler) updateNetworkImages(ctx context.Context, req NetworkUpdateRequest) (*NetworkUpdateResult, error) {
	result := &NetworkUpdateResult{}

	projectConfig, err := config.LoadProjectConfig("config.yaml")
	if err != nil {
		return result, fmt.Errorf("failed to load project config: %v", err)
	}

	var matchingProjects []config.Project
	for _, project := range projectConfig.Projects {
		for _, detectedNetwork := range req.DetectedNetworks {
			if strings.EqualFold(project.Network, detectedNetwork) {
				matchingProjects = append(matchingProjects, project)
				break
			}
		}
	}

	if len(matchingProjects) == 0 {
		return result, fmt.Errorf("no matching projects found for networks: %v", req.DetectedNetworks)
	}

	var filesToUpdate []fileInfo
	for _, project := range matchingProjects {
		for _, path := range project.Paths {
			filesToUpdate = append(filesToUpdate, fileInfo{
				owner: project.Owner,
				repo:  project.Name,
				path:  path,
			})
		}
	}

	// No need to create client - using MCP directly

	imageToTag := make(map[string]string)

	if req.ReleaseTag != "" {
		for _, f := range filesToUpdate {
			content, ferr := h.mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
			if ferr != nil {
				continue
			}
			images := h.yaml.ExtractImageReposFromYAML(content)
			for _, img := range images {
				imageToTag[img] = req.ReleaseTag
			}
		}
	} else {
		dockerResult, err := h.docker.FetchLatestStableTagsMCP(ctx, h.mcpClient, filesToUpdate)
		if err != nil {
			return result, err
		}
		imageToTag = dockerResult.ImageToTag
	}

	filesToCommit, upgrades, err := h.prepareFileUpdatesMCP(ctx, filesToUpdate, imageToTag)
	if err != nil {
		return result, err
	}

	fmt.Printf("DEBUG: PrepareFileUpdates returned %d files to commit\n", len(filesToCommit))
	if len(filesToCommit) == 0 {
		return result, fmt.Errorf("no files needed updating")
	}

	owner := filesToCommit[0].owner
	repo := filesToCommit[0].repo

	// Create branch from main first
	branchName := fmt.Sprintf("%s-%d", req.BranchPrefix, time.Now().Unix())
	fmt.Printf("DEBUG: Creating branch %s from main\n", branchName)
	err = h.mcpClient.CreateBranch(ctx, owner, repo, branchName)
	if err != nil {
		return result, fmt.Errorf("failed to create branch: %v", err)
	}

	// Create commit on the new branch
	fmt.Printf("DEBUG: Creating commit on branch %s\n", branchName)
	commitSHA, err := h.createCommitFromFilesMCP(ctx, owner, repo, branchName, filesToCommit, req.CommitMessage)
	if err != nil {
		return result, err
	}

	// Create PR from branch to main
	fmt.Printf("DEBUG: Creating PR from %s to main\n", branchName)
	prURL, err := h.mcpClient.CreatePullRequest(ctx, owner, repo, branchName, "main", req.PRTitle, req.PRBody)
	if err != nil {
		return result, err
	}

	result.PRUrl = prURL
	result.CommitURL = fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, commitSHA)
	result.ImageUpgrades = upgrades
	result.Success = true

	return result, nil
}

func (h *GitHubDeployHandler) agentUpdatePR(ctx context.Context, payload ReleasesWebhookPayload, summary *AgentSummary) (string, error) {
	if len(payload.Repositories) == 0 {
		return "", fmt.Errorf("no repositories in payload")
	}

	repo := payload.Repositories[0]
	releaseTag := repo.ReleaseTag

	title, body, commitMessage := BuildPRContent(repo.NetworkName, releaseTag, summary)

	req := NetworkUpdateRequest{
		DetectedNetworks: []string{strings.ToLower(repo.NetworkName)},
		ReleaseTag:       releaseTag,
		CommitMessage:    commitMessage,
		PRTitle:          title,
		PRBody:           body,
		BranchPrefix:     "ponos/ai-update",
	}

	result, err := h.updateNetworkImages(ctx, req)
	if err != nil {
		return "", err
	}

	return result.PRUrl, nil
}

func (h *GitHubDeployHandler) notifyError(channelID, message string) {
	blocks := createErrorBlocks("Error", message)
	if _, _, err := h.bot.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...)); err != nil {
		h.bot.logger.Error("failed to send error message", "error", err, "channel", channelID, "message", message)
	}
}

func (h *GitHubDeployHandler) notifyUpdateChainError(channelID, chain, userID string) {
	blocks := createErrorBlocks("Update failed",
		fmt.Sprintf("Chain '%s' is not supported. Only 'polkadot' is allowed.\nRequested by: <@%s>", chain, userID))
	_, _, err := h.bot.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		h.bot.logger.Error("failed to send update chain error message", "error", err, "channel", channelID, "chain", chain)
	}
}

// prepareFileUpdatesMCP prepares file updates using MCP to get file contents
func (h *GitHubDeployHandler) prepareFileUpdatesMCP(ctx context.Context, filesToUpdate []fileInfo, imageToTag map[string]string) ([]fileCommitData, []imageUpgrade, error) {
	var filesToCommit []fileCommitData
	var upgrades []imageUpgrade

	fmt.Printf("DEBUG: PrepareFileUpdates called with %d files, imageToTag: %v\n", len(filesToUpdate), imageToTag)

	for _, f := range filesToUpdate {
		fmt.Printf("DEBUG: Processing file %s/%s:%s\n", f.owner, f.repo, f.path)
		
		// Get file content using MCP
		content, err := h.mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
		if err != nil {
			fmt.Printf("DEBUG: Failed to get file content for %s: %v\n", f.path, err)
			continue
		}

		newYAML, updated, uerr := h.yaml.UpdateAllImageTagsYAML(content, imageToTag)
		if uerr != nil {
			fmt.Printf("DEBUG: Failed to update YAML for %s: %v\n", f.path, uerr)
			continue
		}
		if !updated {
			fmt.Printf("DEBUG: No updates needed for file %s\n", f.path)
			continue
		}

		fmt.Printf("DEBUG: File %s was updated successfully\n", f.path)

		// Track upgrades for reporting
		var oldToNew []imageUpgrade
		var root yaml.Node
		if yaml.Unmarshal([]byte(content), &root) == nil {
			var walk func(n *yaml.Node)
			walk = func(n *yaml.Node) {
				if n == nil {
					return
				}
				switch n.Kind {
				case yaml.MappingNode:
					for i := 0; i < len(n.Content)-1; i += 2 {
						key := n.Content[i]
						val := n.Content[i+1]
						if key.Value == "image" && val.Kind == yaml.ScalarNode {
							img := val.Value
							if idx := strings.Index(img, ":"); idx > 0 {
								repo := img[:idx]
								if tag, ok := imageToTag[repo]; ok {
									newVal := repo + ":" + tag
									if img != newVal {
										oldToNew = append(oldToNew, imageUpgrade{file: f.path, oldImg: img, newImg: newVal})
									}
								}
							}
						}
						walk(val)
					}
				case yaml.SequenceNode:
					for _, item := range n.Content {
						walk(item)
					}
				}
			}
			if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
				walk(root.Content[0])
			} else {
				walk(&root)
			}
		}
		upgrades = append(upgrades, oldToNew...)

		filesToCommit = append(filesToCommit, fileCommitData{
			owner:   f.owner,
			repo:    f.repo,
			path:    f.path,
			sha:     "", // MCP handles SHA automatically
			newYAML: newYAML,
		})
	}

	return filesToCommit, upgrades, nil
}

// createCommitFromFilesMCP creates a commit with multiple file changes using MCP
func (h *GitHubDeployHandler) createCommitFromFilesMCP(ctx context.Context, owner, repo, branch string, filesToCommit []fileCommitData, commitMsg string) (string, error) {
	// Convert fileCommitData to FileUpdate format for MCP
	var files []FileUpdate
	for _, f := range filesToCommit {
		files = append(files, FileUpdate{
			Path:    f.path,
			Content: f.newYAML,
		})
	}

	return h.mcpClient.CreateCommit(ctx, owner, repo, branch, commitMsg, files)
}
