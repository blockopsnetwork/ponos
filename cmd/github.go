package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v72/github"
	"github.com/slack-go/slack"
	"golang.org/x/oauth2"
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
	pemKey      []byte // stores the contents of the PEM key file
	appID       int64  // GitHub App ID
	installID   int64  // GitHub App Installation ID
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

type imageUpgrade struct {
	file   string
	oldImg string
	newImg string
}

type fileCommitData struct {
	owner   string
	repo    string
	path    string
	sha     string
	newYAML string
}

type dockerTagResult struct {
	ImageToTag map[string]string
	Error      error
}

func NewGitHubDeployHandler(bot *Bot) *GitHubDeployHandler {
	pemKey, err := os.ReadFile(bot.config.GitHubPEMKey)
	if err != nil {
		panic(fmt.Sprintf("failed to read GitHub PEM key file: %s %v", bot.config.GitHubPEMKey, err))
	}

	return &GitHubDeployHandler{
		bot: bot,
		repoConfigs: map[string]RepoConfig{
			"dashboard": {
				Name:          DashboardRepo,
				DefaultBranch: "dev",
				SourceBranch:  "staging",
			},
			"api": {
				Name:          APIRepo,
				DefaultBranch: "dev",
				SourceBranch:  "staging",
			},
		},
		pemKey:    pemKey,
		appID:     bot.config.GitHubAppID,
		installID: bot.config.GitHubInstallID,
	}
}

func (h *GitHubDeployHandler) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": h.appID,
		"alg": "rs256",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	key, err := jwt.ParseRSAPrivateKeyFromPEM(h.pemKey)
	if err != nil {
		return "", fmt.Errorf("parsing PEM key: %v", err)
	}

	signedToken, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("signing token: %v", err)
	}

	return signedToken, nil
}

func (h *GitHubDeployHandler) createGitHubClient(ctx context.Context) (*github.Client, error) {
	jwt, err := h.generateJWT()
	if err != nil {
		return nil, fmt.Errorf("generating JWT: %v", err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	tc := oauth2.NewClient(ctx, ts)
	tempClient := github.NewClient(tc)

	installToken, _, err := tempClient.Apps.CreateInstallationToken(ctx, h.installID, nil)
	if err != nil {
		return nil, fmt.Errorf("creating installation token: %v", err)
	}

	// Create the final client using the installation token
	ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: installToken.GetToken()})
	tc = oauth2.NewClient(ctx, ts)
	return github.NewClient(tc), nil
}

func (h *GitHubDeployHandler) HandleDeploy(command, text, userID, channelID string) *SlashCommandResponse {
	params := strings.Fields(text)
	if len(params) > 0 {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("Usage: %s. We will auto deploy to the staging branch", command),
		}
	}

	branch := "staging"
	environment := "staging"

	var service string
	switch command {
	case DeployDashboardCmd:
		service = "dashboard"
	case DeployAPICmd:
		service = "api"
	case DeployProxyCmd:
		service = "proxy"
	}

	// Deployment initiated

	go h.startDeployment(service, branch, environment, userID, channelID)

	return &SlashCommandResponse{
		ResponseType: "in_channel",
		Blocks: []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, ":rocket: *Deployment started!*", false, false),
				nil,
				nil,
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					fmt.Sprintf("*Service:* %s\n*Branch:* %s\n*Environment:* %s\n*Requested by:* <@%s>",
						service, branch, environment, userID),
					false, false),
				nil,
				nil,
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, "_I'll keep you updated on the progress._", false, false),
				nil,
				nil,
			),
		},
	}
}

