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
	DeployDashboardCmd = "/deploy-dashboard"
	DeployAPICmd       = "/deploy-api"
	DeployProxyCmd     = "/deploy-proxy"
	UpdateNetworkCmd   = "/update-network"

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
	return &GitHubDeployHandler{
		bot:       bot,
		mcpClient: bot.mcpClient,
		docker:    NewDockerOperations(),
		yaml:      NewYAMLOperations(),
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
			Text:         fmt.Sprintf("Usage: /%s <network>", updateType),
		}
	}
	network := strings.ToLower(params[0])

	go h.startNetworkUpdate(network, updateType, userID)

	return &SlashCommandResponse{
		ResponseType: "in_channel",
		Blocks:       createUpdateStartBlocks(network, userID),
	}
}

func (h *GitHubDeployHandler) startNetworkUpdate(network, updateType, userID string) {
	ctx := context.Background()

	if h.bot.agent == nil {
		h.notifyError(h.bot.config.SlackUpdateChannel, "AI agent not available for network updates")
		return
	}

	releaseInfo, err := h.bot.agent.GetLatestNetworkRelease(ctx, network)
	if err != nil {
		h.bot.logger.Error("Failed to get latest release", "error", err, "network", network)
		h.notifyError(h.bot.config.SlackUpdateChannel, fmt.Sprintf("Failed to get latest %s release: %v", network, err))
		return
	}

	payload := ReleasesWebhookPayload{
		EventType:    "manual_update",
		Username:     userID,
		Timestamp:    time.Now().Format(time.RFC3339),
		Repositories: []Repository{releaseInfo.Repository},
		Releases: map[string]ReleaseInfo{
			releaseInfo.Release.TagName: releaseInfo.Release,
		},
	}

	summary, err := h.bot.agent.ProcessReleaseUpdate(ctx, payload)
	if err != nil {
		h.bot.logger.Error("AI agent processing failed", "error", err, "network", network)
		h.notifyError(h.bot.config.SlackUpdateChannel, fmt.Sprintf("AI analysis failed for %s: %v", network, err))
		return
	}

	prURL, err := h.agentUpdatePR(ctx, payload, summary)
	if err != nil {
		h.bot.logger.Error("Agent failed to create PR", "error", err)
		h.notifyError(h.bot.config.SlackUpdateChannel, fmt.Sprintf("Failed to create PR for %s: %v", network, err))
		return
	}

	h.bot.logger.Info("Network update PR created with AI analysis", "url", prURL, "network", network)
	h.bot.sendReleaseSummaryFromAgent(h.bot.config.SlackUpdateChannel, payload, summary, prURL)
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

	imageToTag := make(map[string]string)

	if req.ReleaseTag != "" {
		for _, f := range filesToUpdate {
			content, ferr := h.mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
			if ferr != nil {
				continue
			}
			images := h.extractImageReposWithLLM(ctx, content)
			for _, img := range images {
				imageToTag[img] = req.ReleaseTag
			}
		}
	} else {
		primaryNetwork := req.DetectedNetworks[0]
		dockerResult, err := h.docker.FetchLatestStableTagsMCP(ctx, h.mcpClient, h.bot.agent, filesToUpdate, primaryNetwork, "")
		if err != nil {
			return result, err
		}
		imageToTag = dockerResult.ImageToTag
	}

	filesToCommit, upgrades, err := h.prepareFileUpdatesMCP(ctx, filesToUpdate, imageToTag)
	if err != nil {
		return result, err
	}

	if len(filesToCommit) == 0 {
		return result, fmt.Errorf("no files needed updating")
	}

	owner := filesToCommit[0].owner
	repo := filesToCommit[0].repo

	branchName := fmt.Sprintf("%s-%d", req.BranchPrefix, time.Now().Unix())
	err = h.mcpClient.CreateBranch(ctx, owner, repo, branchName)
	if err != nil {
		return result, fmt.Errorf("failed to create branch: %v", err)
	}

	commitSHA, err := h.createCommitFromFilesMCP(ctx, owner, repo, branchName, filesToCommit, req.CommitMessage)
	if err != nil {
		return result, err
	}

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

	dockerTag := summary.DockerTag
	if dockerTag == "" || dockerTag == "Not specified" {
		if repo.DockerTag != "" {
			h.bot.logger.Info("Using docker tag from release metadata", "docker_tag", repo.DockerTag)
			dockerTag = repo.DockerTag
		} else {
			dockerTag = repo.ReleaseTag
			h.bot.logger.Warn("llm unable to infer docker tag, using GitHub release tag", "github_tag", repo.ReleaseTag)
		}
	}

	var releaseDetails *ReleaseInfo
	for _, rel := range payload.Releases {
		releaseCopy := rel
		releaseDetails = &releaseCopy
		break
	}

	title, body, commitMessage := BuildPRContent(repo.NetworkName, dockerTag, h.mcpClient.botName, summary, releaseDetails)

	req := NetworkUpdateRequest{
		DetectedNetworks: []string{strings.ToLower(repo.NetworkName)},
		ReleaseTag:       dockerTag,
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

func (h *GitHubDeployHandler) prepareFileUpdatesMCP(ctx context.Context, filesToUpdate []fileInfo, imageToTag map[string]string) ([]fileCommitData, []imageUpgrade, error) {
	var filesToCommit []fileCommitData
	var upgrades []imageUpgrade

	for _, f := range filesToUpdate {
		content, err := h.mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
		if err != nil {
			continue
		}

		newYAML, updated, uerr := h.yaml.UpdateAllImageTagsYAML(content, imageToTag)
		if uerr != nil {
			continue
		}
		if !updated {
			continue
		}

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
			sha:     "",
			newYAML: newYAML,
		})
	}

	return filesToCommit, upgrades, nil
}

func (h *GitHubDeployHandler) createCommitFromFilesMCP(ctx context.Context, owner, repo, branch string, filesToCommit []fileCommitData, commitMsg string) (string, error) {
	var files []FileUpdate
	for _, f := range filesToCommit {
		files = append(files, FileUpdate{
			Path:    f.path,
			Content: f.newYAML,
		})
	}

	return h.mcpClient.CreateCommit(ctx, owner, repo, branch, commitMsg, files)
}

func (h *GitHubDeployHandler) extractImageReposWithLLM(ctx context.Context, yamlContent string) []string {
	if h.bot.agent != nil {
		if llmRepos, err := h.bot.agent.AnalyzeYAMLForBlockchainContainers(ctx, yamlContent); err == nil && len(llmRepos) > 0 {
			h.bot.logger.Info("Using LLM analysis for image extraction", "repos_found", len(llmRepos))
			return llmRepos
		} else if err != nil {
			h.bot.logger.Warn("LLM analysis failed, falling back to pattern matching", "error", err)
		} else {
			h.bot.logger.Info("LLM found no blockchain containers, falling back to pattern matching")
		}
	}

	h.bot.logger.Info("Using pattern matching for image extraction")
	return h.yaml.ExtractImageReposFromYAML(yamlContent)
}
