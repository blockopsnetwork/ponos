package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type DockerOperations struct{}

func NewDockerOperations() *DockerOperations {
	return &DockerOperations{}
}

func (d *DockerOperations) FetchLatestStableTagsMCP(ctx context.Context, mcpClient *GitHubMCPClient, agent *NodeOperatorAgent, filesToUpdate []fileInfo, network, client string) (*dockerTagResult, error) {
	imageToTag := make(map[string]string)

	for _, f := range filesToUpdate {
		content, ferr := mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
		if ferr != nil {
			continue
		}
		images := d.extractImageReposWithLLM(ctx, agent, content)
		for _, img := range images {
			imageToTag[img] = ""
		}
	}

	// Fetch Docker tags from node-releases API instead of Docker Hub
	for img := range imageToTag {
		tag, err := d.fetchLatestTagFromNodeReleases(network, client)
		if err != nil {
			continue
		}
		imageToTag[img] = tag
	}

	return &dockerTagResult{ImageToTag: imageToTag}, nil
}

func (d *DockerOperations) fetchLatestTagFromNodeReleases(network, client string) (string, error) {
	// Call node-releases API with specific network and client filters
	url := fmt.Sprintf("https://api.nodereleases.com/releases?network=%s&client=%s", network, client)

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("node-releases API returned status %d", resp.StatusCode)
	}

	var apiResp struct {
		Releases []struct {
			Repository string `json:"repository"`
			Metadata   struct {
				DockerRepo   string `json:"docker_repo"`
				DockerHubTag string `json:"dockerhub_tag"`
			} `json:"metadata"`
		} `json:"releases"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	// Return the docker tag from the first matching release
	for _, release := range apiResp.Releases {
		if release.Metadata.DockerHubTag != "" {
			return release.Metadata.DockerHubTag, nil
		}
	}

	return "", fmt.Errorf("docker tag not found for network=%s client=%s", network, client)
}

func (d *DockerOperations) extractImageReposWithLLM(ctx context.Context, agent *NodeOperatorAgent, yamlContent string) []string {
	if agent != nil {
		if llmRepos, err := agent.AnalyzeYAMLForBlockchainContainers(ctx, yamlContent); err == nil && len(llmRepos) > 0 {
			return llmRepos
		}
	}

	yamlOps := NewYAMLOperations()
	return yamlOps.ExtractImageReposFromYAML(yamlContent)
}
