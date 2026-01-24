---
description: About Nodeoperator AI
---

# Introduction

Nodeoperator AI is an autonomous node operator agent that deploys, manages, and remediates issues with blockchain infrastructure using GitOps as a human-in-the-loop control model.

## Why an AI Node Operator?

Running blockchain infrastructure today is manual, fragile, and error-prone. Nodeoperators and Solo Stakers:

* Manually Track upstream client releases
* Perform risky upgrades
* Debug failing nodes under time pressure
* Maintain complex Kubernetes environments

Nodeoperator AI is designed to reduce this operational burden while keeping humans in control.

## Is AI agent safe for critical infrastructure?

We recognize the concerns about placing critical infrastructure under the control of an AI agent:

> Is AI ready? Can it be trusted with critical infra? What about hallucinations and unpredictable execution?. Is this just jumping on another shining new AI tool?

These questions are valid and we addressed them head on

Nodeoperator AI is built on a constraint-driven model, not open-ended automation:

* Actions follow deterministic workflows
* Operational boundaries are explicitly defined
* Changes are delivered via GitOps (not direct mutations)
* The agent uses domain-specific infrastructure knowledge
* Human approval remains part of the control loop

When sandboxed, scoped, and supervised, AI agents can reduce human error and execute repetitive operational tasks with higher consistency than manual workflows.

## System Architecture

Nodeoperator AI is built as modular services:

### Interfaces

Where operators interact with the system. Designed to fit existing workflows rather than forcing a chat-only model.

#### Ponos

**Ponos** is the command interface for Nodeoperator AI.

> Ponos (Greek: Πόνος) means _toil_, _labor_, or _sustained effort_. Ponos takes on that toil for node operators.

**Available today:**

| Interface     | Use Case                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------ |
| **TUI**       | Interactive terminal UI with workflow progress cards, real-time logs, and natural language input |
| **Slack Bot** | Team workflows via slash commands (`/upgrade`, `/diagnose`) and threaded conversations           |

**TUI Features:**

* Workflow cards showing real-time progress percentage
* Status badges (pending → in progress → awaiting approval → complete)
* Natural language command input
* Session history and resume capability

**Slack Bot Features:**

* Slash commands for common operations
* Thread-based conversations for follow-ups
* Alert response integration
* Team visibility into operations

**Planned interfaces:**

* **GitHub Actions** — Trigger workflows from CI/CD pipelines
* **GitHub Comments** — Operate via PR/issue comments
* **Discord** — Community and team workflows

### Agent Core (Backend)

This is the agent's decision engine, where context logic, safety guardrails, and operational intelligence are enforced.

* **Workflow orchestration** — Manages multi-step operations with checkpoints and rollback capability
* **Session management** — Maintains conversation context and execution state across interactions
* **LLM integration** — Supports Claude, GPT-4, Gemini, and other models with streaming responses
* **Safety guardrails** — Validates actions against operational rules before execution
* **Rulebook engine** — Applies team-defined playbooks and constraints to agent decisions
* **Memory system** — Stores and retrieves operational knowledge for context-aware responses

### MCP Servers

MCP (Model Context Protocol) servers are modular connectors to external systems. They are separated to allow teams to run their own servers, control credentials, and minimize trust assumptions.

| Server             | Purpose                                                          |
| ------------------ | ---------------------------------------------------------------- |
| **GitHub MCP**     | Create PRs, manage issues, fetch releases, repository operations |
| **Kubernetes MCP** | Query pods, fetch logs, read deployments, cluster operations     |
| **Slack MCP**      | Read/send messages, manage threads, bot interactions             |
| **Telescope MCP**  | Privacy-preserving observability for blockchain infrastructure   |
| **Blockchain MCP** | Protocol-specific tooling for chain interactions                 |

**Key design principles:**

* **Self-hostable** — Run MCP servers in your own environment
* **Credential isolation** — Each server manages its own secrets
* **Minimal trust** — The agent only has access to what you explicitly connect
* **Auditable** — All MCP calls are logged

All MCP servers are open source: https://github.com/blockopsnetwork/mcp-servers

## Core Workflows & Capabilities

Ponos supports three core workflows:

### 1. Upgrade Workflow

Upgrade blockchain clients and infrastructure components with automated changelog analysis.

