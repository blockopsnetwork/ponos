package main

import (
	"fmt"
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
	messageText.WriteString(fmt.Sprintf("*Published:* %s\n\n", release.PublishedAt))

	messageText.WriteString(fmt.Sprintf(":memo: *AI Generated Release Summary*\n%s\n\n", summary.ReleaseSummary))

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

func BuildPRContent(networkName, releaseTag, botName string, summary *AgentSummary) (title, body, commitMessage string) {
	if botName == "" {
		botName = "Ponos"
	}
	
	title = fmt.Sprintf("%s: Update %s to %s", botName, networkName, releaseTag)
	
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
- 🔍 **AI Analysis:** Comprehensive release analysis performed
- ⚡ **Action Required:** Review and decide whether to merge or close

*This PR was automatically created by %s. The AI has analyzed the release and provided recommendations above.*`,
		botName,
		summary.ReleaseSummary,
		summary.ConfigChangesNeeded,
		summary.RiskAssessment,
		strings.ToUpper(summary.Severity),
		botName,
		botName)
	
	commitMessage = fmt.Sprintf("🤖 %s: Update %s to %s\n\n%s", botName, networkName, releaseTag, summary.ReleaseSummary)
	
	return title, body, commitMessage
}

func BuildAIAnalysisPrompt(payload ReleasesWebhookPayload) string {
	var repoInfo, releaseInfo string

	for _, repo := range payload.Repositories {
		repoInfo += fmt.Sprintf("Repository: %s/%s (%s)\nNetwork: %s\nClient Type: %s\n",
			repo.Owner, repo.Name, repo.DisplayName, repo.NetworkName, repo.ClientType)
	}

	for key, release := range payload.Releases {
		releaseInfo += fmt.Sprintf("Release: %s\nTag: %s\nName: %s\nPrerelease: %t\nPublished: %s\n\nRELEASE NOTES:\n%s\n\n",
			key, release.TagName, release.Name, release.Prerelease, release.PublishedAt, release.Body)
	}

	prompt := fmt.Sprintf(`You are NodeOperator.ai, an expert blockchain infrastructure AI agent specializing in blockchain node operations and updates.

RELEASE INFORMATION:
%s

REPOSITORY INFORMATION:
%s

TASK: Analyze this blockchain node release comprehensively and provide detailed recommendations.

Please analyze the provided RELEASE NOTES carefully and provide a structured analysis covering:

RELEASE SUMMARY: Based on the actual release notes, what are the specific key changes, features, or fixes in this release? What specific problems does it solve? Reference specific components, modules, or improvements mentioned in the release notes.

NETWORK IDENTIFICATION: Which blockchain network(s) is this for?

SEVERITY ASSESSMENT: Rate as low/medium/high/critical based on:
   - Security fixes
   - Performance improvements  
   - Breaking changes
   - Network compatibility

CONFIGURATION CHANGES: Based on the release notes, are there any specific configuration file updates, environment variable changes, runtime parameter modifications, or deployment changes needed? Look for breaking changes, new features requiring config, or deprecated settings. If none mentioned, state "No configuration changes mentioned in release notes".

RISK ASSESSMENT: What are the potential risks of updating vs not updating? Consider downtime, compatibility, rollback complexity.

Provide detailed analysis to help node operators make an informed decision about this release.`, releaseInfo, repoInfo)

	return prompt
}

// Slack block utility functions
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
