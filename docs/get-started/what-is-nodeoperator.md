---
description: About nodeoperator.ai
---

# What is nodeoperator.ai?

Nodeoperator.ai is an autonomous node operator agent that deploys, manages, and remediates issues with blockchain infrastructure using GitOps as a human-in-the-loop control model. 

## Why an AI Node Operator?
Running blockchain infrastructure today is manual, fragile, and error-prone. Nodeoperators and Solo Stakers:
- Manually Track upstream client releases
- Perform risky upgrades
- Debug failing nodes under time pressure
- Maintain complex Kubernetes environments

Nodeoperator AI is designed to reduce this operational burden while keeping humans in control.

## Is AI safe for critical infrastructure?
We recognize the concerns about placing critical infrastructure under the control of an AI agent: 

"Is AI ready? Can it be trusted with critical infra? What about hallucinations and unpredictable execution?. Is this just jumping on another shining new AI tool?" 

These questions are valid and they must be addressed head-on.

NodeOperator.ai is built on a constraint-driven model, not open-ended automation:

- Actions follow deterministic workflows
- Operational boundaries are explicitly defined
- Changes are delivered via GitOps (not direct mutations)
- The agent uses domain-specific infrastructure knowledge
- Human approval remains part of the control loop

When sandboxed, scoped, and supervised, AI agents can reduce human error and execute repetitive operational tasks with higher consistency than manual workflows


# System Architecture
Nodeoperator AI is built as modular services:

### Interfaces
Where operators interact with the system. Designed to fit existing workflows rather than forcing a chat-only model.

#### Ponos
**Ponos** is the terminal  interface for nodeoperator ai

> Ponos (Greek: Ponos) means *toil*, *labor*, or *sustained effort*.  
> Ponos takes on that toil for node operators.

Ponos ships today as:
- **TUI** (terminal UI) for interactive operations
- **Slack** bot for team workflows

Planned interfaces:
- **GitHub Actions**
- **Discord**
- **GitHub Comments**


### Agent Core (Backend)
This is the agent’s decision engine, where context logic, safety guardrails, and operational intelligence are enforced.
- Workflow orchestration
- Context management
- Safety guardrails
- Operational reasoning

### MCP Servers
These are modular connectors to external systems. They are separated to allow teams to run their own servers, control credentials, and minimize trust assumptions.
- GitHub MCP Server
- Kubernetes MCP Server
- Slack MCP Server
- Telescope MCP Server — privacy-preserving observability for blockchain infrastructure
- Blockchain MCP Server — protocol-specific tooling for chain interactions

All MCP servers are open source and available for self-hosting: https://github.com/blockopsnetwork/mcp-servers

# Core Workflows & Capabilities
Ponos focuses on two core workflows:

1) **Upgrade blockchain clients and components**
   - Examples: Ethereum execution/consensus clients, EVM chains, Polkadot, Cosmos,
     and Solana (experimental).
   - Typical prompts:  
     - "Upgrade mainnet Geth to the latest version"  
     - "Show me available Lighthouse versions for testnet"

2) **Diagnose blockchain nodes on Kubernetes**
   - Typical prompts:  
     - "Diagnose mainnet Ethereum nodes"  
     - "Check why Geth pods are failing on testnet"

## Features

** Upgrade Nodes Through Pull Requests **
Client upgrades are proposed via GitOps with version and release awareness.

** Diagnose Node Failures **
Investigate issues using logs, metrics, and Kubernetes state to determine root causes.

** Operate Through Git, Not Direct Access **
Infrastructure is never mutated directly — changes go through reviewable PRs.

** Work From Your Existing Tools **
Run operations from the Ponos terminal interface, Slack, or automation workflows.

** Keep Secrets Out of Outputs **
Sensitive values are redacted and never exposed in logs or agent responses.

** Enforce Operational Guardrails **
Actions are validated against safety rules before execution.

** Track What the Agent Does **
Workflows include logs, progress visibility, and execution history

** Integrate With Your Stack **
Connects to GitHub, Kubernetes, monitoring systems, and blockchain networks

** Run It in Your Own Environment **
MCP servers are open source and self-hostable, so you control credentials and access.

** Support Multi-Chain Node Operations ** 
Works across Ethereum, Polkadot, Cosmos, and other networks.
