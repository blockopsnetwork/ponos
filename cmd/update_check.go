package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (tui *PonosAgentTUI) checkForUpdates(program *tea.Program) {
	if program == nil {
		return
	}
	if strings.TrimSpace(version) == "" || version == "dev" {
		return
	}
	if isEnvTruthy("PONOS_DISABLE_UPDATE_CHECK") {
		return
	}

	latest, err := fetchLatestTag()
	if err != nil || latest == "" {
		return
	}
	if isNewerVersion(version, latest) {
		program.Send(updateNotice{latest: latest})
	}
}

func isNewerVersion(current, latest string) bool {
	currentParts, ok := parseSemver(current)
	if !ok {
		return false
	}
	latestParts, ok := parseSemver(latest)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}
	return false
}

func parseSemver(value string) ([3]int, bool) {
	var out [3]int
	clean := strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(clean, ".")
	if len(parts) < 3 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		part := parts[i]
		if dash := strings.Index(part, "-"); dash >= 0 {
			part = part[:dash]
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func fetchLatestTag() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, githubAPIBase+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	var info releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	return info.TagName, nil
}

func isEnvTruthy(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	switch strings.ToLower(val) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}
