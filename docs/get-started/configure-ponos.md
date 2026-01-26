---
description: Create a ponos.yml and set required integrations.
---

# Configure Ponos

Ponos reads configuration from `ponos.yml`. By default it looks for the file in:

1. `./ponos.yml` (current directory)
2. `~/.config/ponos/ponos.yml`
3. `/etc/ponos/ponos.yml`

You can override the path with `PONOS_CONFIG_PATH`.

## Get your API key

Ponos uses an API key to authenticate with the NodeOperator backend.

1. Go to `https://platform.nodeoperator.ai/`

<figure><img src="../.gitbook/assets/Screenshot 2026-01-26 at 08.28.45.png" alt=""><figcaption></figcaption></figure>

1. Provide your email and a Magic link will be sent to you

<figure><img src="../.gitbook/assets/Screenshot 2026-01-26 at 08.34.59.png" alt=""><figcaption></figcaption></figure>

1. Create or copy your API key

<figure><img src="../.gitbook/assets/Screenshot 2026-01-26 at 08.36.13.png" alt=""><figcaption></figcaption></figure>

1. Add it to your `ponos.yml` as `api_key`

## Create a config file

Copy the example config:

```bash
cp ponos.yml.example ponos.yml
```

## Required settings

At minimum, set:

* `api_endpoint` (Nodeoperator API base URL)
* `api_key` (from https://platform.nodeoperator.ai/)
* GitHub auth (PAT or GitHub App)
* Slack bot token + signing secret

Example (minimal):

```yaml
version: 1

api_endpoint: "https://api.nodeoperator.ai"
api_key: "nai_..."

integrations:
  github:
    token: "ghp_xxx"
    # OR GitHub App:
    # app_id: "123456"
    # install_id: "654321"
    # pem_key: "/path/to/your.pem"
    # bot_name: "ponos-bot"

  slack:
    token: "xoxb-xxx"
    signing_key: "your_slack_signing_secret"
    verify_token: "optional"
    channel: "sre-tasks"
```

## Optional: Telescope observability

Add this if you want metrics and logs via Telescope (Prometheus/Loki):

```yaml
integrations:
  telescope:
    project_id: ""
    project_name: ""
    prometheus_url: ""
    prometheus_username: ""
    prometheus_password: ""
    loki_url: ""
    loki_username: ""
    loki_password: ""
```

## Optional: Diagnostics

Diagnostics uses either `telescope` or `kubernetes` as the provider.

```yaml
diagnostics:
  enabled: false
  provider: "telescope"
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
```

## Projects (for GitHub updates)

Projects map networks to repos and file paths that Ponos can update:

```yaml
projects:
  - network: "ethereum"
    project_name: "mainnet"
    owner: "your-org"
    name: "infra-repo"
    branch: "main"
    paths:
      - "deployments/ethereum.yaml"
```

## Next step

Run the TUI:

```bash
ponos
```
