# Contributing to Ponos

Ponos is the terminal interface for nodeoperator.ai, an agentic platform for blockchain infrastructure management. This document explains how to contribute effectively to a system where correctness, security, and architectural clarity matter deeply.

## What Ponos Is and Isn't

Ponos is a **client**. It handles user interaction, configuration management, and request routing. It is not an orchestration engine, tool library, or integration platform.

**Ponos owns:**
- Terminal user interface and interaction flows
- Local configuration loading and validation
- Credential management and workspace isolation
- HTTP communication with agent-core
- Command parsing and user feedback

**Ponos does not own:**
- LLM orchestration or tool execution
- External service integrations (GitHub, Slack, Kubernetes)
- Business logic for blockchain operations
- Multi-step workflow management

These responsibilities belong to **agent-core**, the backend service that Ponos communicates with. Understanding this boundary is essential for effective contribution.

## Architecture Context

Ponos operates in two modes:
1. **Development mode**: Connects to local agent-core instance
2. **Production mode**: Connects to hosted agent-core at api.nodeoperator.ai

The production model requires multi-tenant isolation. Each user's workspace configuration (GitHub tokens, Slack credentials, Kubernetes access) must be completely isolated from other users. This constraint affects every architectural decision.

## Contribution Boundaries

### Changes that belong in Ponos:
- TUI improvements and new interaction patterns
- Configuration loading enhancements
- HTTP client optimizations
- Local validation and error handling
- Command routing and response formatting

### Changes that require agent-core coordination:
- New tool integrations
- Workflow modifications
- External service protocols
- LLM prompt engineering
- Multi-step operation logic

### Changes that belong entirely in agent-core:
- Tool implementations
- External API clients
- Orchestration workflows
- State management between operations

If you're unsure where a change belongs, ask before implementing.

## How to Contribute

### Before You Start

Open an issue for any substantial change. This includes:
- New commands or interaction patterns
- Configuration model changes
- Security or credential handling modifications
- Breaking changes to existing workflows

Small fixes (typos, obvious bugs, test improvements) don't require upfront discussion.

### Quality Standards

This is infrastructure software. Contributors should approach it with the same rigor as systems that manage production traffic or financial transactions.

**Code must be:**
- Correct by inspection
- Defensively written
- Properly error handled
- Thoroughly tested

**Changes must:**
- Preserve backward compatibility unless explicitly discussed
- Include appropriate validation
- Handle credential security properly
- Maintain workspace isolation guarantees

### Local Development

You need:
- Go 1.21+ for Ponos development
- Access to agent-core (local or hosted)
- Test workspace configurations with isolated credentials

Set up your development workspace:
```bash
git clone https://github.com/blockopsnetwork/ponos
cd ponos
cp config.yaml.example ~/.ponos/config.yaml
# Edit ~/.ponos/config.yaml with your isolated test credentials
make build
./ponos --help
```

Test against both local and hosted agent-core to verify your changes work in both deployment models.

### Configuration and Security

Credential handling is not negotiable. Every change that touches user configuration or external service access must:

1. Maintain workspace isolation between users
2. Validate credentials against explicit allow-lists
3. Never log or expose sensitive data
4. Follow least-privilege patterns

The configuration model serves as a security boundary. Changes to config structures affect multi-tenancy and must be carefully reviewed.

### Submitting Changes

**Branch naming:** `feature/description`, `fix/description`, or `security/description`

**Commits:** Should be atomic and well-described. If you can't explain the change in one sentence, split it.

**Pull requests must include:**
- Clear description of what changed and why
- Test coverage for new functionality
- Validation that existing workflows still work
- Documentation updates if user-facing behavior changed

**Tests:** We expect comprehensive test coverage. If your change touches credential handling, multi-tenancy, or external communication, include integration tests.

### What We Review For

**Correctness:** Does the code do what it claims to do? Are edge cases handled?

**Security:** Are credentials handled safely? Is workspace isolation maintained? Could this be abused?

**Architecture:** Does this belong in Ponos? Does it respect the client/server boundary?

**Maintainability:** Will future contributors understand this? Is it consistent with existing patterns?

**Performance:** TUI interactions should feel instant. HTTP requests should have reasonable timeouts and error handling.

### Review Process

Maintainers will:
- Test changes against both local and hosted agent-core
- Verify workspace isolation works correctly
- Check for credential leakage or security issues
- Validate architectural boundaries

We may request changes or suggest alternative approaches. This is normal for infrastructure software where correctness matters more than shipping quickly.

### Documentation

Code should be self-explanatory. Comments should explain **why**, not **what**. User-facing documentation should be updated when behavior changes.

Configuration examples and security guidance are particularly important. Users need to understand how to set up workspace isolation correctly.

## Project Values

**Correctness over speed:** We would rather ship working software slowly than break existing workflows.

**Security by design:** Multi-tenant isolation and credential safety are not afterthoughts.

**Clear boundaries:** Each component should have a well-defined purpose. Scope creep leads to unmaintainable systems.

**User empathy:** People use Ponos to manage critical infrastructure. Confusing errors or unexpected behavior can cause real damage.

**Composability:** Ponos should work well with other tools in a practitioner's workflow.

## Getting Help

For technical questions, open an issue. For architectural discussions, use GitHub discussions. For security concerns, email the maintainers directly.

We expect contributors to read existing code, understand the patterns, and ask specific questions rather than broad "how do I" requests.

## What Not to Contribute

- Generic features that could apply to any CLI tool
- Tool integrations that should live in agent-core
- Breaking changes without substantial justification
- Security modifications without threat model discussion
- Performance optimizations without benchmarks

Focus on making Ponos better at being a client for the nodeoperator.ai platform, not turning it into something else.