func (h *GitHubDeployHandler) startDeployment(service, branch, environment, userID, channelID string) {
	ctx := context.Background()

	client, err := h.createGitHubClient(ctx)
	if err != nil {
		h.notifyError(channelID, fmt.Sprintf("Failed to create GitHub client: %v", err))
		return
	}

	repoConfig, ok := h.repoConfigs[service]
	if !ok {
		h.notifyError(channelID, fmt.Sprintf("Service %s not configured", service))
		return
	}

	parts := strings.Split(repoConfig.Name, "/")
	owner, repo := parts[0], parts[1]

	// Get the reference to default branch
	defaultRef, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", repoConfig.DefaultBranch))
	if err != nil {
		h.notifyError(channelID, fmt.Sprintf("Failed to get default branch '%s': %v", repoConfig.DefaultBranch, err))
		return
	}

	// Get the reference to source branch
	sourceRef, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", repoConfig.SourceBranch))
	if err != nil {
		h.notifyError(channelID, fmt.Sprintf("Failed to get source branch reference: %v", err))
		return
	}

	if sourceRef.Object.GetSHA() == defaultRef.Object.GetSHA() {
		h.notifyError(channelID, fmt.Sprintf("Deployment skipped: already running the latest changes (commit: `%s`)", sourceRef.Object.GetSHA()))
		return
	}

	// Update source branch reference to point to default branch (equivalent to rebase)
	sourceRef.Object.SHA = defaultRef.Object.SHA
	_, _, err = client.Git.UpdateRef(ctx, owner, repo, sourceRef, true)
	if err != nil {
		h.notifyError(channelID, fmt.Sprintf("Failed to rebase branches: %v", err))
		return
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, ":white_check_mark: *Deployment completed!*", false, false),
			nil,
			nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("*Service:* %s\n*Branch:* %s\n*Environment:* %s\n*Deployed by:* <@%s>\n*Commit:* <https://github.com/%s/commit/%s|`%s`>",
					service, branch, environment, userID,
					repoConfig.Name,
					*defaultRef.Object.SHA,
					*defaultRef.Object.SHA),
				false, false),
			nil,
			nil,
		),
	}

	_, _, err = h.bot.client.PostMessage(channelID,
		slack.MsgOptionBlocks(blocks...))
	if err != nil {
		h.bot.logger.Error("failed to send deployment completion message",
			"error", err,
			"service", service,
			"branch", branch,
			"environment", environment)
	}
}

func (h *GitHubDeployHandler) notifyError(channelID, message string) {

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, ":x: *Deployment failed*", false, false),
			nil,
			nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, message, false, false),
			nil,
			nil,
		),
	}
	_, _, err := h.bot.client.PostMessage(channelID,
		slack.MsgOptionBlocks(blocks...))
	if err != nil {
		h.bot.logger.Error("failed to send error message",
			"error", err,
			"channel", channelID,
			"message", message)
	}
}

func (h *GitHubDeployHandler) HandleUpdateChain(text, userID string) *SlashCommandResponse {
	params := strings.Fields(text)
	if len(params) != 1 {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Usage: /update-chain <chain>. Only 'polkadot' is supported.",
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

	go h.startPolkadotUpdate(userID)
	return &SlashCommandResponse{
		ResponseType: "in_channel",
		Blocks: []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, ":rocket: *Polkadot update started!*", false, false),
				nil,
				nil,
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					fmt.Sprintf("*Chain:* %s\n*Requested by:* <@%s>", chain, userID),
					false, false),
				nil,
				nil,
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, "_I'll keep you updated on the progress._", false, false),
				nil,
				nil,
			),
		},
	}
}

func (h *GitHubDeployHandler) notifyUpdateChainError(channelID, chain, userID string) {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, ":x: *Update failed*", false, false),
			nil,
			nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("Chain '%s' is not supported. Only 'polkadot' is allowed.\nRequested by: <@%s>", chain, userID),
				false, false),
			nil,
			nil,
		),
	}
	_, _, err := h.bot.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		h.bot.logger.Error("failed to send update chain error message", "error", err, "channel", channelID, "chain", chain)
	}
}

func (h *GitHubDeployHandler) HandleUpdateNetwork(text, userID string) *SlashCommandResponse {
	params := strings.Fields(text)
	if len(params) != 1 {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Usage: /update-network <chain>. Only 'polkadot' is supported.",
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

	go h.startNetworkUpdate(userID)
	return &SlashCommandResponse{
		ResponseType: "in_channel",
		Blocks: []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, ":rocket: *Polkadot update started!*", false, false),
				nil,
				nil,
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					fmt.Sprintf("*Chain:* %s\n*Requested by:* <@%s>", chain, userID),
					false, false),
				nil,
				nil,
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, "_I'll keep you updated on the progress._", false, false),
				nil,
				nil,
			),
		},
	}
}

