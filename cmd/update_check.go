package main

import (
	"encoding/json"
	"net/http"
	"os"
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
	if latest == version {
		return
	}
	program.Send(updateNotice{latest: latest})
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
