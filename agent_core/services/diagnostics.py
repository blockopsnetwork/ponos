"""Diagnostics workflow service for agent-core.

This module orchestrates diagnostics across configured MCP servers
(GitHub, Slack, Kubernetes). It is inspired by the SRE agent implementation
but adapted for this repository.
"""

from __future__ import annotations

import json
import logging
from datetime import datetime
from typing import Any, Callable, Dict, List, Optional, Tuple
import os

from ..config import config
from ..mcp.client import MCPConnectionManager

logger = logging.getLogger(__name__)


class DiagnosticsService:
    """Service responsible for running diagnostics workflows."""

    def __init__(self) -> None:
        self.logger = logger
        self.github_owner = config.DIAGNOSTICS_GITHUB_OWNER
        self.github_repo = config.DIAGNOSTICS_GITHUB_REPO
        self.default_namespace = config.DIAGNOSTICS_DEFAULT_NAMESPACE or "default"
        self.resource_type = config.DIAGNOSTICS_RESOURCE_TYPE or "deployment"
        self.log_tail = config.DIAGNOSTICS_LOG_TAIL or 10
        self.max_llm_iterations = int(os.getenv("DIAGNOSTICS_MAX_LLM_STEPS", "6"))

        allowed_tools_env = os.getenv("DIAGNOSTICS_ALLOWED_TOOLS")
        if allowed_tools_env:
            self.allowed_tools = {
                tool.strip()
                for tool in allowed_tools_env.split(",")
                if tool.strip()
            }
        else:
            self.allowed_tools = {
                "get_logs",
                "describe_deployment",
                "describe_pod",
                "get_events",
                "list_pods",
                "create_issue",
                "slack_post_message",
            }

        self.llm_client_getter: Optional[Callable[[], Any]] = None

    def configure(self, llm_client_getter: Optional[Callable[[], Any]] = None) -> None:
        """Configure runtime providers for diagnostics."""
        if llm_client_getter is not None:
            self.llm_client_getter = llm_client_getter

    async def run_diagnostics(
        self,
        service: str,
        slack_channel: Optional[str] = None,
        namespace: Optional[str] = None,
        resource_type: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Execute diagnostics for the requested service."""
        if not service:
            raise ValueError("Service name is required for diagnostics")

        effective_namespace = namespace or self.default_namespace
        effective_resource_type = (resource_type or self.resource_type or "deployment").lower()

        resolved_channel = (
            slack_channel
            or config.SLACK_DIAGNOSTICS_CHANNEL
            or config.DEFAULT_SLACK_CHANNEL
        )
        prompt = self._build_prompt(
            service,
            effective_namespace,
            resolved_channel,
            effective_resource_type,
        )

        result: Dict[str, Any] = {
            "service": service,
            "namespace": effective_namespace,
            "resource_type": effective_resource_type,
            "prompt": prompt,
            "timestamp": datetime.utcnow().isoformat(),
            "issue_url": "",
            "issue_number": 0,
            "slack_channel": resolved_channel or "",
            "slack_result": {},
            "log_snippet": "",
            "resource_description": "",
            "events_summary": "",
            "summary": "",
        }

        llm_client = self._get_llm_client()
        summary_text = ""
        llm_collected: Dict[str, Any] = {}

        async with MCPConnectionManager() as manager:
            await manager.connect_default_servers()

            if llm_client:
                try:
                    llm_output = await self._run_llm_session(
                        manager=manager,
                        llm_client=llm_client,
                        service=service,
                        namespace=effective_namespace,
                        resource_type=effective_resource_type,
                        slack_channel=resolved_channel,
                    )
                    summary_text = llm_output.get("summary", "") or ""
                    llm_collected = llm_output.get("collected", {}) or {}
                except Exception as exc:  # pragma: no cover - defensive logging
                    self.logger.exception("LLM diagnostics session failed", exc_info=exc)

            log_snippet = self._render_log_snippet(llm_collected.get("logs"))
            if not log_snippet:
                log_snippet = await self._fetch_log_snippet(
                    manager,
                    service,
                    effective_namespace,
                    effective_resource_type,
                )

            resource_description = (
                llm_collected.get("resource")
                or await self._describe_resource(
                    manager,
                    service,
                    effective_namespace,
                    effective_resource_type,
                )
            )
            events_summary = (
                llm_collected.get("events")
                or await self._fetch_events(
                    manager,
                    service,
                    effective_namespace,
                    effective_resource_type,
                )
            )

            issue_data = llm_collected.get("issue") or {}
            if issue_data:
                result["issue_url"] = issue_data.get("html_url") or issue_data.get("url", "")
                number = issue_data.get("number")
                if isinstance(number, int):
                    result["issue_number"] = number
                elif isinstance(number, str) and number.isdigit():
                    result["issue_number"] = int(number)

            if not summary_text:
                summary_text = self._fallback_summary(
                    service,
                    effective_namespace,
                    effective_resource_type,
                    log_snippet,
                    resource_description,
                    events_summary,
                )
            result["summary"] = summary_text

            slack_result = llm_collected.get("slack_result")
            if slack_result is None:
                slack_result = await self._post_slack_summary(
                    manager,
                    channel=resolved_channel,
                    service=service,
                    namespace=effective_namespace,
                    resource_type=effective_resource_type,
                    issue_url=result["issue_url"],
                    log_snippet=log_snippet,
                    summary=summary_text,
                )
            result["slack_result"] = slack_result or {}

            if not result["issue_url"]:
                issue_url, issue_number = await self._create_github_issue(
                    manager,
                    service=service,
                    prompt=prompt,
                    namespace=effective_namespace,
                    resource_type=effective_resource_type,
                    log_snippet=log_snippet,
                    resource_description=resource_description,
                    events_summary=events_summary,
                )
                result["issue_url"] = issue_url
                result["issue_number"] = issue_number

            result["log_snippet"] = log_snippet
            result["resource_description"] = resource_description
            result["events_summary"] = events_summary

        return result

    def _build_prompt(
        self,
        service: str,
        namespace: Optional[str],
        slack_channel: Optional[str],
        resource_type: str,
    ) -> str:
        namespace_instruction = (
            f"in the `{namespace}` namespace"
            if namespace
            else "searching across all namespaces"
        )
        slack_instruction = (
            f"and send a summary to Slack channel `{slack_channel}`"
            if slack_channel
            else "and prepare a summary message"
        )
        return (
            f"Investigate the recent status of {resource_type} `{service}` {namespace_instruction}. "
            f"Inspect the most recent {self.log_tail} log lines for anomalies, capture any events, "
            f"and provide a concise summary. Prepare a remediation plan {slack_instruction}."
        )

    def _get_llm_client(self):
        if self.llm_client_getter:
            try:
                return self.llm_client_getter()
            except Exception as exc:  # pragma: no cover - defensive logging
                self.logger.warning("LLM client getter failed: %s", exc)
        try:
            from ..llm.main import STATE  # type: ignore circular import

            return STATE.get("llm_client")
        except Exception:
            return None

    async def _run_llm_session(
        self,
        manager: MCPConnectionManager,
        llm_client,
        service: str,
        namespace: str,
        resource_type: str,
        slack_channel: Optional[str],
    ) -> Dict[str, Any]:
        tools = await self._collect_tool_definitions(manager)
        if not tools:
            raise RuntimeError("No diagnostic tools available for LLM session")

        system_prompt = self._build_system_prompt(namespace, resource_type)
        user_prompt = self._build_prompt(
            service,
            namespace,
            slack_channel,
            resource_type,
        )

        messages: List[Dict[str, Any]] = [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ]

        conversation_trace: List[Dict[str, Any]] = []
        collected: Dict[str, Any] = {}
        summary_text = ""

        for iteration in range(self.max_llm_iterations):
            response = llm_client.chat_with_tools(messages, tools)

            assistant_content_raw = response.get("content", "")
            assistant_text = self._coerce_content_to_text(assistant_content_raw)
            raw_tool_calls = response.get("tool_calls") or []

            normalized_calls = [
                self._normalize_tool_call(call) for call in raw_tool_calls
            ]

            assistant_message: Dict[str, Any] = {"role": "assistant"}
            if isinstance(assistant_content_raw, list):
                assistant_message["content"] = assistant_content_raw
            elif assistant_text:
                assistant_message["content"] = assistant_text

            if normalized_calls:
                assistant_message["tool_calls"] = [
                    call_dict for _, _, _, call_dict in normalized_calls
                ]

            if assistant_message.get("content") or assistant_message.get("tool_calls"):
                messages.append(assistant_message)
                conversation_trace.append(
                    {
                        "type": "assistant",
                        "content": assistant_text,
                        "tool_calls": assistant_message.get("tool_calls", []),
                    }
                )

            if not normalized_calls:
                summary_text = assistant_text
                break

            for tool_name, arguments, call_id, _ in normalized_calls:
                if not tool_name:
                    continue

                self._inject_default_arguments(arguments, namespace, resource_type)

                try:
                    tool_result = await manager.call_tool(tool_name, arguments)
                except ValueError as exc:
                    fallback_name = tool_name.split(".")[-1]
                    if fallback_name != tool_name and manager.has_tool(fallback_name):
                        try:
                            tool_result = await manager.call_tool(fallback_name, arguments)
                            tool_name = fallback_name
                        except Exception as inner_exc:  # pragma: no cover
                            self.logger.exception(
                                "Tool %s execution failed after fallback", fallback_name, exc_info=inner_exc
                            )
                            tool_result = {
                                "content": [
                                    {
                                        "type": "text",
                                        "text": f"Tool {fallback_name} execution failed: {inner_exc}",
                                    }
                                ],
                                "is_error": True,
                            }
                    else:
                        self.logger.exception("Tool %s execution failed", tool_name, exc_info=exc)
                        tool_result = {
                            "content": [
                                {
                                    "type": "text",
                                    "text": f"Tool {tool_name} execution failed: {exc}",
                                }
                            ],
                            "is_error": True,
                        }
                except Exception as exc:  # pragma: no cover - defensive logging
                    self.logger.exception("Tool %s execution failed", tool_name, exc_info=exc)
                    tool_result = {
                        "content": [
                            {
                                "type": "text",
                                "text": f"Tool {tool_name} execution failed: {exc}",
                            }
                        ],
                        "is_error": True,
                    }

                limited_result = self._limit_tool_payload(tool_name, tool_result)

                conversation_trace.append(
                    {
                        "type": "tool_result",
                        "tool": tool_name,
                        "arguments": arguments,
                        "result": limited_result,
                    }
                )
                self._update_collected_from_tool(tool_name, limited_result, collected)

                messages.append(
                    {
                        "role": "tool",
                        "tool_call_id": call_id,
                        "name": tool_name,
                        "content": self._serialize_tool_result(limited_result),
                    }
                )

        return {
            "summary": summary_text,
            "collected": collected,
            "conversation": conversation_trace,
        }

    async def _collect_tool_definitions(
        self, manager: MCPConnectionManager
    ) -> List[Dict[str, Any]]:
        definitions: List[Dict[str, Any]] = []
        for session in manager.sessions.values():
            server_name = None
            try:
                server_name = session.server_info.get("name")
            except Exception:
                pass
            try:
                response = await session.list_tools()
            except Exception as exc:  # pragma: no cover - defensive logging
                self.logger.exception("Failed to list tools for diagnostics", exc_info=exc)
                continue

            for tool in response.tools:
                tool_name = getattr(tool, "name", None)
                if not tool_name:
                    continue
                if self.allowed_tools:
                    qualified_name = tool_name
                    if server_name and not tool_name.startswith(f"{server_name}."):
                        qualified_name = f"{server_name}.{tool_name}"

                    if (
                        tool_name not in self.allowed_tools
                        and qualified_name not in self.allowed_tools
                    ):
                        continue

                parameters: Dict[str, Any] = {}
                schema = getattr(tool, "input_schema", None)
                if schema is None:
                    schema = getattr(tool, "inputSchema", None)

                if schema is not None:
                    try:
                        if hasattr(schema, "model_dump"):
                            parameters = schema.model_dump()
                        else:
                            parameters = dict(schema)  # type: ignore[arg-type]
                    except Exception:
                        parameters = {}

                description = getattr(tool, "description", "") or ""

                definition = {
                    "type": "function",
                    "function": {
                        "name": tool_name,
                        "description": description,
                        "parameters": parameters or {"type": "object", "properties": {}},
                    },
                }
                definitions.append(definition)

                if server_name and not tool_name.startswith(f"{server_name}."):
                    qualified = dict(definition)
                    qualified_function = dict(definition["function"])
                    qualified_function["name"] = f"{server_name}.{tool_name}"
                    qualified["function"] = qualified_function
                    definitions.append(qualified)
        return definitions

    def _coerce_content_to_text(self, content: Any) -> str:
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            fragments = []
            for item in content:
                if isinstance(item, dict):
                    text = item.get("text")
                    if text:
                        fragments.append(text)
            return "\n".join(fragments)
        return ""

    def _inject_default_arguments(
        self,
        arguments: Dict[str, Any],
        namespace: str,
        resource_type: str,
    ) -> None:
        if namespace and "namespace" not in arguments:
            arguments["namespace"] = namespace
        if resource_type and "resourceType" not in arguments:
            arguments["resourceType"] = resource_type
        if "tail" not in arguments and self.log_tail:
            arguments["tail"] = self.log_tail

    def _serialize_tool_result(self, result: Dict[str, Any]) -> str:
        try:
            return json.dumps(result, default=str)
        except TypeError:
            return str(result)

    def _truncate_text(self, text: str, limit: int = 5000) -> str:
        if len(text) <= limit:
            return text
        return text[:limit] + "\n… (truncated)"

    def _shrink_logs_json(
        self,
        text: str,
        per_container_limit: int = 2000,
        max_pods: int = 1,
        max_containers: int = 1,
    ) -> str:
        try:
            data = json.loads(text)
        except json.JSONDecodeError:
            return self._truncate_text(text)

        logs = data.get("logs")
        if not isinstance(logs, dict):
            return self._truncate_text(text)

        trimmed_logs = {}
        for pod_index, (pod_name, pod_value) in enumerate(logs.items()):
            if pod_index >= max_pods:
                break
            if isinstance(pod_value, dict):
                trimmed_pod = {}
                for container_index, (container_name, container_text) in enumerate(pod_value.items()):
                    if container_index >= max_containers:
                        break
                    if isinstance(container_text, str):
                        trimmed_pod[container_name] = self._truncate_text(
                            container_text, per_container_limit
                        )
                    else:
                        trimmed_pod[container_name] = container_text
                trimmed_logs[pod_name] = trimmed_pod
            else:
                trimmed_logs[pod_name] = pod_value

        data["logs"] = trimmed_logs
        try:
            return json.dumps(data)
        except TypeError:
            return self._truncate_text(text)

    def _limit_tool_payload(self, tool_name: str, result: Dict[str, Any]) -> Dict[str, Any]:
        if not result:
            return result

        limited = dict(result)
        content = limited.get("content")
        simple_name = tool_name.split(".")[-1] if tool_name else tool_name

        if isinstance(content, list):
            new_content = []
            for block in content:
                if not isinstance(block, dict):
                    new_content.append(block)
                    continue
                block = dict(block)
                text = block.get("text")
                if isinstance(text, str):
                    if simple_name == "get_logs":
                        block["text"] = self._shrink_logs_json(text)
                    else:
                        block["text"] = self._truncate_text(text)

                resource = block.get("resource")
                if isinstance(resource, dict):
                    resource = dict(resource)
                    resource_text = resource.get("text")
                    if isinstance(resource_text, str):
                        if simple_name == "get_logs":
                            resource["text"] = self._shrink_logs_json(resource_text)
                        else:
                            resource["text"] = self._truncate_text(resource_text)
                    block["resource"] = resource

                new_content.append(block)
            limited["content"] = new_content
        return limited

    def _normalize_tool_call(
        self, call: Any
    ) -> Tuple[Optional[str], Dict[str, Any], Optional[str], Dict[str, Any]]:
        """Return (tool_name, arguments_dict, call_id, call_dict) from various call formats."""

        call_dict: Dict[str, Any]
        if isinstance(call, dict):
            call_dict = dict(call)
        elif hasattr(call, "model_dump"):
            call_dict = call.model_dump()
        else:
            call_dict = {
                "id": getattr(call, "id", None),
                "type": getattr(call, "type", "function"),
                "function": {
                    "name": getattr(getattr(call, "function", None), "name", None),
                    "arguments": getattr(getattr(call, "function", None), "arguments", None),
                },
            }

        function_call = call_dict.get("function") or {}
        if not isinstance(function_call, dict) and hasattr(function_call, "model_dump"):
            function_call = function_call.model_dump()
            call_dict["function"] = function_call

        tool_name = function_call.get("name")
        raw_arguments = function_call.get("arguments")

        arguments: Dict[str, Any] = {}
        if isinstance(raw_arguments, dict):
            arguments = raw_arguments
        elif isinstance(raw_arguments, str):
            try:
                arguments = json.loads(raw_arguments or "{}")
            except json.JSONDecodeError:
                arguments = {}

        return tool_name, arguments, call_dict.get("id"), call_dict

    def _update_collected_from_tool(
        self, tool_name: str, result: Dict[str, Any], collected: Dict[str, Any]
    ) -> None:
        if not result:
            return

        simple_name = tool_name.split(".")[-1] if tool_name else tool_name

        content = result.get("content") or []
        text_value = self._extract_text_from_content(content)
        json_value = self._extract_json_from_content(content)

        if simple_name == "get_logs":
            payload = json_value or self._try_parse_json(text_value) or text_value
            existing = collected.get("logs")
            if isinstance(existing, dict) and isinstance(payload, dict):
                existing.update(payload)  # merge pods
            else:
                collected["logs"] = payload
            return

        if simple_name in {"describe_deployment", "describe_pod"}:
            collected["resource"] = text_value or json_value or ""
            return

        if simple_name in {"get_events", "list_events"}:
            collected["events"] = text_value or json.dumps(json_value, default=str)
            return

        if simple_name == "create_issue":
            data = json_value or self._try_parse_json(text_value) or {}
            collected["issue"] = data
            return

        if simple_name == "slack_post_message":
            data = json_value or self._try_parse_json(text_value) or {}
            collected["slack_result"] = data
            return

    def _render_log_snippet(self, logs_payload: Any) -> str:
        if not logs_payload:
            return ""

        if isinstance(logs_payload, str):
            return logs_payload

        snippet_parts: List[str] = []

        if isinstance(logs_payload, dict):
            for pod, pod_value in logs_payload.items():
                if isinstance(pod_value, dict):
                    for container, text in pod_value.items():
                        text_str = text if isinstance(text, str) else str(text)
                        snippet_parts.append(
                            f"--- {pod} :: {container} ---\n"
                            f"{text_str[:2000]}"
                        )
                else:
                    text_str = pod_value if isinstance(pod_value, str) else str(pod_value)
                    snippet_parts.append(f"--- {pod} ---\n{text_str[:2000]}")

        if not snippet_parts:
            return str(logs_payload)

        combined = "\n\n".join(snippet_parts)
        if len(combined) > 6000:
            combined = combined[:6000] + "\n…"
        return combined

    def _fallback_summary(
        self,
        service: str,
        namespace: str,
        resource_type: str,
        logs: str,
        resource: str,
        events: str,
    ) -> str:
        namespace_text = f" in namespace `{namespace}`" if namespace else ""
        sections = [f"Diagnostics summary for {resource_type} `{service}`{namespace_text}."]
        if logs:
            sections.append("Log excerpt collected.")
        if resource:
            sections.append("Resource description retrieved.")
        if events:
            sections.append("Recent Kubernetes events recorded.")
        sections.append("Review the attached details and consider next remediation steps.")
        return " ".join(sections)

    def _build_system_prompt(self, namespace: str, resource_type: str) -> str:
        details = [
            "You are an SRE diagnostics assistant with access to MCP tools for Kubernetes, Slack, and GitHub.",
            "Use the available tools to inspect the service, gather evidence, and summarize findings.",
            "Always include container names when requesting logs if a pod has multiple containers.",
            "Prefer the provided namespace for Kubernetes requests unless the user overrides it explicitly.",
            "After gathering information, provide a clear summary with potential next steps.",
        ]
        if namespace:
            details.append(f"The default Kubernetes namespace for diagnostics is `{namespace}`.")
        if resource_type:
            details.append(f"The default resource type to inspect is `{resource_type}`.")
        details.append("If a GitHub issue already exists, avoid creating duplicates unless necessary.")
        return "\n".join(details)

    async def _fetch_log_snippet(
        self,
        manager: MCPConnectionManager,
        service: str,
        namespace: str,
        resource_type: str,
    ) -> str:
        if not manager.has_tool("get_logs"):
            self.logger.info(
                "Kubernetes MCP server not available; skipping log retrieval"
            )
            return "Kubernetes MCP server was unavailable; no logs were captured."

        args = {
            "resourceType": resource_type,
            "name": service,
            "namespace": namespace,
            "tail": self.log_tail,
        }

        response = await self._call_tool_safe(manager, "get_logs", args)
        if not response:
            return "Failed to retrieve logs via Kubernetes MCP."

        limited = self._limit_tool_payload("get_logs", response)
        text = self._extract_text_from_content(limited.get("content", []))
        return text or "No logs were returned by the Kubernetes MCP server."

    async def _describe_resource(
        self,
        manager: MCPConnectionManager,
        service: str,
        namespace: str,
        resource_type: str,
    ) -> str:
        tool_map = {
            "deployment": "describe_deployment",
            "pod": "describe_pod",
            "job": "describe_job",
        }
        tool_name = tool_map.get(resource_type.lower())
        if not tool_name or not manager.has_tool(tool_name):
            return ""

        response = await self._call_tool_safe(
            manager,
            tool_name,
            {
                "name": service,
                "namespace": namespace,
            },
        )
        if not response:
            return ""

        return self._extract_text_from_content(response.get("content", []))

    async def _fetch_events(
        self,
        manager: MCPConnectionManager,
        service: str,
        namespace: str,
        resource_type: str,
    ) -> str:
        if not manager.has_tool("get_events"):
            return ""

        response = await self._call_tool_safe(
            manager,
            "get_events",
            {
                "namespace": namespace,
                "resourceType": resource_type,
                "name": service,
            },
        )
        if not response:
            return ""

        return self._extract_text_from_content(response.get("content", []))

    async def _create_github_issue(
        self,
        manager: MCPConnectionManager,
        service: str,
        prompt: str,
        namespace: str,
        resource_type: str,
        log_snippet: str,
        resource_description: str,
        events_summary: str,
    ) -> Tuple[str, int]:
        if not (
            self.github_owner
            and self.github_repo
            and manager.has_tool("create_issue")
        ):
            self.logger.info(
                "GitHub issue creation skipped (owner/repo missing or tool unavailable)"
            )
            return "", 0

        title = f"Diagnostics report for {service}"
        body_sections = [
            f"## Prompt\n{prompt}",
            f"## Timestamp\n{datetime.utcnow().isoformat()}",
        ]
        if namespace:
            body_sections.append(f"## Namespace\n`{namespace}`")
        if resource_type:
            body_sections.append(f"## Resource Type\n`{resource_type}`")
        if resource_description:
            body_sections.append(
                f"## Resource Description\n```\n{resource_description}\n```"
            )
        if events_summary:
            body_sections.append(
                f"## Recent Events\n```\n{events_summary}\n```"
            )
        if log_snippet:
            body_sections.append(
                f"## Log Snippet (last {self.log_tail} lines)\n```\n{log_snippet}\n```"
            )

        body = "\n\n".join(body_sections)

        response = await self._call_tool_safe(
            manager,
            "create_issue",
            {
                "owner": self.github_owner,
                "repo": self.github_repo,
                "title": title,
                "body": body,
                "labels": ["diagnostics", service],
            },
        )
        if not response:
            return "", 0

        data = self._extract_json_from_content(response.get("content", []))
        issue_url = data.get("html_url") if isinstance(data, dict) else ""
        issue_number = data.get("number") if isinstance(data, dict) else 0

        return issue_url or "", issue_number or 0

    async def _post_slack_summary(
        self,
        manager: MCPConnectionManager,
        channel: Optional[str],
        service: str,
        namespace: str,
        resource_type: str,
        issue_url: str,
        log_snippet: str,
        summary: str = "",
    ) -> Optional[Dict[str, Any]]:
        if not channel:
            return None

        if not manager.has_tool("slack_post_message"):
            self.logger.info(
                "Slack MCP server not available; skipping Slack notification"
            )
            return None

        summary_lines = [
            f":mag: Diagnostics run for *{service}* completed at {datetime.utcnow().isoformat()}",
        ]
        if namespace:
            summary_lines.append(f":round_pushpin: Namespace: `{namespace}`")
        if resource_type:
            summary_lines.append(f":clipboard: Resource type: `{resource_type}`")
        if issue_url:
            summary_lines.append(f":github: Issue created → {issue_url}")
        if summary:
            summary_lines.append(f":memo: Summary:\n{summary}")
        if log_snippet:
            summary_lines.append(
                f":scroll: Log excerpt:\n```{log_snippet[:700] + ('…' if len(log_snippet) > 700 else '')}```"
            )

        response = await self._call_tool_safe(
            manager,
            "slack_post_message",
            {"slack_channel_id": channel, "text": "\n".join(summary_lines)},
        )
        if not response:
            return None

        data = self._extract_json_from_content(response.get("content", []))
        return data if isinstance(data, dict) else {"raw_response": response}

    async def _call_tool_safe(
        self,
        manager: MCPConnectionManager,
        tool_name: str,
        arguments: Dict[str, Any],
    ) -> Optional[Dict[str, Any]]:
        try:
            return await manager.call_tool(tool_name, arguments)
        except Exception as exc:
            self.logger.exception("Tool %s failed: %s", tool_name, exc)
            return None

    def _extract_text_from_content(self, content: List[Dict[str, Any]]) -> str:
        texts: List[str] = []
        for block in content:
            if not isinstance(block, dict):
                continue
            if block.get("type") == "text" and "text" in block:
                texts.append(str(block["text"]))
            elif "text" in block:
                texts.append(str(block["text"]))
            elif "resource" in block:
                resource = block["resource"]
                if isinstance(resource, dict):
                    text_value = resource.get("text")
                    if text_value:
                        texts.append(str(text_value))
            elif "data" in block:
                texts.append(json.dumps(block["data"], indent=2))
        return "\n".join(texts).strip()

    def _extract_json_from_content(self, content: List[Dict[str, Any]]) -> Any:
        for block in content:
            if not isinstance(block, dict):
                continue
            if "data" in block and isinstance(block["data"], dict):
                return block["data"]
            if "resource" in block:
                resource = block["resource"]
                if isinstance(resource, dict) and isinstance(resource.get("data"), dict):
                    return resource["data"]
                if isinstance(resource, dict) and isinstance(resource.get("text"), str):
                    parsed = self._try_parse_json(resource["text"])
                    if parsed is not None:
                        return parsed
            if "text" in block and isinstance(block["text"], str):
                parsed = self._try_parse_json(block["text"])
                if parsed is not None:
                    return parsed
        return {}

    def _try_parse_json(self, candidate: str) -> Optional[Any]:
        try:
            return json.loads(candidate)
        except (json.JSONDecodeError, TypeError):
            return None
