---
description: A working demo setup using the public Playground repo.
---

# Using Ponos (demo)

This demo uses the public Playground repo so you can test upgrades and diagnostics without wiring a private repository.

Repository: https://github.com/blockopsnetwork/playground

Example manifest used in this demo: https://github.com/blockopsnetwork/playground/blob/main/ethereum/ethereum-hoodi.yaml

## Sample config (Upgrade Workflow)

Add this to your `ponos.yml` under `projects`:

```yaml
version: 1

api_endpoint: "https://api.nodeoperator.ai"
api_key: "nai-*******"

projects:
  - network: "ethereum"
    project_name: "hoodi"
    owner: "blockopsnetwork"
    name: "playground"
    branch: "main"
    paths:
      - "ethereum/ethereum-hoodi.yaml"
```

## Sample config (Diagnostic Workflow)

Below is a sample `ponos.yml` used for the demo, with secrets redacted:

```yaml
version: 1


api_endpoint: "https://api.nodeoperator.ai"
api_key: "nai-*******"

integrations:
  github:
    token: "ghp_*******"

  slack:
    token: "xoxb-*****"
    signing_key: "*******"

  telescope:
    project_id: "random-number"
    project_name: "random-name"
    prometheus_url: "https://prometheus-datasource_url"
    prometheus_username: "prometheus_username"
    prometheus_password: "******"
    loki_url: "https://loki.datasource_url"
    loki_username: "loki_username"
    loki_password: "*****"

diagnostics:
  enabled: true
  provider: telescope
  github:
    owner: "blockopsnetwork"
    repo: "playground"
  slack:
    channel: "sre-tasks"
  monitoring:
    service: "<project-name>"
    log_tail: 10
    eval_interval: 2

server:
  port: "8080"
  enable_release_listener: false

projects:
  - network: ethereum
    owner: blockopsnetwork
    project_name: ethereum-hoodi-fullnode
    name: playground
    branch: main
    paths:
      - ethereum/ethereum-hoodi.yaml
```

## Diagnostics demo

This assumes diagnostics are enabled in `ponos.yml` and the provider is set to
`telescope` or `kubernetes` (see Configure Ponos).

Example prompts:

- "Diagnose mainnet Ethereum nodes"
- "Check why Geth pods are failing on testnet"

## Diagnostics demo video
