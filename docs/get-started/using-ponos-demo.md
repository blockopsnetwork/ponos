---
description: Demo setup using the public Playground repo.
---

# Using Ponos (demo)

This page shows a sample demo setup using the public repo to represent an ideal infrastructire-as-code repository and how nodeoperator agent interacts with it, so you can test deploy, upgrade and diagnostics workflows without wiring a private repository.

Repository: https://github.com/blockopsnetwork/playground

Example manifest used in this demo: https://github.com/blockopsnetwork/playground/blob/main/ethereum/ethereum-hoodi.yaml

## Upgrade demo

Add this to your `ponos.yml` under `projects`:

```yaml
projects:
  - network: ethereum
    project_name: ethereum-hoodi-fullnode
    owner: blockopsnetwork
    name: playground
    branch: main
    paths:
      - ethereum/ethereum-hoodi.yaml
```

Example prompts:

* "Upgrade ethereum hoodi to the latest version"
* "Show me available Geth versions for hoodi"

### Upgrade demo video

{% embed url="https://youtu.be/dfi_1cAbCbk" %}

## Diagnostics demo

Diagnostics only run when enabled in `ponos.yml` and the provider is set to `telescope` or `kubernetes`.

Example prompts:

* "Diagnose mainnet Ethereum nodes"
* "Check why Geth pods are failing on testnet"

{% embed url="https://youtu.be/BopV6C_kHNQ" %}



## Ponos.yml config (Diagnostic)

This is the full `ponos.yml` used in the demo, with secrets redacted:

```yaml
version: 1

# dev
api_endpoint: "http://localhost:8001"
api_key: "nai-REDACTED"

# prod
# api_endpoint: "https://api.nodeoperator.ai"
# api_key: "nai-REDACTED"

integrations:
  github:
    token: "ghp_REDACTED"

  slack:
    token: "xoxb-REDACTED"
    team_id: "T016BSZRXPF"
    signing_key: "REDACTED"

  telescope:
    project_id: "random-id"
    project_name: "rnadom-name"
    prometheus_url: "<prometheus_datasource_url>"
    prometheus_username: "prometheus_username"
    prometheus_password: "******"
    loki_url: "https://loki.<loki_datasource_url>"
    loki_username: "loki_username"
    loki_password: "********"

diagnostics:
  enabled: true
  provider: telescope
  github:
    owner: "blockopsnetwork"
    repo: "playground"
  slack:
    channel: "sre-tasks"
  monitoring:
    service: "lively-elisabetta"
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