func (h *GitHubDeployHandler) startPolkadotUpdate(userID string) {
	type dockerTag struct {
		Name        string `json:"name"`
		LastUpdated string `json:"last_updated"`
	}
	type dockerTagsResp struct {
		Results []dockerTag `json:"results"`
	}

	projectConfig, err := config.LoadProjectConfig("config.yaml")
	if err != nil {
		return
	}

	var filesToUpdate []struct {
		owner string
		repo  string
		path  string
	}

	for _, project := range projectConfig.Projects {
		for _, path := range project.Paths {
			filesToUpdate = append(filesToUpdate, struct {
				owner string
				repo  string
				path  string
			}{
				owner: project.Owner,
				repo:  project.Name,
				path:  path,
			})
		}
	}

	ctx := context.Background()
	client, err := h.createGitHubClient(ctx)
	if err != nil {
		h.bot.logger.Error("Failed to create GitHub client", "error", err)
		return
	}

	imageToTag := make(map[string]string) // image repo -> latest stable tag

	for _, f := range filesToUpdate {
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			continue
		}
		images := extractImageReposFromYAML(content)
		for _, img := range images {
			imageToTag[img] = ""
		}
	}

	for img := range imageToTag {
		parts := strings.Split(img, "/")
		if len(parts) != 2 {
			h.bot.logger.Error("Invalid image name", "image", img)
			h.notifyError("rpc-updates", "Invalid image name: "+img)
			return
		}
		url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/%s/tags?page_size=100", parts[0], parts[1])
		resp, err := http.Get(url)
		if err != nil {
			h.notifyError("rpc-updates", fmt.Sprintf("Failed to fetch Docker tags for %s: %v", img, err))
			return
		}
		var tagsResp dockerTagsResp
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			h.notifyError("rpc-updates", fmt.Sprintf("Failed to parse Docker tags JSON for %s: %v", img, err))
			return
		}
		resp.Body.Close()
		var stableTags []dockerTag
		for _, tag := range tagsResp.Results {
			if strings.HasPrefix(tag.Name, "stable") {
				stableTags = append(stableTags, tag)
			}
		}
		if len(stableTags) == 0 {
			h.notifyError("rpc-updates", "No stable tags found for "+img)
			return
		}
		sort.Slice(stableTags, func(i, j int) bool {
			return stableTags[i].LastUpdated > stableTags[j].LastUpdated
		})
		imageToTag[img] = stableTags[0].Name
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, ":white_check_mark: *Polkadot update completed!*", false, false),
			nil,
			nil,
		),
	}

	var commitURL string

	var filesToCommit []struct {
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

	var upgrades []imageUpgrade

	for _, f := range filesToUpdate {
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			continue
		}

		var oldToNew []imageUpgrade
		var root yaml.Node
		err := yaml.Unmarshal([]byte(content), &root)
		if err == nil {
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
		newYAML, updated, uerr := updateAllImageTagsYAML(content, imageToTag)
		if uerr != nil || !updated {
			continue
		}
		filesToCommit = append(filesToCommit, struct {
			owner   string
			repo    string
			path    string
			sha     string
			newYAML string
		}{f.owner, f.repo, f.path, *file.SHA, newYAML})
		upgrades = append(upgrades, oldToNew...)
	}

	if len(filesToCommit) == 0 {
		return
	}

	owner := filesToCommit[0].owner
	repo := filesToCommit[0].repo
	branch := "main"

	ref, _, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		h.notifyError("rpc-updates", "Failed to get branch ref: "+err.Error())
		return
	}

	baseCommit, _, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		h.notifyError("rpc-updates", "Failed to get base commit: "+err.Error())
		return
	}

	var entries []*github.TreeEntry
	for _, f := range filesToCommit {
		blob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
			Content:  github.Ptr(f.newYAML),
			Encoding: github.Ptr("utf-8"),
		})
		if err != nil {
			h.notifyError("rpc-updates", "Failed to create blob: "+err.Error())
			return
		}

		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(f.path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  blob.SHA,
		})
	}

	tree, _, err := client.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, entries)
	if err != nil {
		h.notifyError("rpc-updates", "Failed to create tree: "+err.Error())
		return
	}

	commitMsg := "ponos: update all polkadot image tags to latest stable"
	now := time.Now()
	author := &github.CommitAuthor{
		Name:  github.Ptr("ponos-bot"),
		Email: github.Ptr("ponos@blockops.sh"),
		Date:  &github.Timestamp{Time: now},
	}
	newCommit := &github.Commit{
		Message:   github.Ptr(commitMsg),
		Tree:      tree,
		Parents:   []*github.Commit{baseCommit},
		Author:    author,
		Committer: author,
	}

	commit, _, err := client.Git.CreateCommit(ctx, owner, repo, newCommit, nil)
	if err != nil {
		h.bot.logger.Error("Failed to create commit", "error", err)
		h.notifyError("rpc-updates", "Failed to create commit: "+err.Error())
		return
	}

	ref.Object.SHA = commit.SHA
	_, _, err = client.Git.UpdateRef(ctx, owner, repo, ref, false)
	if err != nil {
		h.bot.logger.Error("Failed to update branch ref", "error", err)
		h.notifyError("rpc-updates", "Failed to update branch ref: "+err.Error())
		return
	}

	commitURL = fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, *commit.SHA)
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType,
			fmt.Sprintf(":link: *Commit:* %s", commitURL), false, false),
		nil, nil,
	))

	if len(upgrades) > 0 {
		var b strings.Builder
		b.WriteString("*Image Upgrades:*")
		for _, up := range upgrades {
			b.WriteString(fmt.Sprintf("\n• `%s`: `%s` → `%s`", up.file, up.oldImg, up.newImg))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, b.String(), false, false),
			nil, nil,
		))
	}
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType,
			":memo: *All K8s YAML preview files updated in a single commit*", false, false),
		nil, nil,
	))
	_, _, err = h.bot.client.PostMessage("rpc-updates", slack.MsgOptionBlocks(blocks...))
	if err != nil {
		h.bot.logger.Error("failed to send polkadot update completion message", "error", err, "user", userID)
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

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, ":white_check_mark: *Network update completed!*", false, false),
			nil, nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("*Pull Request:* <%s|View PR>", result.PRUrl), false, false),
			nil, nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("*Commit:* <%s|View Commit>", result.CommitURL), false, false),
			nil, nil,
		),
	}

	if len(result.ImageUpgrades) > 0 {
		var b strings.Builder
		b.WriteString("*Image Upgrades:*")
		for _, up := range result.ImageUpgrades {
			b.WriteString(fmt.Sprintf("\n• `%s`: `%s` → `%s`", up.file, up.oldImg, up.newImg))
		}
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, b.String(), false, false),
			nil, nil,
		))
	}

	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType,
			":memo: *All K8s YAML network files updated via pull request*", false, false),
		nil, nil,
	))

	_, _, err = h.bot.client.PostMessage("rpc-updates", slack.MsgOptionBlocks(blocks...))
	if err != nil {
		h.bot.logger.Error("failed to send network update completion message", "error", err, "user", userID)
	}
}

