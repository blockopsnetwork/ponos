# Ponos

Ponos is a Slack bot that does a bunch of automations for Blockops.

## Features

- [x] Automated deployments via Slack slash commands with Github apps <https://github.com/apps/blockops-deploy-bot>
  - [x] Backend
  - [ ] Blockproxy
- [ ] Allow us retry deployment for specific projects in mission control if it fails instead of waiting for them to reach out to us if we have fixed the issue
- [ ] Migrate org to enterprise plan
- [x] list recent user signups
- [x] list support messages in slack
- [ ] Allow management to update plans
  - [ ] Update plan price
  - [ ] Manage plan feature

and more

## Building and Running

### Using Task

```bash
# Build the application
task build

# Run the application
task run
```

### Using Docker

```bash
# Build the Docker image
docker build -t ponos .

# Run the container
docker run -d \
  -e GITHUB_APP_ID=<your-github-app-id> \
  -e GITHUB_INSTALL_ID=<your-github-installation-id> \
  -e GITHUB_PEM_KEY=/app/gh.pem \
  -e SLACK_BOT_TOKEN=<your-slack-bot-token> \
  -v $(pwd)/gh.pem:/app/gh.pem \
  ponos
```

## Usage

The bot responds to the following slash commands in Slack:

- `/deploy-api` - Deploy the API service

more soon
