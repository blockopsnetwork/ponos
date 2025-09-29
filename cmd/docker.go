package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-github/v72/github"
)

type DockerOperations struct{}

func NewDockerOperations() *DockerOperations {
	return &DockerOperations{}
}

func (d *DockerOperations) FetchLatestStableTags(ctx context.Context, client *github.Client, filesToUpdate []fileInfo) (*dockerTagResult, error) {
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
		yamlOps := NewYAMLOperations()
		images := yamlOps.ExtractImageReposFromYAML(content)
		for _, img := range images {
			imageToTag[img] = ""
		}
	}

	for img := range imageToTag {
		parts := strings.Split(img, "/")
		if len(parts) != 2 {
			continue
		}
		namespace := parts[0]
		repo := parts[1]

		tag, err := d.fetchLatestStableTag(namespace, repo)
		if err != nil {
			continue
		}
		imageToTag[img] = tag
	}

	return &dockerTagResult{ImageToTag: imageToTag}, nil
}

func (d *DockerOperations) fetchLatestStableTag(namespace, repo string) (string, error) {
	url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/%s/tags/?page_size=100", namespace, repo)
	
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docker hub API returned status %d", resp.StatusCode)
	}

	var tagsResp dockerTagsResp
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return "", err
	}

	stableTagPattern := regexp.MustCompile(`^stable\d+(-\d+)*$`)
	var stableTags []string

	for _, tag := range tagsResp.Results {
		if stableTagPattern.MatchString(tag.Name) {
			stableTags = append(stableTags, tag.Name)
		}
	}

	if len(stableTags) == 0 {
		return "", fmt.Errorf("no stable tags found for %s/%s", namespace, repo)
	}

	sort.Slice(stableTags, func(i, j int) bool {
		return d.compareStableTags(stableTags[i], stableTags[j])
	})

	return stableTags[len(stableTags)-1], nil
}

func (d *DockerOperations) compareStableTags(tag1, tag2 string) bool {
	extract := func(tag string) []int {
		re := regexp.MustCompile(`\d+`)
		matches := re.FindAllString(tag, -1)
		var nums []int
		for _, match := range matches {
			var num int
			fmt.Sscanf(match, "%d", &num)
			nums = append(nums, num)
		}
		return nums
	}

	nums1 := extract(tag1)
	nums2 := extract(tag2)

	minLen := len(nums1)
	if len(nums2) < minLen {
		minLen = len(nums2)
	}

	for i := 0; i < minLen; i++ {
		if nums1[i] != nums2[i] {
			return nums1[i] < nums2[i]
		}
	}

	return len(nums1) < len(nums2)
}