func (h *GitHubDeployHandler) createBranchAndPR(ctx context.Context, client *github.Client, owner, repo string, commit *github.Commit, branchName, prTitle, prBody string) (*github.PullRequest, error) {

	newRef := &github.Reference{
		Ref: github.Ptr("refs/heads/" + branchName),
		Object: &github.GitObject{
			SHA: commit.SHA,
		},
	}

	_, _, err := client.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch: %v", err)
	}

	newPR := &github.NewPullRequest{
		Title: github.Ptr(prTitle),
		Head:  github.Ptr(branchName),
		Base:  github.Ptr("main"),
		Body:  github.Ptr(prBody),
	}

	pullRequest, _, err := client.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %v", err)
	}

	return pullRequest, nil
}

func extractImageReposFromYAML(yamlContent string) []string {
	mainRepos := extractMainApplicationRepos(yamlContent)
	if len(mainRepos) > 0 {
		return mainRepos
	}
	
	// Fallback to extracting all repos if we can't identify main containers
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlContent), &root)
	if err != nil {
		return nil
	}
	var repos []string
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
						repos = append(repos, img[:idx])
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
	return repos
}

func extractMainApplicationRepos(yamlContent string) []string {
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlContent), &root)
	if err != nil {
		return nil
	}
	
	type containerInfo struct {
		name            string
		image           string
		repo            string
		isMainContainer bool
	}
	
	var containers []containerInfo
	var currentContainerName string
	
	var walk func(n *yaml.Node, depth int)
	walk = func(n *yaml.Node, depth int) {
		if n == nil {
			return
		}
		
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i < len(n.Content)-1; i += 2 {
				key := n.Content[i]
				val := n.Content[i+1]
				
				// Track container names
				if key.Value == "name" && val.Kind == yaml.ScalarNode {
					currentContainerName = val.Value
				}
				
				// Extract image information - handle both formats
				if key.Value == "image" {
					if val.Kind == yaml.ScalarNode {
						// Format: image: parity/polkadot:tag
						img := val.Value
						if idx := strings.Index(img, ":"); idx > 0 {
							repo := img[:idx]
							
							isMain := isMainContainer(currentContainerName, repo)
									container := containerInfo{
								name:            currentContainerName,
								image:           img,
								repo:            repo,
								isMainContainer: isMain,
							}
							containers = append(containers, container)
						}
					} else if val.Kind == yaml.MappingNode {
						// Format: image: { repo: parity/polkadot, tag: stable2506-2 }
						var repo, tag string
						for j := 0; j < len(val.Content)-1; j += 2 {
							subKey := val.Content[j]
							subVal := val.Content[j+1]
							if subKey.Value == "repo" && subVal.Kind == yaml.ScalarNode {
								repo = subVal.Value
							} else if subKey.Value == "tag" && subVal.Kind == yaml.ScalarNode {
								tag = subVal.Value
							}
						}
						
						if repo != "" {
							img := repo
							if tag != "" {
								img = repo + ":" + tag
							}
							
							isMain := isMainContainer(currentContainerName, repo)
									container := containerInfo{
								name:            currentContainerName,
								image:           img,
								repo:            repo,
								isMainContainer: isMain,
							}
							containers = append(containers, container)
						}
					}
				}
				
				walk(val, depth+1)
			}
		case yaml.SequenceNode:
			for _, item := range n.Content {
				walk(item, depth+1)
			}
		}
	}
	
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		walk(root.Content[0], 0)
	} else {
		walk(&root, 0)
	}
	
	// Filter to only main application containers
	var mainRepos []string
	for _, container := range containers {
		if container.isMainContainer {
			// Avoid duplicates
			found := false
			for _, existing := range mainRepos {
				if existing == container.repo {
					found = true
					break
				}
			}
			if !found {
				mainRepos = append(mainRepos, container.repo)
			}
		}
	}
	
	return mainRepos
}

