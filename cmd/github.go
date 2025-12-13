package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
)

type fileInfo struct {
	owner string
	repo  string
	path  string
}

type fileCommitData struct {
	owner   string
	repo    string
	path    string
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
	RepoConfig       *config.ProjectConfig
}

type NetworkUpdateResult struct {
	PRUrl         string
	CommitURL     string
	UpdatedFiles  []string
	ImageUpgrades []imageUpgrade
	Success       bool
	Error         error
}

func NewGitHubDeployHandler(logger *slog.Logger, cfg *config.Config, slackClient SlackClient, agent AgentClient, mcpClient *GitHubMCPClient) *GitHubDeployHandler {
	return &GitHubDeployHandler{
		logger:      logger,
		config:      cfg,
		slack:       slackClient,
		agent:       agent,
		mcpClient:   mcpClient,
		docker:      NewDockerOperations(),
		yaml:        NewYAMLOperations(),
	}
}



func (h *GitHubDeployHandler) updateNetworkImages(ctx context.Context, req NetworkUpdateRequest) (*NetworkUpdateResult, error) {
	result := &NetworkUpdateResult{}

	if req.RepoConfig == nil {
		return result, fmt.Errorf("repo config not provided")
	}

	var matchingProjects []config.Project
	for _, project := range req.RepoConfig.Projects {
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

	if req.ReleaseTag == "" {
		primaryNetwork := req.DetectedNetworks[0]
		dockerResult, err := h.docker.FetchLatestStableTagsMCP(ctx, h.mcpClient, h.agent, filesToUpdate, primaryNetwork, "")
		if err != nil {
			return result, err
		}
		imageToTag = dockerResult.ImageToTag
	}

	filesToCommit, upgrades, err := h.prepareFileUpdatesMCP(ctx, filesToUpdate, imageToTag, req.ReleaseTag)
	if err != nil {
		return result, err
	}

	if len(filesToCommit) == 0 {
		return result, fmt.Errorf("no files needed updating")
	}

	owner := filesToCommit[0].owner
	repo := filesToCommit[0].repo

	branchName := h.generateBranchName(req, filesToCommit[0])
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

func (h *GitHubDeployHandler) agentUpdatePR(ctx context.Context, payload ReleasesWebhookPayload, summary *AgentSummary, repoConfig *config.ProjectConfig) (string, error) {
	if len(payload.Repositories) == 0 {
		return "", fmt.Errorf("no repositories in payload")
	}

	repo := payload.Repositories[0]

	dockerTag := summary.DockerTag
	if dockerTag == "" || dockerTag == "Not specified" {
		if repo.DockerTag != "" {
			h.logger.Info("Using docker tag from release metadata", "docker_tag", repo.DockerTag)
			dockerTag = repo.DockerTag
		} else {
			dockerTag = repo.ReleaseTag
			h.logger.Warn("llm unable to infer docker tag, using GitHub release tag", "github_tag", repo.ReleaseTag)
		}
	}

	var releaseDetails *ReleaseInfo
	for _, rel := range payload.Releases {
		releaseCopy := rel
		releaseDetails = &releaseCopy
		break
	}

	title, body, commitMessage := buildPRContent(repo.NetworkName, dockerTag, h.mcpClient.botName, summary, releaseDetails)
	if strings.TrimSpace(body) == "" {
		h.logger.Warn("LLM analysis missing; using fallback PR body", "network", repo.NetworkName, "tag", dockerTag)
		body = fmt.Sprintf("Automated update for %s to %s. No additional analysis provided.", repo.NetworkName, dockerTag)
	}

	req := NetworkUpdateRequest{
		DetectedNetworks: []string{strings.ToLower(repo.NetworkName)},
		ReleaseTag:       dockerTag,
		CommitMessage:    commitMessage,
		PRTitle:          title,
		PRBody:           body,
		BranchPrefix:     "ponos/ai-update",
		RepoConfig:       repoConfig,
	}

	result, err := h.updateNetworkImages(ctx, req)
	if err != nil {
		return "", err
	}

	return result.PRUrl, nil
}

func (h *GitHubDeployHandler) notifyError(channelID, message string) {
	blocks := createErrorBlocks("Error", message)
	if _, _, err := h.slack.PostMessage(channelID, slack.MsgOptionBlocks(blocks...)); err != nil {
		h.logger.Error("failed to send error message", "error", err, "channel", channelID, "message", message)
	}
}

func (h *GitHubDeployHandler) prepareFileUpdatesMCP(ctx context.Context, filesToUpdate []fileInfo, imageToTag map[string]string, releaseTag string) ([]fileCommitData, []imageUpgrade, error) {
	var filesToCommit []fileCommitData
	var upgrades []imageUpgrade

	for _, f := range filesToUpdate {
		content, err := h.mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
		if err != nil {
			continue
		}

		currentImageToTag := imageToTag
		if releaseTag != "" {
			images, err := h.agent.ExtractImages(ctx, content)
			if err != nil {
				h.logger.Warn("Failed to extract images from YAML", "error", err, "file", f.path)
				continue
			}
			currentImageToTag = make(map[string]string)
			for _, img := range images {
				currentImageToTag[img] = releaseTag
			}
		}

		newYAML, updated, uerr := h.yaml.UpdateAllImageTagsYAML(content, currentImageToTag)
		if uerr != nil {
			continue
		}
		if !updated {
			continue
		}

		fileUpgrades := h.trackImageUpgrades(content, currentImageToTag, f.path)
		upgrades = append(upgrades, fileUpgrades...)

		filesToCommit = append(filesToCommit, fileCommitData{
			owner:   f.owner,
			repo:    f.repo,
			path:    f.path,
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

func (h *GitHubDeployHandler) trackImageUpgrades(yamlContent string, imageToTag map[string]string, filePath string) []imageUpgrade {
	var upgrades []imageUpgrade
	
	// Extract current images from YAML
	currentImages := h.yaml.ExtractImageReposFromYAML(yamlContent)
	
	for _, img := range currentImages {
		if strings.Contains(img, ":") {
			repo := img[:strings.Index(img, ":")]
			if newTag, ok := imageToTag[repo]; ok {
				newImg := repo + ":" + newTag
				if img != newImg {
					upgrades = append(upgrades, imageUpgrade{
						file:   filePath,
						oldImg: img,
						newImg: newImg,
					})
				}
			}
		}
	}
	
	return upgrades
}

func (h *GitHubDeployHandler) generateBranchName(req NetworkUpdateRequest, fileCommit fileCommitData) string {
	networkName := "network"
	if len(req.DetectedNetworks) > 0 {
		networkName = req.DetectedNetworks[0]
	}
	
	cleanTag := strings.ReplaceAll(req.ReleaseTag, ".", "-")
	cleanTag = strings.ReplaceAll(cleanTag, ":", "-")
	cleanTag = strings.TrimPrefix(cleanTag, "v")
	
	branchName := fmt.Sprintf("upgrade/%s-to-%s", networkName, cleanTag)
	
	timestamp := time.Now().Unix()
	return fmt.Sprintf("%s-%d", branchName, timestamp)
}
