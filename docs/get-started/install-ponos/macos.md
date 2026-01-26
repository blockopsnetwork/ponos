---
description: Install Ponos on macOS.
---

# Installation on macOS

## Quick install (recommended)

```bash
curl -sSL https://nodeoperator.ai/install.sh | sh
```

This installs the `ponos` binary.

## Build from source

Prereqs:

* Go (1.20+)
* Git
* Make

```bash
git clone https://github.com/blockops-sh/ponos
cd ponos
make build-ponos
```

The binary is produced at `./bin/ponos`.

## Download the binary from GitHub Releases

Pick the correct asset for your CPU:

* `ponos_Darwin_x86_64.tar.gz` (Intel)
* `ponos_Darwin_arm64.tar.gz` (Apple Silicon)

Replace `vX.Y.Z` with a real tag from the GitHub releases page.

Example for Apple Silicon:

```bash
VERSION="vX.Y.Z"
curl -L -o ponos.tar.gz \
  "https://github.com/blockopsnetwork/ponos/releases/download/${VERSION}/ponos_Darwin_arm64.tar.gz"
tar -xzf ponos.tar.gz
sudo mv ponos /usr/local/bin/ponos
```

Verify the binary is on your PATH:

```bash
which ponos
```

## Run the TUI

```bash
ponos
```

This is the Ponos TUI after launch. You can begin prompting the agent here

<figure><img src="../../.gitbook/assets/Screenshot 2026-01-26 at 08.25.04.png" alt=""><figcaption></figcaption></figure>

## Upgrade Ponos

```bash
ponos upgrade
```

You can target a specific version:

```bash
ponos upgrade --version vX.Y.Z
```