func isMainContainer(containerName, imageRepo string) bool {
	containerName = strings.ToLower(containerName)
	imageRepo = strings.ToLower(imageRepo)
	
	// Known blockchain node repositories that should be updated
	knownMainRepos := map[string]bool{
		"parity/polkadot":     true,
		"parity/kusama":       true,
		"paritytech/polkadot": true,
		"paritytech/kusama":   true,
		"ethereum/client-go":  true,
		"hyperledger/fabric":  true,
	}
	
	if knownMainRepos[imageRepo] {
		return true
	}
	
	// Sidecar/utility containers that should NOT be updated
	sidecarPatterns := []string{
		"filebeat", "fluentd", "logstash", "fluent-bit",
		"prometheus", "grafana", "jaeger", "zipkin",
		"nginx", "envoy", "istio", "linkerd",
		"vault", "consul", "redis", "memcached",
		"postgres", "mysql", "mongodb",
		"busybox", "alpine", "ubuntu", "centos",
		"pause", "k8s.gcr.io/pause",
		"cloudflare", "certbot",
	}
	
	for _, pattern := range sidecarPatterns {
		if strings.Contains(imageRepo, pattern) || strings.Contains(containerName, pattern) {
			return false
		}
	}
	
	// Check if container name suggests it's a main application
	mainContainerPatterns := []string{
		"polkadot", "kusama", "node", "validator", "archive",
		"ethereum", "geth", "consensus", "execution",
	}
	
	for _, pattern := range mainContainerPatterns {
		if strings.Contains(containerName, pattern) || strings.Contains(imageRepo, pattern) {
			return true
		}
	}
	
	// Default to false for safety - only update containers we're confident about
	return false
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

	var filesToUpdate []struct {
		owner string
		repo  string
		path  string
	}

	for _, project := range matchingProjects {
		for _, path := range project.Paths {
			filesToUpdate = append(filesToUpdate, struct {
				owner string
				repo  string
				path  string
			}{
				owner: project.Owner,
				repo:  project.Name,
				path:  path,
			})
		}
	}

	client, err := h.createGitHubClient(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to create GitHub client: %v", err)
	}

	imageToTag := make(map[string]string)

	if req.ReleaseTag != "" {
		for _, f := range filesToUpdate {
			file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
			if ferr != nil || file == nil {
				continue
			}
			content, cerr := file.GetContent()
			if cerr != nil {
				continue
			}
			images := extractImageReposFromYAML(content)
			for _, img := range images {
				imageToTag[img] = req.ReleaseTag
			}
		}
	} else {
		dockerResult, err := h.fetchLatestStableTags(ctx, client, filesToUpdate)
		if err != nil {
			return result, err
		}
		imageToTag = dockerResult.ImageToTag
	}

	filesToCommit, upgrades, err := h.prepareFileUpdates(ctx, client, filesToUpdate, imageToTag)
	if err != nil {
		return result, err
	}

	if len(filesToCommit) == 0 {
		return result, fmt.Errorf("no files needed updating")
	}

	owner := filesToCommit[0].owner
	repo := filesToCommit[0].repo

	commit, err := h.createCommitFromFiles(ctx, client, owner, repo, "main", filesToCommit, req.CommitMessage)
	if err != nil {
		return result, err
	}

	branchName := fmt.Sprintf("%s-%d", req.BranchPrefix, time.Now().Unix())
	pullRequest, err := h.createBranchAndPR(ctx, client, owner, repo, commit, branchName, req.PRTitle, req.PRBody)
	if err != nil {
		return result, err
	}

	result.PRUrl = *pullRequest.HTMLURL
	result.CommitURL = fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, *commit.SHA)
	result.ImageUpgrades = upgrades
	result.Success = true

	for _, f := range filesToCommit {
		result.UpdatedFiles = append(result.UpdatedFiles, f.path)
	}

	return result, nil
}

