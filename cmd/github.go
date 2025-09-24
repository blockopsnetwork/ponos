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
	DeployAPICmd              = "/deploy-api"
	DeployProxyCmd            = "/deploy-proxy"
	UpdatePolkadotToLatestCmd = "/update-chain"
	UpdateNetworkCmd          = "/update-network"

	APIRepo = "blockops-sh/api-core-service"
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

func NewGitHubDeployHandler(bot *Bot) *GitHubDeployHandler {
	pemKey, err := os.ReadFile(bot.config.GitHubPEMKey)
	if err != nil {
		panic(fmt.Sprintf("failed to read GitHub PEM key file: %s %v", bot.config.GitHubPEMKey, err))
	}

	return &GitHubDeployHandler{
		bot: bot,
		repoConfigs: map[string]RepoConfig{
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
	case DeployAPICmd:
		service = "api"
	case DeployProxyCmd:
		service = "proxy"
	}

	h.bot.logger.Info("deployment requested",
		"service", service,
		"branch", branch,
		"environment", environment,
		"user", userID)

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
		h.bot.logger.Error("failed to load project config", "error", err)
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

	imageToTag := make(map[string]string)              // image repo -> latest stable tag
	fileImages := make([][]string, len(filesToUpdate)) // images found in each file

	for i, f := range filesToUpdate {
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			continue
		}
		images := extractImageReposFromYAML(content)
		fileImages[i] = images
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
	type dockerTag struct {
		Name        string `json:"name"`
		LastUpdated string `json:"last_updated"`
	}
	type dockerTagsResp struct {
		Results []dockerTag `json:"results"`
	}

	projectConfig, err := config.LoadProjectConfig("config.yaml")
	if err != nil {
		h.bot.logger.Error("failed to load project config", "error", err)
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

	imageToTag := make(map[string]string)              // image repo -> latest stable tag
	fileImages := make([][]string, len(filesToUpdate)) // images found in each file

	for i, f := range filesToUpdate {
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			continue
		}
		images := extractImageReposFromYAML(content)
		fileImages[i] = images
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
	
	// FOR TESTING: Override all tags with "latest"
	for repo := range imageToTag {
		imageToTag[repo] = "latest"
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, ":white_check_mark: *Network update completed!*", false, false),
			nil,
			nil,
		),
	}

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

	commitMsg := "ponos: update all network image tags to latest stable"
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

	// Create PR using helper function
	newBranch := fmt.Sprintf("ponos/update-network-%d", time.Now().Unix())
	prTitle := "Update network image tags to latest stable"
	prBody := fmt.Sprintf("Automated update of network Docker image tags to latest stable versions.\n\nCommit: %s", *commit.SHA)
	
	pullRequest, err := h.createBranchAndPR(ctx, client, owner, repo, commit, newBranch, prTitle, prBody)
	if err != nil {
		h.bot.logger.Error("Failed to create branch and PR", "error", err)
		h.notifyError("rpc-updates", "Failed to create branch and PR: "+err.Error())
		return
	}

	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType,
			fmt.Sprintf("*Pull Request:* <%s|#%d>", *pullRequest.HTMLURL, *pullRequest.Number),
			false, false),
		nil, nil,
	))
	
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType,
			fmt.Sprintf("*Branch:* `%s`\n*Commit:* `%s`", newBranch, (*commit.SHA)[:7]),
			false, false),
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
				if key.Value == "image" && val.Kind == yaml.ScalarNode {
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
