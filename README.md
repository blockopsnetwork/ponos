# NodeOperator.ai

**Intelligent Blockchain Operations Agent**

NodeOperator.ai is a powerful AI-driven tool designed to automate blockchain operations, diagnostics, and deployments. It combines a Python-based intelligent backend (`agent-core`) with a Go-based interface (`ponos`) that works as both a Slack bot and a Terminal User Interface (TUI).

![Architecture](docs/architecture.mermaid)

## Features

*   **Intelligent Diagnostics**: Automatically diagnose issues with blockchain nodes (Kubernetes/Docker) using LLMs.
*   **Slack Integration**: Interact with the agent via Slack slash commands (`/diagnose`, `/deploy-dashboard`, etc.) and natural language.
*   **Terminal UI (TUI)**: A rich command-line interface for local interaction and monitoring.
*   **Automated Upgrades**: Detects new blockchain client releases and automates the upgrade process via GitHub Pull Requests.
*   **MCP Support**: Built on the Model Context Protocol (MCP) to extensibly interact with GitHub, Slack, and Kubernetes.

## Architecture

The project consists of two main components:

1.  **Agent Core (`agent-core`)**: The "Brain". A Python service using FastAPI and LangChain. It handles complex reasoning, tool execution, and MCP connections.
2.  **Ponos (`ponos-new`)**: The "Body". A Go application that handles user interfaces (Slack, TUI) and orchestrates requests to the core.

## Getting Started

### Prerequisites

*   **Go**: 1.21 or later
*   **Python**: 3.10 or later
*   **Docker**: For running MCP servers (optional but recommended)
*   **OpenAI API Key**: Required for the LLM.

### Installation

1.  **Clone the repository**:
    ```bash
    git clone https://github.com/blockops-sh/nodeoperator.ai.git
    cd nodeoperator.ai
    ```

2.  **Install Agent Core Dependencies**:
    ```bash
    cd agent-core
    pip install -r requirements.txt
    cd ..
    ```

3.  **Build Ponos**:
    ```bash
    make build-ponos
    ```

### Configuration

1.  **Agent Core**:
    Create a `.env` file in `agent-core/` (see `agent-core/config.py` for variables).
    ```bash
    OPENAI_API_KEY=sk-...
    # Add other MCP config variables
    ```

2.  **Ponos**:
    Copy the sample env/config and edit it:
    ```bash
    cp ponos-new/.env.sample ponos-new/.env
    cp ponos-new/config.yaml ponos-new/config.yaml # adjust paths as needed
    ```
    - **TUI log file**: set `PONOS_TUI_LOG_PATH` to a writable location (defaults to system temp dir); logs are kept off stdout/stderr so they don’t interfere with the TUI.
    - **Release listener (optional)**: set `ENABLE_RELEASE_LISTENER=true` to enable `/webhooks/releases` in server mode. Subscribe your webhook via https://relay.blockops.network pointing to `https://<your-host>/webhooks/releases` to receive release event notifications. Provide your Slack/GitHub creds so ponos can act on releases.

### Running the System

You can use the provided `Makefile` to run the services.

1.  **Start Agent Core** (in terminal 1):
    ```bash
    make run-core
    ```

2.  **Start Ponos TUI** (in terminal 2):
    ```bash
    make run-ponos-tui
    ```
    *Or start the Slack Server:*
    ```bash
    make run-ponos
    ```

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
