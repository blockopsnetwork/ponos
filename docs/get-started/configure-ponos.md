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

This is the NodeOperator login screen where you start the magic-link flow.

2. Provide your email and a Magic link will be sent to you

<figure><img src="../.gitbook/assets/Screenshot 2026-01-26 at 08.34.59.png" alt=""><figcaption></figcaption></figure>

Enter your email and use the link in your inbox to sign in.

3. Create or copy your API key

<figure><img src="../.gitbook/assets/Screenshot 2026-01-26 at 08.36.13.png" alt=""><figcaption></figcaption></figure>

Copy the API key shown here and keep it safe. You will paste it into `ponos.yml`.

4. Add it to your `ponos.yml` as `api_key`

## Create a config file

Copy the example config:

```bash
cp ponos.yml.example ponos.yml
```

## Configuring Ponos for Client Updates and Management

This section is required for upgrade workflows and GitOps PR creation.

At minimum, set:

- `api_endpoint` (NodeOperator API base URL)
- `api_key` (from https://platform.nodeoperator.ai/)
- GitHub auth (PAT or GitHub App)
- Slack bot token + signing secret

Minimal example:

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

Once you save `ponos.yml`, restart Ponos. To verify the TUI is connected to
the API, send a simple prompt like “hello.” You should see a response like this:

<figure><img src="../.gitbook/assets/Screenshot 2026-01-26 at 10.19.46.png" alt=""><figcaption></figcaption></figure>

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

## Node Observability & Incident Management

Use this section if you want diagnostics, logs, and metrics-driven analysis.

### Telescope (Prometheus/Loki)

Telescope is an all‑in‑one observability tool for blockchain nodes. It removes
the need to hand‑maintain separate scrape configs for each component by setting
up metrics and logs automatically based on the network you run.

Open source: https://github.com/blockopsnetwork/telescope

If you already run Prometheus and Loki, point Ponos at your own endpoints here.
Make sure your metrics and logs are labeled with `project_id` or `project_name`
so Telescope can scope queries correctly.

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

### Diagnostics

Diagnostics is disabled by default. It only runs when `diagnostics.enabled` is
set to `true`. The provider can be `telescope` or `kubernetes`.

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

## Automatic client updates (optional)

Ponos can listen for release webhooks and create upgrade PRs automatically. This
is off by default and only runs when the server is started with the release
listener enabled.

In `ponos.yml`:

```yaml
server:
  enable_release_listener: true
```

And set the env var before starting the server:

```bash
export ENABLE_RELEASE_LISTENER=true
```

## Next step

Run the TUI:

```bash
ponos
```
