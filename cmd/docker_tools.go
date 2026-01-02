package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type DockerOperations struct{}

func NewDockerOperations() *DockerOperations {
	return &DockerOperations{}
}

func (d *DockerOperations) FetchLatestStableTagsMCP(ctx context.Context, mcpClient *GitHubMCPClient, agent AgentClient, filesToUpdate []fileInfo, network, client string) (*dockerTagResult, error) {
	imageToTag := make(map[string]string)

	for _, f := range filesToUpdate {
		content, ferr := mcpClient.GetFileContent(ctx, f.owner, f.repo, f.path)
		if ferr != nil {
			continue
		}
		images, err := agent.ExtractImages(ctx, content)
		if err != nil {
			continue
		}
		for _, img := range images {
			imageToTag[img] = ""
		}
	}

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
	baseURL := os.Getenv("NODE_RELEASES_API_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.nodereleases.com"
	}

	releasesURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/releases")
	if err != nil {
		return "", fmt.Errorf("invalid NODE_RELEASES_API_BASE_URL %q: %w", baseURL, err)
	}

	query := releasesURL.Query()
	query.Set("network", strings.ToLower(network))
	if client != "" {
		query.Set("client_type", strings.ToLower(client))
	}
	releasesURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctxWithTimeout(), http.MethodGet, releasesURL.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ponos/1.0")

	resp, err := http.DefaultClient.Do(req)
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

	if len(apiResp.Releases) == 0 {
		if client != "" {
			return d.fetchLatestTagFromNodeReleases(network, "")
		}
		return "", fmt.Errorf("node-releases API returned no releases for network=%s", network)
	}

	for _, release := range apiResp.Releases {
		if release.Metadata.DockerHubTag != "" {
			return release.Metadata.DockerHubTag, nil
		}
	}

	if client != "" {
		return d.fetchLatestTagFromNodeReleases(network, "")
	}

	return "", fmt.Errorf("docker tag not found for network=%s client=%s", network, client)
}

func ctxWithTimeout() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 15*time.Second)
	return ctx
}