func (h *GitHubDeployHandler) fetchLatestStableTags(ctx context.Context, client *github.Client, filesToUpdate []struct{ owner, repo, path string }) (*dockerTagResult, error) {
	imageToTag := make(map[string]string)

	for _, f := range filesToUpdate {
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			continue
		}
		images := extractImageReposFromYAML(content)
		for _, img := range images {
			imageToTag[img] = ""
		}
	}

	// get latest stable tags
	type dockerTag struct {
		Name        string `json:"name"`
		LastUpdated string `json:"last_updated"`
	}
	type dockerTagsResp struct {
		Results []dockerTag `json:"results"`
	}

	for img := range imageToTag {
		parts := strings.Split(img, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid image name: %s", img)
		}

		url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/%s/tags?page_size=100", parts[0], parts[1])
		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch Docker tags for %s: %v", img, err)
		}

		var tagsResp dockerTagsResp
		if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to parse Docker tags JSON for %s: %v", img, err)
		}
		resp.Body.Close()

		var stableTags []dockerTag
		for _, tag := range tagsResp.Results {
			if strings.HasPrefix(tag.Name, "stable") {
				stableTags = append(stableTags, tag)
			}
		}

		if len(stableTags) == 0 {
			return nil, fmt.Errorf("no stable tags found for %s", img)
		}

		sort.Slice(stableTags, func(i, j int) bool {
			return stableTags[i].LastUpdated > stableTags[j].LastUpdated
		})

		imageToTag[img] = stableTags[0].Name
	}

	return &dockerTagResult{ImageToTag: imageToTag}, nil
}

func (h *GitHubDeployHandler) prepareFileUpdates(ctx context.Context, client *github.Client, filesToUpdate []struct{ owner, repo, path string }, imageToTag map[string]string) ([]fileCommitData, []imageUpgrade, error) {
	var filesToCommit []fileCommitData
	var upgrades []imageUpgrade

	for _, f := range filesToUpdate {
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			continue
		}

		var oldToNew []imageUpgrade

		newYAML, updated, uerr := updateAllImageTagsYAML(content, imageToTag)
		if uerr != nil || !updated {
			continue
		}

		filesToCommit = append(filesToCommit, fileCommitData{
			owner:   f.owner,
			repo:    f.repo,
			path:    f.path,
			sha:     *file.SHA,
			newYAML: newYAML,
		})
		upgrades = append(upgrades, oldToNew...)
	}

	return filesToCommit, upgrades, nil
}

