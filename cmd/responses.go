package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/slack-go/slack"
)

type SlashCommandResponse struct {
	ResponseType string        `json:"response_type"`
	Text         string        `json:"text,omitempty"`
	Blocks       []slack.Block `json:"blocks,omitempty"`
}

func BuildReleaseNotificationBlocks(payload ReleasesWebhookPayload, summary *AgentSummary, prURL ...string) []slack.Block {
	var repo Repository
	var release ReleaseInfo

	if len(payload.Repositories) > 0 {
		repo = payload.Repositories[0]
	}

	for _, rel := range payload.Releases {
		release = rel
		break
	}

	severityIcons := map[string]string{
		"critical": ":rotating_light:",
		"high":     ":warning:",
		"medium":   ":information_source:",
		"low":      ":white_check_mark:",
	}

	icon := severityIcons[summary.Severity]
	if icon == "" {
		icon = ":warning:"
	}

	var messageText strings.Builder
	messageText.WriteString(fmt.Sprintf("%s *Ponos Release Alert*\n\n", icon))

	messageText.WriteString(fmt.Sprintf("*Repository:* %s/%s\n", repo.Owner, repo.Name))
	messageText.WriteString(fmt.Sprintf("*Network:* %s\n", repo.NetworkName))
	messageText.WriteString(fmt.Sprintf("*Client:* %s\n", repo.ClientType))
	messageText.WriteString(fmt.Sprintf("*Tag:* %s\n", release.TagName))
	if repo.DockerTag != "" && !strings.EqualFold(repo.DockerTag, release.TagName) {
		messageText.WriteString(fmt.Sprintf("*Docker Tag:* %s\n", repo.DockerTag))
	}
	messageText.WriteString(fmt.Sprintf("*Published:* %s\n\n", release.PublishedAt))

	releaseSummary := strings.TrimSpace(summary.ReleaseSummary)
	if releaseSummary == "" || strings.EqualFold(releaseSummary, "Not specified") {
		if release.Body != "" {
			bodyPreview := release.Body
			if len(bodyPreview) > 600 {
				bodyPreview = bodyPreview[:600] + "\n\n…"
			}
			releaseSummary = fmt.Sprintf("Summary derived from GitHub release notes:\n%s", bodyPreview)
		} else {
			releaseSummary = "Upgrade generated without additional release analysis details."
		}
	}

	messageText.WriteString(fmt.Sprintf(":memo: *AI Generated Release Summary*\n%s\n\n", releaseSummary))

	messageText.WriteString(":gear: *Next Steps*\n")
	messageText.WriteString("- PR created → review/merge required.\n")
	if summary.ConfigChangesNeeded != "" && summary.ConfigChangesNeeded != "Not specified" {
		messageText.WriteString(fmt.Sprintf("- Config changes: %s\n", summary.ConfigChangesNeeded))
	} else {
		messageText.WriteString("- No config changes noted.\n")
	}
	messageText.WriteString("\n")

	messageText.WriteString(fmt.Sprintf(":warning: *Risk*\n- %s: %s\n\n",
		strings.Title(summary.Severity), summary.RiskAssessment))

	if len(prURL) > 0 && prURL[0] != "" {
		messageText.WriteString(fmt.Sprintf(":link: *Pull Request:* <%s|View PR>", prURL[0]))
	}

	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, messageText.String(), false, false),
			nil, nil,
		),
	}
}

func extractVersionTag(aiResponse string) string {
	re := regexp.MustCompile(`"(v?\d+\.\d+\.\d+[^"]*)"`)
	if matches := re.FindStringSubmatch(aiResponse); len(matches) > 1 {
		return matches[1]
	}

	re = regexp.MustCompile(`\b(v?\d+\.\d+\.\d+(?:\.\d+)?(?:-[a-zA-Z0-9\-\.]+)?)\b`)
	if matches := re.FindStringSubmatch(aiResponse); len(matches) > 1 {
		return matches[1]
	}

	return aiResponse
}