* **Supported clients**: Ethereum execution/consensus clients (Geth, Prysm, Lighthouse, Teku, Nimbus), EVM chains, Polkadot, Cosmos, and Solana (experimental)
* **What it does**:
  * Fetches latest releases from upstream repositories
  * Analyzes changelogs and identifies breaking changes
  * Compares current vs target versions
  * Generates upgrade PR with AI-summarized release notes
  * Runs pre-flight compatibility checks

**Example prompts:**

* "Upgrade mainnet Geth to the latest version"
* "Show me available Lighthouse versions for testnet"
* "Upgrade all Ethereum clients on holesky to latest stable"

### 2. Diagnose Workflow

Investigate node failures using logs, metrics, and cluster state to determine root causes.

* **What it does**:
  * Collects pod logs and Kubernetes events
  * Queries Prometheus/Grafana metrics
  * Performs root cause analysis (RCA)
  * Creates GitHub issues with findings
  * Generates fix PRs for common issues (e.g., memory limits, config errors)

**Example prompts:**

* "Diagnose mainnet Ethereum validators"
* "Check why Geth pods are failing on testnet"
* "Investigate high attestation miss rate on validator-01"

### 3. Deploy Workflow

Deploy new nodes or update configurations via GitOps.

* **What it does**:
  * Validates Kubernetes manifests
  * Creates deployment PRs with change diffs
  * Monitors rollout progress
  * Runs post-deployment health checks

**Example prompts:**

* "Deploy a new Lighthouse beacon node on mainnet"
* "Update validator count to 10 on holesky cluster"

## Features

### GitOps-First Operations

**Upgrade Nodes Through Pull Requests** Client upgrades are proposed via GitOps with version and release awareness. Every upgrade includes AI-generated changelog summaries, breaking change detection, and rollback instructions.

**Operate Through Git, Not Direct Access** Infrastructure is never mutated directly — all changes go through reviewable PRs. This provides a complete audit trail, enables team review, and allows easy rollbacks.

### Intelligent Diagnostics

**Root Cause Analysis (RCA)** When nodes fail, the agent correlates logs, metrics, and Kubernetes state to identify the root cause. Findings are documented in GitHub issues with actionable recommendations.

**Automated Fix Generation** For common issues (OOM kills, resource limits, configuration errors), the agent generates fix PRs automatically. Human approval is still required before changes are applied.

### AI Capabilities

**Natural Language Interface** Describe what you want in plain English. The agent interprets your intent and executes the appropriate workflow.

**Context-Aware Sessions** The agent remembers conversation context. Follow-up questions like "now do the same for testnet" work without repeating the full context.

**Multi-Model Support** Works with Claude, GPT-4, and other LLMs. Choose the model that fits your needs and budget.

### Operational Safety

**Keep Secrets Out of Outputs** Sensitive values (API keys, passwords, private keys) are automatically redacted and never exposed in logs, PRs, or agent responses.

**Enforce Operational Guardrails** Actions are validated against safety rules before execution. The agent cannot perform destructive operations without explicit approval.

**Rulebooks** Define operational playbooks that the agent must follow. Rulebooks encode your team's best practices and constraints.

### Observability & Tracking

**Real-Time Progress** Workflows display live progress in the TUI. See exactly what the agent is doing at each step.

**Execution History** All sessions are logged with checkpoints. Resume failed workflows or replay past operations.

**Session Continuity** If a workflow fails, you can resume from the last checkpoint instead of starting over.

### Integration & Extensibility

**Work From Your Existing Tools** Run operations from the Ponos terminal interface, Slack bot, or automation workflows (GitHub Actions coming soon).

**Integrate With Your Stack** Connects to GitHub, Kubernetes, Prometheus, Grafana, and blockchain networks via MCP servers.

**Run It in Your Own Environment** MCP servers are open source and self-hostable. You control credentials, network access, and trust boundaries.

### Multi-Chain Support

**Ethereum Ecosystem** Full support for execution clients (Geth, Nethermind, Besu, Erigon) and consensus clients (Prysm, Lighthouse, Teku, Nimbus, Lodestar).

**Other Networks** Polkadot, Cosmos, and Solana support (experimental). The architecture is designed to be chain-agnostic.