func (h *GitHubDeployHandler) createCommitFromFiles(ctx context.Context, client *github.Client, owner, repo, branch string, filesToCommit []fileCommitData, commitMsg string) (*github.Commit, error) {
	ref, _, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %v", err)
	}

	baseCommit, _, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get base commit: %v", err)
	}

	var entries []*github.TreeEntry
	for _, f := range filesToCommit {
		blob, _, err := client.Git.CreateBlob(ctx, owner, repo, &github.Blob{
			Content:  github.Ptr(f.newYAML),
			Encoding: github.Ptr("utf-8"),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create blob for %s: %v", f.path, err)
		}

		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(f.path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  blob.SHA,
		})
	}

	tree, _, err := client.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, entries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %v", err)
	}

	now := time.Now()
	author := &github.CommitAuthor{
		Name:  github.Ptr("nodeoperator-ai"),
		Email: github.Ptr("agent@nodeoperator.ai"),
		Date:  &github.Timestamp{Time: now},
	}

	newCommit := &github.Commit{
		Message:   github.Ptr(commitMsg),
		Tree:      tree,
		Parents:   []*github.Commit{baseCommit},
		Author:    author,
		Committer: author,
	}

	commit, _, err := client.Git.CreateCommit(ctx, owner, repo, newCommit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %v", err)
	}

	return commit, nil
}

func (h *GitHubDeployHandler) agentUpdatePR(ctx context.Context, payload ReleasesWebhookPayload, summary *AgentSummary) (string, error) {
	var releaseTag string
	for _, release := range payload.Releases {
		releaseTag = release.TagName
		break
	}

	if releaseTag == "" {
		return "", fmt.Errorf("no release tag found in payload")
	}

	var networkName string
	for _, repo := range payload.Repositories {
		if repo.NetworkName != "" {
			networkName = repo.NetworkName
			break
		}
	}

	// Creating PR
	prTitle, prBody, commitMessage := BuildPRContent(networkName, releaseTag, summary)

	req := NetworkUpdateRequest{
		DetectedNetworks: summary.DetectedNetworks,
		ReleaseTag:       releaseTag,
		CommitMessage:    commitMessage,
		PRTitle:          prTitle,
		PRBody:           prBody,
		BranchPrefix:     "ponos-ai-update",
	}

	result, err := h.updateNetworkImages(ctx, req)
	if err != nil {
		return "", err
	}

	return result.PRUrl, nil
}

func updateAllImageTagsYAML(yamlContent string, repoToTag map[string]string) (string, bool, error) {
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlContent), &root)
	if err != nil {
		return "", false, err
	}
	var updated bool
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
				if key.Value == "image" {
					if val.Kind == yaml.ScalarNode {
						// Handle simple format: image: "parity/polkadot:stable2503-9"
						img := val.Value
						if idx := strings.Index(img, ":"); idx > 0 {
							repo := img[:idx]
							if tag, ok := repoToTag[repo]; ok {
								newVal := repo + ":" + tag
								if val.Value != newVal {
									val.Value = newVal
									updated = true
								}
							}
						}
					} else if val.Kind == yaml.MappingNode {
						// Handle mapped format: image: { repo: parity/polkadot, tag: stable2503-9 }
						var repo, currentTag string
						var tagNode *yaml.Node
						
						// Find repo and tag fields
						for j := 0; j < len(val.Content)-1; j += 2 {
							subKey := val.Content[j]
							subVal := val.Content[j+1]
							if subKey.Value == "repo" && subVal.Kind == yaml.ScalarNode {
								repo = subVal.Value
							} else if subKey.Value == "tag" && subVal.Kind == yaml.ScalarNode {
								currentTag = subVal.Value
								tagNode = subVal
							}
						}
						
						// Update tag if we have a new one for this repo
						if repo != "" && tagNode != nil {
							if newTag, ok := repoToTag[repo]; ok && newTag != currentTag {
								tagNode.Value = newTag
								updated = true
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
	if !updated {
		return yamlContent, false, nil
	}
	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)
	defer encoder.Close()
	err = encoder.Encode(&root)
	if err != nil {
		return "", false, err
	}
	return strings.TrimRight(b.String(), "\n") + "\n", true, nil
}