func BuildPRContent(networkName, releaseTag, botName string, summary *AgentSummary, release *ReleaseInfo) (title, body, commitMessage string) {
	if botName == "" {
		botName = "Ponos"
	}

	cleanReleaseTag := extractVersionTag(releaseTag)
	if summary.PRTitle != "" {
		title = summary.PRTitle
	} else {
		title = fmt.Sprintf("%s: Update %s to %s", botName, networkName, cleanReleaseTag)
	}

	releaseSummary := strings.TrimSpace(summary.ReleaseSummary)
	if releaseSummary == "" || strings.EqualFold(releaseSummary, "Not specified") {
		if release != nil && release.Body != "" {
			bodyPreview := release.Body
			if len(bodyPreview) > 1000 {
				bodyPreview = bodyPreview[:1000] + "\n\n…"
			}
			releaseSummary = fmt.Sprintf("Summary derived from GitHub release notes:\n%s", bodyPreview)
		} else {
			releaseSummary = fmt.Sprintf("Upgrade %s to %s based on latest release information.", networkName, cleanReleaseTag)
		}
	}

	configChanges := strings.TrimSpace(summary.ConfigChangesNeeded)
	if configChanges == "" || strings.EqualFold(configChanges, "Not specified") {
		configChanges = "Updated Docker image tags to reference the latest stable release."
	}

	riskAssessment := strings.TrimSpace(summary.RiskAssessment)
	if riskAssessment == "" || strings.EqualFold(riskAssessment, "Not specified") {
		riskAssessment = "Review release notes and run smoke tests before promoting to production."
	}

	body = fmt.Sprintf(`## 🤖 Automated Update by %s

**NodeOperator AI Analysis:**
%s

**Configuration Changes:**
%s

**Risk Assessment:**
%s

**Severity:** %s

---
**About this PR:**
- 🤖 **Created by:** %s Bot
- 🔍 **Node Operator Agent Analysis:** Comprehensive release analysis performed
- ⚡ **Action Required:** Review and decide whether to merge or close

*This PR was automatically created by %s. The AI has analyzed the release and provided recommendations above.*`,
		botName,
		releaseSummary,
		configChanges,
		riskAssessment,
		strings.ToUpper(summary.Severity),
		botName,
		botName)

	commitMessage = fmt.Sprintf("🤖 %s: Update %s to %s\n\n%s", botName, networkName, cleanReleaseTag, releaseSummary)

	return title, body, commitMessage
}

func createStatusBlock(icon, title string) slack.Block {
	return slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("%s *%s*", icon, title), false, false),
		nil, nil,
	)
}

func createInfoBlock(text string) slack.Block {
	return slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, text, false, false),
		nil, nil,
	)
}

func createProgressBlock() slack.Block {
	return slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, "_I'll keep you updated on the progress._", false, false),
		nil, nil,
	)
}

func createDeploymentStartBlocks(service, branch, environment, userID string) []slack.Block {
	return []slack.Block{
		createStatusBlock(":rocket:", "Deployment started!"),
		createInfoBlock(fmt.Sprintf("*Service:* %s\n*Branch:* %s\n*Environment:* %s\n*Requested by:* <@%s>",
			service, branch, environment, userID)),
		createProgressBlock(),
	}
}

func createUpdateStartBlocks(chain, userID string) []slack.Block {
	return []slack.Block{
		createStatusBlock(":rocket:", "Network update started!"),
		createInfoBlock(fmt.Sprintf("*Network:* %s\n*Requested by:* <@%s>", chain, userID)),
		createProgressBlock(),
	}
}

func createErrorBlocks(title, message string) []slack.Block {
	return []slack.Block{
		createStatusBlock(":x:", title),
		createInfoBlock(message),
	}
}

func createSuccessBlocks(title, details string) []slack.Block {
	return []slack.Block{
		createStatusBlock(":white_check_mark:", title),
		createInfoBlock(details),
	}
}
