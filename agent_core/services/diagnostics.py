"""Diagnostics workflow orchestrated via MCP servers."""

from __future__ import annotations

import logging
from datetime import datetime
from typing import Any, Dict, Optional, Tuple
import json

from ..config import config
from ..mcp.client import MCPConnectionManager
from .prompts import DIAGNOSE_PROMPT

logger = logging.getLogger(__name__)


class DiagnosticsService:
    """Run lightweight diagnostics by leveraging Slack/GitHub MCP servers."""

    def __init__(self) -> None:
        self.default_slack_channel = (
            config.SLACK_DIAGNOSTICS_CHANNEL
            or config.DEFAULT_SLACK_CHANNEL
            or ""
        )

    async def run_diagnostics(
        self,
        service: str,
        slack_channel: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Connect to MCP servers, post a diagnostics notification, and return metadata."""

        target_channel = slack_channel or self.default_slack_channel
        if not target_channel:
            raise ValueError(
                "No Slack channel specified for diagnostics. Set SLACK_DIAGNOSTICS_CHANNEL or provide slack_channel."
            )

        owner = config.DIAGNOSTICS_GITHUB_OWNER
        repo = config.DIAGNOSTICS_GITHUB_REPO
        if not owner or not repo:
            raise ValueError(
                "DIAGNOSTICS_GITHUB_OWNER/DIAGNOSTICS_GITHUB_REPO must be set to create diagnostics issues."
            )

        async with MCPConnectionManager() as manager:
            await manager.connect_default_servers()

            issue_title = f"[Diagnostics] {service} - {datetime.utcnow().isoformat(timespec='seconds')}"
            issue_body = (
                "## Automated Diagnostics Request\n"
                f"Service: `{service}`\n"
                f"Triggered: `{datetime.utcnow().isoformat(timespec='seconds')} UTC`\n\n"
                "This issue was created automatically by the diagnostics workflow."
            )

            github_payload = {
                "owner": owner,
                "repo": repo,
                "title": issue_title,
                "body": issue_body,
            }

            logger.info("Creating diagnostics issue in %s/%s", owner, repo)
            github_result = await manager.call_tool("create_issue", github_payload)

            issue_url = self._extract_issue_url(github_result)
            issue_number = self._extract_issue_number(github_result)

        log_comment, log_snippet = await self._collect_logs(manager, service)

        if log_comment and issue_number is not None:
            await self._post_issue_comment(
                manager,
                owner,
                repo,
                issue_number,
                log_comment,
            )

        resource_comment = await self._collect_resource_description(manager, service)
        if resource_comment and issue_number is not None:
            await self._post_issue_comment(
                manager,
                owner,
                repo,
                issue_number,
                resource_comment,
            )

        events_comment = await self._collect_recent_events(manager, service)
        if events_comment and issue_number is not None:
            await self._post_issue_comment(
                manager,
                owner,
                repo,
                issue_number,
                events_comment,
            )

        namespace_instruction = ""
        prompt = DIAGNOSE_PROMPT.format(
            service=service,
            namespace_instruction=namespace_instruction,
            project_root=config.DIAGNOSTICS_PROJECT_ROOT or "<project_root>",
                repo_name=repo,
                organisation=owner,
                slack_channel_id=target_channel,
            )

            message_lines = [
                f"Diagnostics initiated for *{service}*.",
            ]
            if issue_url:
                message_lines.append(f"GitHub issue: {issue_url}")
            if log_attachment:
                message_lines.append(f"Log snapshot:\n```{log_attachment}```")

            message_lines.append("\nPrompt issued to diagnostics agent:\n")
            message_lines.append(f"```{prompt}```")
            message = "\n".join(message_lines)

            slack_payload = {
                "slack_channel_id": target_channel,
                "text": message,
            }

            logger.info(
                "Posting diagnostics notification to Slack channel %s", target_channel
            )

            result = await manager.call_tool("slack_post_message", slack_payload)

            return {
                "service": service,
                "slack_channel": target_channel,
                "slack_result": result,
                "github_issue": github_result,
                "issue_url": issue_url,
                "issue_number": issue_number,
                "log_snippet": log_snippet,
                "resource_description": resource_comment,
                "events_summary": events_comment,
                "prompt": prompt,
            }

    @staticmethod
    def _extract_issue_url(result: Dict[str, Any]) -> Optional[str]:
        """Attempt to parse the GitHub issue HTML URL from the MCP response."""

        content = result.get("content", [])
        for block in content:
            text = block.get("text") if isinstance(block, dict) else None
            if not text:
                continue
            try:
                data = json.loads(text)
            except json.JSONDecodeError:
                continue

            if isinstance(data, dict):
                url = data.get("html_url") or data.get("url")
                if isinstance(url, str):
                    return url

        return None

    @staticmethod
    def _extract_issue_number(result: Dict[str, Any]) -> Optional[int]:
        content = result.get("content", [])
        for block in content:
            text = block.get("text") if isinstance(block, dict) else None
            if not text:
                continue
            try:
                data = json.loads(text)
            except json.JSONDecodeError:
                continue
            if isinstance(data, dict) and isinstance(data.get("number"), int):
                return data["number"]
        return None

    async def _post_issue_comment(
        self,
        manager: MCPConnectionManager,
        owner: str,
        repo: str,
        issue_number: int,
        comment: str,
    ) -> None:
        if not manager.has_tool("add_issue_comment"):
            return

        payload = {
            "owner": owner,
            "repo": repo,
            "number": issue_number,
            "body": comment,
        }

        try:
            await manager.call_tool("add_issue_comment", payload)
        except Exception as exc:  # pragma: no cover
            logger.warning("Failed to append diagnostics comment", exc_info=exc)

    async def _collect_logs(
        self,
        manager: MCPConnectionManager,
        service: str,
    ) -> Tuple[Optional[str], Optional[str]]:
        if not manager.has_tool("get_logs") or not config.KUBERNETES_MCP_URL:
            return None, None

        args = {
            "resourceType": config.DIAGNOSTICS_RESOURCE_TYPE,
            "name": service,
            "namespace": config.DIAGNOSTICS_DEFAULT_NAMESPACE,
            "tail": config.DIAGNOSTICS_LOG_TAIL,
        }

        try:
            result = await manager.call_tool("get_logs", args)
        except Exception as exc:  # pragma: no cover
            logger.warning("Failed to retrieve logs via MCP", exc_info=exc)
            return None, None

        logs = self._extract_primary_text(result)
        if not logs:
            return None, None

        snippet_lines = min(40, len(logs.splitlines()))
        lines = logs.splitlines()[-snippet_lines:]
        snippet = "\n".join(lines)

        comment = (
            "## Log Snapshot\n"
            f"Namespace: `{config.DIAGNOSTICS_DEFAULT_NAMESPACE}`\n"
            f"Resource: `{config.DIAGNOSTICS_RESOURCE_TYPE}/{service}`\n\n"
            f"```\n{snippet}\n```"
        )

        return comment, snippet

    async def _collect_resource_description(
        self,
        manager: MCPConnectionManager,
        service: str,
    ) -> Optional[str]:
        """Retrieve resource description via MCP if the tool is available."""

        tool_map = {
            "deployment": "describe_deployment",
            "pod": "describe_pod",
            "service": "describe_service",
        }

        tool_name = tool_map.get(config.DIAGNOSTICS_RESOURCE_TYPE.lower())
        if not tool_name or not manager.has_tool(tool_name):
            return None

        args = {
            "name": service,
            "namespace": config.DIAGNOSTICS_DEFAULT_NAMESPACE,
        }

        try:
            result = await manager.call_tool(tool_name, args)
        except Exception as exc:  # pragma: no cover
            logger.warning("Failed to describe resource via MCP", exc_info=exc)
            return None

        description = self._extract_primary_text(result)
        if not description:
            return None

        return (
            "## Resource Description\n"
            f"Resource: `{config.DIAGNOSTICS_RESOURCE_TYPE}/{service}`\n\n"
            f"```\n{description}\n```"
        )

    async def _collect_recent_events(
        self,
        manager: MCPConnectionManager,
        service: str,
    ) -> Optional[str]:
        """Fetch recent Kubernetes events and format them for GitHub."""

        if not manager.has_tool("get_events") or not config.KUBERNETES_MCP_URL:
            return None

        args = {
            "namespace": config.DIAGNOSTICS_DEFAULT_NAMESPACE,
            "resourceName": service,
        }

        try:
            result = await manager.call_tool("get_events", args)
        except Exception as exc:  # pragma: no cover
            logger.warning("Failed to fetch events via MCP", exc_info=exc)
            return None

        events = self._extract_primary_text(result)
        if not events:
            return None

        return (
            "## Recent Events\n"
            f"Namespace: `{config.DIAGNOSTICS_DEFAULT_NAMESPACE}`\n"
            f"Resource filter: `{service}`\n\n"
            f"```\n{events}\n```"
        )

    @staticmethod
    def _extract_primary_text(result: Dict[str, Any]) -> Optional[str]:
        """Parse text payload from the MCP response."""
        content = result.get("content", [])
        for block in content:
            text = block.get("text") if isinstance(block, dict) else None
            if not text:
                continue
            try:
                data = json.loads(text)
            except json.JSONDecodeError:
                continue
            if isinstance(data, dict):
                for value in data.values():
                    if isinstance(value, str) and value.strip():
                        return value
            if isinstance(text, str) and text.strip():
                return text
        return None
