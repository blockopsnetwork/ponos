# Ponos - Slack Automation Bot Documentation

## Table of Contents
- [Overview](#overview)
- [Architecture](#architecture)
- [Technology Stack](#technology-stack)
- [Core Features](#core-features)
- [Code Structure](#code-structure)
- [Configuration](#configuration)
- [Deployment](#deployment)
- [API Endpoints](#api-endpoints)
- [Security](#security)
- [Development](#development)
  - [Taskfile (go-task)](#taskfile-go-task)
- [Troubleshooting](#troubleshooting)

## Overview

**Ponos** is a sophisticated Slack automation bot built for Blockops, a blockchain infrastructure company. Named after the Greek god of hard work and toil, Ponos automates critical operational tasks including deployments, monitoring, and AI-powered decision making for blockchain infrastructure management.

### Key Capabilities
- 🚀 **Automated Deployments** - Deploy services via Slack slash commands
- 📊 **Automated Reporting** - Monitor user signups and support messages
- 🤖 **AI-Powered Analysis** - Intelligent blockchain release evaluation
- 🔄 **Scheduled Tasks** - Periodic maintenance and monitoring
- 🔐 **Secure Operations** - GitHub App integration with proper authentication

## Architecture

### High-Level Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Slack API     │◄──►│     Ponos       │◄──►│   GitHub API    │
│                 │    │   (Go Server)   │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌─────────────────┐
                       │    MySQL DB     │
                       │                 │
                       └─────────────────┘
                              ▲
                              │
                       ┌─────────────────┐
                       │   OpenAI API    │
                       │   (AI Agent)    │
                       └─────────────────┘
```

### Component Overview

1. **HTTP Server** - Handles Slack events, slash commands, and webhooks
2. **GitHub Handler** - Manages repository operations and deployments
3. **AI Agent** - Analyzes releases and makes intelligent decisions
4. **Scheduler** - Executes periodic tasks and reports
5. **Database Layer** - Stores and retrieves operational data

## Technology Stack

### Core Technologies

| Technology | Version | Purpose |
|------------|---------|---------|
| **Go** | 1.24.1 | Primary language |
| **MySQL** | Latest | Data persistence |
| **Docker** | Latest | Containerization |
| **Kubernetes** | Latest | Orchestration |

### Key Dependencies

```go
// Core Slack Integration
github.com/slack-go/slack v0.17.0

// GitHub API Client
github.com/google/go-github/v72 v72.0.0

// Database ORM
github.com/uptrace/bun v1.2.12
github.com/go-sql-driver/mysql v1.9.2

// AI Integration
github.com/tmc/langchaingo v0.1.13

// Authentication & Security
github.com/golang-jwt/jwt/v5 v5.2.2
golang.org/x/oauth2 v0.30.0

// Task Scheduling
github.com/go-co-op/gocron/v2 v2.16.2

// Configuration Management
github.com/kelseyhightower/envconfig v1.4.0
gopkg.in/yaml.v3 v3.0.1
```

## Core Features

### 1. Automated Deployments

#### Supported Slash Commands

| Command | Repository | Description |
|---------|------------|-------------|
| `/deploy-dashboard` | `blockops-sh/user-dashboard-client-v2` | Deploy dashboard service |
| `/deploy-api` | `blockops-sh/api-core-service` | Deploy API service |
| `/deploy-proxy` | TBD | Deploy proxy service (planned) |

#### Deployment Flow

```mermaid
sequenceDiagram
    participant U as User
    participant S as Slack
    participant P as Ponos
    participant G as GitHub
    participant K as Kubernetes

    U->>S: /deploy-dashboard
    S->>P: Slash command webhook
    P->>P: Validate request signature
    P->>G: Create deployment via GitHub API
    G->>K: Trigger deployment pipeline
    P->>S: Send deployment status
    S->>U: Display deployment confirmation
```

### 2. Chain Management Commands

#### Update Commands

| Command | Purpose |
|---------|---------|
| `/update-chain` | Update Polkadot to latest version |
| `/update-network` | Update network configurations |

#### Update Process
1. **Fetch Latest Tags** - Query Docker registry for newest versions
2. **Parse YAML Files** - Extract current image versions from K8s manifests  
3. **Create Updates** - Generate new YAML with updated image tags
4. **Submit PR** - Create pull request with changes
5. **Notify Slack** - Report update status to requesting user

### 3. Automated Monitoring & Reporting

#### Scheduled Reports (Every 30 minutes)

**New User Signups Report**
- **Target Channel**: `C05TT7LULP8`
- **Data Source**: MySQL `users` table
- **Content**: User details, email, signup time

**Support Messages Report**
- **Target Channel**: `C06GTAC99T9` 
- **Data Source**: MySQL `supportMessages` table
- **Content**: Customer name, email, message content, timestamp

```go
// Example report structure
type user struct {
    FirstName string    `json:"firstName" bun:"firstName"`
    LastName  string    `json:"lastName" bun:"lastName"`
    Email     string    `json:"email"`
    CreatedAt time.Time `json:"createdAt" bun:"createdAt"`
}
```

### 4. AI-Powered Release Analysis

#### AI Agent Capabilities
- **Release Evaluation** - Analyze blockchain release notes and metadata
- **Risk Assessment** - Determine update severity and impact
- **Decision Making** - Recommend update strategies
- **Network Detection** - Identify which blockchain networks are affected

#### Decision Matrix

```go
type UpdateDecision struct {
    ShouldUpdate     bool     `json:"should_update"`
    DetectedNetworks []string `json:"detected_networks"`
    Severity         string   `json:"severity"`         // low/medium/high/critical
    UpdateStrategy   string   `json:"update_strategy"`  // create_pr/immediate/wait
    Reasoning        string   `json:"reasoning"`
}
```

## Code Structure

### Directory Layout

```
ponos/
├── cmd/                    # Application entry points
│   ├── main.go            # HTTP server and main application logic
│   ├── github.go          # GitHub API integration and deployment handling
│   ├── agent.go           # AI agent for release analysis
│   ├── scheduler.go       # Scheduled tasks and reporting
│   ├── webhook.go         # External webhook handlers
│   └── responses.go       # Slack response utilities
├── config/                # Configuration management
│   └── config.go          # Environment and project configuration
├── k8s/                   # Kubernetes manifests
│   └── ponos/             # Production deployment configs
├── .github/workflows/     # CI/CD pipelines
├── Dockerfile             # Container build definition
├── docker-compose.yml     # Local development setup
├── Taskfile.yml          # Task runner configuration
├── config.yaml           # Project deployment configuration
└── go.mod                # Go module dependencies
```

### Core Components

#### 1. Main Server (`cmd/main.go`)

```go
type Bot struct {
    client        *slack.Client
    db            *sql.DB
    config        *config.Config
    logger        *slog.Logger
    githubHandler *GitHubDeployHandler
}
```

**Key Responsibilities:**
- HTTP server lifecycle management
- Slack event processing
- Request authentication and validation
- Route handling for different endpoints
- Graceful shutdown handling

**Main Endpoints:**
- `/slack/events` - Slack Events API
- `/slack/command` - Slash commands
- `/webhooks/releases` - External release notifications

#### 2. GitHub Integration (`cmd/github.go`)

**Repository Configuration:**
```go
type RepoConfig struct {
    Name          string  // Repository full name
    DefaultBranch string  // Target deployment branch
    SourceBranch  string  // Source branch for merges
}
```

**Deployment Handler:**
```go
type GitHubDeployHandler struct {
    bot         *Bot
    repoConfigs map[string]RepoConfig
    pemKey      []byte  // GitHub App private key
    appID       int64   // GitHub App ID
    installID   int64   // GitHub App Installation ID
}
```

**Key Operations:**
- JWT token generation for GitHub App authentication
- Repository dispatch event creation
- Branch and pull request management
- YAML file parsing and modification for image updates

#### 3. AI Agent (`cmd/agent.go`)

```go
type NodeOperatorAgent struct {
    llm    llms.LLM          // OpenAI client
    logger *slog.Logger
}
```

**Analysis Workflow:**
1. **Prompt Construction** - Build context from release data
2. **LLM Query** - Send structured prompt to OpenAI
3. **Response Parsing** - Extract decisions from AI response
4. **Action Planning** - Determine next steps based on analysis

#### 4. Scheduler (`cmd/scheduler.go`)

**Scheduled Jobs:**
- **User Monitoring** - Track new registrations
- **Support Tracking** - Monitor customer messages
- **Database Queries** - Efficient time-windowed queries
- **Slack Notifications** - Formatted reporting to channels

### Configuration Management

#### Environment Configuration

```go
type Config struct {
    MySQLDSN        string `envconfig:"MYSQL_DSN"`
    GitHubPEMKey    string `envconfig:"GITHUB_PEM_KEY"`
    GitHubAppID     int64  `envconfig:"GITHUB_APP_ID"`
    GitHubInstallID int64  `envconfig:"GITHUB_INSTALL_ID"`
    SlackToken      string `envconfig:"SLACK_TOKEN"`
    SlackSigningKey string `envconfig:"SLACK_SIGNING_SECRET"`
    Port            string `envconfig:"PORT" default:"8080"`
}
```

#### Project Configuration

```yaml
version: 1
projects:
  - network: polkadot
    owner: blockops-sh
    name: blockops-sre
    branch: main
    paths:
      - k8s/assethub-polkadot-replica/sts.yml
      - k8s/blockops-polkadot-archive/statefulset.yaml
      - k8s/blockops-assethub-polkadot/sts.yml
```

## Deployment

### Kubernetes Deployment

#### Production Configuration
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ponos
  namespace: ponos
spec:
  replicas: 1
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
```

#### Resource Management
- **CPU**: Not specified (burstable)
- **Memory**: Not specified (burstable)
- **Port**: 8700
- **Namespace**: ponos

#### Volume Mounts
- **GitHub PEM Key**: `/app/gh.pem` (from secret)

### Docker Configuration

#### Multi-stage Build
```dockerfile
FROM golang:1.24-alpine AS builder
# ... build process
FROM gcr.io/distroless/static-debian12:nonroot
# ... runtime image
```

**Benefits:**
- Minimal attack surface with distroless base
- Optimized binary size with build flags
- Non-root user execution
- CA certificates included

### CI/CD Pipeline

#### GitHub Actions Workflow
1. **Trigger**: Push to `main` branch
2. **Build**: Multi-platform Docker image
3. **Push**: Image to Docker Hub as `blockopsnetwork/ponos:${SHA}`
4. **Deploy**: Update Kubernetes deployment
5. **Verify**: Rollout status monitoring

## API Endpoints

### Slack Integration

#### Event Handler: `/slack/events`
- **Method**: POST
- **Purpose**: Handle Slack Events API callbacks
- **Authentication**: Slack signature verification
- **Supported Events**:
  - `url_verification` - Initial setup verification
  - `app_mention` - Bot mentions in channels
  - `message` - Direct messages to bot

#### Command Handler: `/slack/command`
- **Method**: POST  
- **Purpose**: Process slash commands
- **Authentication**: Slack signature verification
- **Response Format**: JSON with Slack message blocks

### Webhook Endpoints

#### Release Webhook: `/webhooks/releases`
- **Method**: POST
- **Purpose**: Receive external release notifications
- **Processing**: AI analysis and decision making
- **Response**: JSON acknowledgment

```json
{
  "event_type": "release_published",
  "username": "blockchain-team",
  "timestamp": "2024-01-15T10:30:00Z",
  "repositories": [...],
  "releases": {...}
}
```

## Security

### Authentication & Authorization

#### Slack Security
- **Signature Verification**: HMAC-SHA256 request validation
- **Timestamp Validation**: Prevents replay attacks
- **Secret Management**: Signing secrets stored in Kubernetes secrets

#### GitHub Security
- **App Authentication**: JWT tokens with RSA-256 signatures
- **Private Key Security**: PEM keys mounted as Kubernetes secrets
- **Scoped Permissions**: Limited to specific repositories and actions

#### Database Security
- **Connection Security**: MySQL DSN with credentials in secrets
- **Query Safety**: Parameterized queries via Bun ORM
- **Connection Pooling**: Managed database connections

### Network Security
- **TLS Termination**: Handled by Kubernetes ingress
- **Internal Communication**: Cluster-internal service communication
- **Firewall Rules**: Kubernetes network policies (if configured)

## Development

### Local Development Setup

#### Prerequisites
```bash
# Required tools
go 1.24+
docker
kubectl
task (taskfile.dev)

# Required services
mysql
```

#### Environment Setup
```bash
# Clone repository
git clone https://github.com/blockops-sh/ponos
cd ponos

# Set environment variables
export MYSQL_DSN="root:root@tcp(127.0.0.1:3306)/ponos?parseTime=true"
export SLACK_TOKEN="xoxb-your-slack-token"
export SLACK_SIGNING_SECRET="your-signing-secret"
export GITHUB_APP_ID="your-app-id"
export GITHUB_INSTALL_ID="your-install-id"
export GITHUB_PEM_KEY="/path/to/private-key.pem"
```

#### Development Commands
```bash
# Build application
task build

# Run in development mode
task dev

# Run tests
task test

# Build Docker image
task docker-build

# Run Docker container
task docker-run
```

#### Taskfile (go-task)

The Taskfile is a developer convenience to run common operations consistently across environments. It’s similar to a Makefile but uses YAML and is cross-platform.

Overview and usage:
- Install the task runner from https://taskfile.dev or your package manager (e.g., brew install go-task).
- From the `ponos` directory:
  - `task build` builds a local binary to `bin/ponos`.
  - `task run` starts the app with environment variables from your shell, falling back to defaults via `ensure-env`.
  - `task dev` runs the app with hardcoded defaults from the Taskfile.
  - `task test` executes Go tests across the repo.
  - `task docker-build` builds a Docker image using the multi-stage `Dockerfile`.
  - `task docker-run` runs the image locally with default environment variables.

Tasks breakdown:
- `build`:
  - `go build -o bin/ponos ./cmd/main.go`
- `run`:
  - Runs `ensure-env` to export needed env vars, then `go run ./cmd/main.go`
- `ensure-env` (internal helper):
  - Exports `MYSQL_DSN`, `PORT`, `GITHUB_APP_ID`, `GITHUB_INSTALL_ID`, `GITHUB_PEM_KEY`, `SLACK_TOKEN`, `SLACK_SIGNING_SECRET`, `SLACK_APP_ID` from the shell if set; otherwise uses defaults
- `test`:
  - `go test -v ./...`
- `dev`:
  - Sets defaults inline, then `go run ./cmd/*.go`
- `docker-build`:
  - `docker build -t ponos:latest .`
- `docker-run`:
  - Runs the container mapping `8080:8080` and passes default env vars

Variables (defaults defined in Taskfile):
- `DEFAULT_MYSQL_DSN`
- `DEFAULT_PORT`
- `DEFAULT_GITHUB_APP_ID`
- `DEFAULT_GITHUB_INSTALL_ID`
- `DEFAULT_GITHUB_PEM_KEY`
- `DEFAULT_SLACK_TOKEN`
- `DEFAULT_SLACK_SIGNING_SECRET`

Caveats and recommendations:
- `SLACK_APP_ID` is referenced in tasks but does not have a corresponding `DEFAULT_SLACK_APP_ID` in the `vars` section. Add a default or remove references if unused.
- Naming inconsistency: `ensure-env` checks `GITHUB_INSTALLATION_ID` but exports `GITHUB_INSTALL_ID`. Standardize on one to avoid confusion.
- Port mismatch: The `Dockerfile` exposes `8700`, while `docker-run` sets `PORT=8080` and maps `8080:8080`. This works, but consider aligning `EXPOSE` or the mapping for clarity.
- Secrets and credentials in defaults are placeholders. Prefer setting real values in your shell or an `.env` loaded into your shell, rather than hardcoding sensitive values in the Taskfile.

### Code Quality

#### Logging
- **Structured Logging**: JSON format via `slog`
- **Log Levels**: Info, Error with contextual data
- **Request Tracing**: User and channel identification

#### Error Handling
- **Graceful Degradation**: Fallback modes for AI agent
- **User Feedback**: Clear error messages in Slack
- **Operational Monitoring**: Detailed error logging

#### Testing Strategy
- **Unit Tests**: Core logic validation
- **Integration Tests**: API endpoint testing
- **Manual Testing**: Slack command verification

## Troubleshooting

### Common Issues

#### Deployment Failures
**Symptom**: Slash commands fail to trigger deployments
**Diagnosis**:
- Check GitHub App permissions
- Verify PEM key validity
- Confirm repository access

**Resolution**:
```bash
# Verify GitHub App authentication
kubectl logs -n ponos deployment/ponos | grep "github"

# Check secrets
kubectl get secrets -n ponos
kubectl describe secret github-pem-secret -n ponos
```

#### Database Connection Issues
**Symptom**: Scheduled reports not working
**Diagnosis**:
- MySQL connectivity
- Database permissions
- Table existence

**Resolution**:
```bash
# Check database connection
kubectl logs -n ponos deployment/ponos | grep "database\|mysql"

# Verify secret
kubectl get secret ponos-secret -n ponos -o yaml
```

#### AI Agent Problems
**Symptom**: Release analysis not working
**Diagnosis**:
- OpenAI API key validity
- Rate limiting
- Model availability

**Resolution**:
```bash
# Check AI agent logs
kubectl logs -n ponos deployment/ponos | grep -i "openai\|agent\|llm"
```

### Monitoring

#### Health Checks
- **Kubernetes Probes**: Readiness and liveness checks
- **Application Metrics**: Prometheus annotations configured
- **Log Aggregation**: Structured JSON logs for parsing

#### Key Metrics to Monitor
- **Request Volume**: Slack command frequency
- **Response Times**: GitHub API call latency
- **Error Rates**: Failed deployments and AI calls
- **Database Performance**: Query execution times

### Maintenance

#### Regular Tasks
- **Secret Rotation**: GitHub PEM keys, Slack tokens
- **Dependency Updates**: Go modules, base images
- **Database Maintenance**: Query optimization, index management
- **Log Retention**: Archive and cleanup strategies

#### Backup Strategy
- **Database Backups**: Regular MySQL dumps
- **Configuration Backups**: Kubernetes manifests and secrets
- **Code Backups**: Git repository with proper branching

---

## Contributing

### Code Standards
- **Go Formatting**: Use `gofmt` and `golint`
- **Error Handling**: Comprehensive error checking
- **Documentation**: Inline comments for complex logic
- **Testing**: Unit tests for new features

### Pull Request Process
1. Create feature branch from `main`
2. Implement changes with tests
3. Update documentation as needed
4. Submit PR with clear description
5. Address review feedback
6. Merge after approval

### Release Process
1. Tag release with semantic versioning
2. Update changelog
3. Deploy to staging environment
4. Verify functionality
5. Promote to production

---

*This documentation covers Ponos v1.0 as of the current codebase state. For updates and additional information, consult the source code and inline documentation.*