# Page

# Ponos

**Ponos** (Greek: Πόνος) means *toil*, *labor*, or *sustained effort*.  

Ponos takes on that toil for node operators. It is the terminal interface for **nodeoperator.ai**, an autonomous nodeoperator agent that deploys, manages, and remediates issues with blockchain infrastructures through GitHub-based workflows.

## Interaction Interfaces

- **TUI** (Terminal UI) - Interactive terminal interface
- **Slack** - Slash commands and bot integration
- **Coming Soon**: GitHub Actions, Discord, GitHub Comments

## Installation

### Quick install (recommended)

```bash
curl -sSL https://nodeoperator.ai/install.sh | sh
```

### Build from source

```bash
git clone https://github.com/blockops-sh/ponos
cd ponos
make build-ponos
```

The binary is produced at `./bin/ponos`.

### Docker

```bash
docker pull ghcr.io/blockopsnetwork/ponos-server:latest
```

## Usage

Ponos supports two main workflows:

### 1. Upgrading Blockchain Clients and Components

**Supported Networks:**
- **Ethereum** (execution clients: Geth, Nethermind, Besu, Erigon; consensus clients: Lighthouse, Prysm, Teku, Nimbus)
- **EVM-compatible chains** (Polygon, Arbitrum, Optimism, Base, etc.)
- **Polkadot Relaychains** (Polkadot, Kusama) 
- **Cosmos chains** (experimental support)
- **Solana** (experimental support)

*Note: While we technically support all EVM chains and clients, testing has been primarily focused on Ethereum execution and consensus clients.*

**TUI Interface**
```bash
./bin/ponos
```
Once in the TUI, use prompts like:
- `"Upgrade mainnet Geth to the latest version"`
- `"Show me available Lighthouse versions for testnet"`
- `"Create a PR to update Polygon nodes to v1.2.3"`
- `"What's the current version of Besu on mainnet?"`

**Slack Interface**
```
@ponos (replace with your slacp app name) <your prompt>
```
Example Slack messages:
- `@ponos upgrade mainnet Geth to the latest version`
- `@ponos create a PR to update Polygon nodes to v1.2.3`
- `@ponos show me the current Besu version on mainne`
- `@ponos what's the recent Prysm upgrade about? Do I need to make any configurational changes? If so, create a PR to upgrade and make the chnages`

### 2. Diagnosing Blockchain Nodes on Kubernetes

**TUI Interface**
```bash
./bin/ponos
```
Once in the TUI, use prompts like:
- `"Diagnose mainnet Ethereum nodes"`
- `"Check why Geth pods are failing on testnet"`
- `"Show me resource usage for Lighthouse validators"`
- `"Analyze logs for polygon-bor nodes in the last hour"`
- `"Why is my consensus client out of sync?"`

**Slack Interface**  
```bash
/diagnose <service or prompt>
```
Example Slack commands:
- `/diagnose mainnet-geth` - Quick health check for specific service
- `/diagnose check mainnet Ethereum nodes health`
- `/diagnose why are my Geth pods failing on testnet?`

Or use @ponos for more complex requests:
- `@ponos show resource usage for Lighthouse validators`
- `@ponos analyze logs for polygon-bor nodes in the last hour`

**Server Mode (for Slack + webhooks)**
```bash
make run-ponos
```
- Enables Slack slash commands and GitHub webhook listening
- Handles background diagnostic tasks
- (Note: Will be migrating to agent-core soon)

## Configuration

Configuration is provided via `ponos.yml` file. Copy `ponos.yml.example` to `ponos.yml` and customize for your environment.

### Configuration Structure

```yaml
version: 1

api_endpoint: "https://api.nodeoperator.ai"
api_key: "your_api_key_here"

integrations:
  github:
    # Choose one authentication method:
    token: "ghp_xxx"  # Personal Access Token (PAT)
    # OR GitHub App (recommended for production):
    app_id: "123456"
    install_id: "654321"
    pem_key: "/path/to/pem/key"
    bot_name: "ponos-bot"
    mcp_url: "http://localhost:3001"

  slack:
    token: "xoxb-xxx"
    signing_key: "your_slack_signing_secret"
    verify_token: "your_slack_verification_token"
    channel: "sre-tasks"

  # Telescope observability (for node metrics, logs, and blockchain client metrics - see https://github.com/blockopsnetwork/telescope)
  telescope:
    project_id: ""
    project_name: "your-project-name"
    prometheus_url: "https://your-prometheus-endpoint"
    prometheus_username: ""
    prometheus_password: ""
    loki_url: "https://your-loki-endpoint"
    loki_username: ""
    loki_password: ""

# Diagnostics & monitoring (optional)
diagnostics:
  enabled: false
  provider: "telescope"  # Options: telescope, kubernetes
  github:
    owner: "your-org"
    repo: "infrastructure-issues"
  slack:
    channel: "sre-tasks"
  kubernetes:
    namespace: "default"
    resource_type: "deployment"
  monitoring:
    service: "your-service-name"
    log_tail: 500
    eval_interval: 2

# Server settings
server:
  port: "8080"
  enable_release_listener: false
```

### GitHub Authentication

**Personal Access Token (PAT)**
- Simpler setup for development and personal use
- Uses your personal GitHub account credentials
- Requires `repo` scope for private repositories
- Set `integrations.github.token` only

**GitHub App (Recommended)**
- Recommended for production and organizational use
- More secure with fine-grained permissions
- Operations appear as the configured bot name
- Set `integrations.github.app_id`, `install_id`, and `pem_key`

### Telescope Integration

Telescope provides observability for blockchain nodes, including system metrics (CPU, memory, disk), blockchain client metrics (sync status, peers, attestations), and container logs.

