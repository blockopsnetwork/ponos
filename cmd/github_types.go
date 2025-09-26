package main

// Shared types used across GitHub operations

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