| Field | Description |
|-------|-------------|
| `project_id` | Telescope project identifier |
| `project_name` | Project name for multi-tenant scoping |
| `prometheus_url` | Prometheus/Thanos endpoint for metrics |
| `prometheus_username` | Basic auth username |
| `prometheus_password` | Basic auth password |
| `loki_url` | Loki endpoint for logs |
| `loki_username` | Basic auth username |
| `loki_password` | Basic auth password |

See [Telescope documentation](https://github.com/blockopsnetwork/telescope) for setup.

### Diagnostics Provider

The `diagnostics.provider` field determines where Ponos fetches node metrics and logs:

| Provider | Description |
|----------|-------------|
| `telescope` | Uses Telescope's Prometheus and Loki endpoints for metrics and logs |
| `kubernetes` | Queries Kubernetes API directly for pod logs and resource metrics |

### Optional Environment Variables

```bash
PONOS_TUI_LOG_PATH=/tmp/ponos.log
```

### GitHub Webhook Settings

For automated release detection and pull request creation:

Set to 'true' to enable GitHub webhook listener
```bash
ENABLE_RELEASE_LISTENER=false  
```

**Release Webhook Listener** (`ENABLE_RELEASE_LISTENER`)
- **Default**: `false` (disabled)
- **Purpose**: Automatically creates upgrade PRs when new blockchain client releases are detected
- **When enabled**: Ponos server listens for GitHub release webhooks at `/webhooks/releases`
- **Workflow**: 
  1. External systems send release notifications to Ponos webhook endpoint
  2. Ponos analyzes the release using NodeOperator.ai agent
  3. Automatically creates pull requests with version updates and deployment changes
  4. Sends summary notifications to configured Slack channel

**Setup Requirements** (when enabled):
- Ponos must be running in server mode (`make run-ponos`)
- Webhook endpoint must be publicly accessible (consider using ngrok for development)
- Subscribe to release notifications via **Relay** at [relay.blockops.network](https://relay.blockops.network)
  - Relay is our specialized service for blockchain release monitoring
  - Configure your webhook endpoint: `https://your-ponos-server.com/webhooks/releases`
  - Subscribe to specific blockchain networks you want to monitor
  - Relay will automatically send release payloads when new versions are published

**Note**: This feature is primarily for advanced automation setups. Most users can rely on manual upgrade commands via TUI or Slack interface.

### MCP Server Configuration

#### Default Mode (Hosted MCP Servers)
- By default, Ponos uses MCP servers hosted by NodeOperator.ai
- Default endpoints:
  - GitHub MCP: `github-mcp.nodeoperator.ai`
  - Slack MCP: `slack-mcp.nodeoperator.ai` 
  - Kubernetes MCP: `k8s-mcp.nodeoperator.ai`
- Source code: [github.com/blockopsnetwork/mcp-servers](https://github.com/blockopsnetwork/mcp-servers)
- No additional MCP server setup required

#### Bring Your Own MCP Servers (BYOM)
To use your own MCP servers, set these environment variables:

```bash
# Override default hosted MCP servers
GITHUB_MCP_URL=http://your-github-mcp-server:3001
SLACK_MCP_URL=http://your-slack-mcp-server:3002
K8S_MCP_URL=http://your-k8s-mcp-server:3003
```

**GitHub MCP Server**
- Handles GitHub API calls, branch creation, and pull requests
- Required for all repository operations
- Default: Uses hosted GitHub MCP server

**Slack Integration**
- Built directly into Ponos using official Slack Go SDK
- No separate Slack MCP server needed unless using custom implementation

## Repository Configuration

Ponos requires a configuration file to discover and manage your blockchain deployments. This file tells Ponos which GitHub repositories contain your node deployment configurations.

### Setup

1. Copy the example configuration:
   ```bash
   cp repo-config.yaml.example repo-config.yaml
   ```

2. Set the configuration file path:
   ```bash
   export CONFIG_YAML_PATH=repo-config.yaml
   ```

### Configuration Format

```yaml
version: 1
projects:
  - network: ethereum
    project_name: eth-consensus-client
    owner: your-org
    name: ethereum-deployments
    branch: main
    paths:
      - "deployments/prysm/values.yaml"
      - "deployments/lighthouse/values.yaml"
      - "k8s/ethereum/prysm-deployment.yaml"

  - network: ethereum
    project_name: eth-execution-client
    owner: your-org
    name: ethereum-deployments
    branch: main
    paths:
      - "deployments/geth/values.yaml"
      - "deployments/nethermind/values.yaml"
      - "k8s/ethereum/geth-deployment.yaml"

  - network: arbitrum
    project_name: arbitrum-node
    owner: your-org
    name: layer2-infrastructure
    branch: main
    paths:
      - "arbitrum/nitro/values.yaml"
      - "k8s/arbitrum-deployment.yaml"
```

### Configuration Fields

| Field | Required | Description |
|-------|----------|-------------|
| `network` | Yes | Network name (ethereum, polkadot, arbitrum, etc.) |
| `project_name` | Yes | Unique identifier for this deployment project |
| `owner` | Yes | GitHub organization or username |
| `name` | Yes | Repository name containing deployment configs |
| `branch` | No | Git branch for updates (default: "main") |
| `paths` | Yes | List of file paths containing node configurations |

### Supported File Types

Ponos can update Docker image tags in various configuration formats:

- **Kubernetes YAML** - Deployment, StatefulSet, DaemonSet manifests
- **Helm values.yaml** - Chart configuration files  
- **Docker Compose** - docker-compose.yml files
- **Custom configs** - Any YAML with `image:` or `tag:` fields


## Development

```bash
go test ./...
make build-ponos
make run-ponos
```
