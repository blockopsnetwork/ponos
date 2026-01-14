#!/bin/sh
set -eu

REPO="blockopsnetwork/ponos"
GITHUB_RELEASES="https://github.com/${REPO}/releases"
GITHUB_API="https://api.github.com/repos/${REPO}"

VERSION=""
INSTALL_DIR=""
FORCE=0
NO_PROMPT=0
DRY_RUN=0
NO_VERIFY=0

usage() {
  cat <<'EOF'
Ponos installer

Usage:
  install.sh [options]

Options:
  --version <vX.Y.Z>  Install a specific version (default: latest)
  --path <dir>        Install directory (default: /usr/local/bin or ~/.local/bin)
  --force             Overwrite existing binary without prompting
  --no-prompt         Do not prompt; fail if input is required
  --dry-run           Print actions without making changes
  --no-verify         Skip checksum verification
  -h, --help          Show help
EOF
}

log() { printf '%s\n' "$*"; }
err() { printf 'Error: %s\n' "$*" >&2; }
die() { err "$*"; exit 1; }

need_cmd() { command -v "$1" >/dev/null 2>&1; }

fetch() {
  url=$1
  out=$2
  if need_cmd curl; then
    curl -fsSL "$url" -o "$out"
  elif need_cmd wget; then
    wget -qO "$out" "$url"
  else
    die "curl or wget is required"
  fi
}

confirm() {
  if [ "$NO_PROMPT" -eq 1 ]; then
    return 1
  fi
  printf '%s [y/N] ' "$1"
  read -r answer
  case "$answer" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      VERSION=$2
      shift 2
      ;;
    --path)
      INSTALL_DIR=$2
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --no-prompt)
      NO_PROMPT=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --no-verify)
      NO_VERIFY=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
done

OS=$(uname -s || echo "")
case "$OS" in
  Darwin) OS="Darwin" ;;
  Linux) OS="Linux" ;;
  *) die "Unsupported OS: $OS" ;;
esac

ARCH=$(uname -m || echo "")
case "$ARCH" in
  x86_64|amd64) ARCH="x86_64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

if [ -z "$INSTALL_DIR" ]; then
  for dir in /usr/local/bin /usr/bin "${HOME}/.local/bin"; do
    if [ -d "$dir" ] && [ -w "$dir" ]; then
      INSTALL_DIR="$dir"
      break
    fi
  done
  if [ -z "$INSTALL_DIR" ]; then
    INSTALL_DIR="${HOME}/.local/bin"
  fi
fi

if [ ! -d "$INSTALL_DIR" ]; then
  if [ -w "$(dirname "$INSTALL_DIR")" ]; then
    mkdir -p "$INSTALL_DIR"
  else
    die "Install directory does not exist and is not writable: $INSTALL_DIR"
  fi
fi

if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
  if need_cmd curl; then
    latest_json=$(curl -fsSL "${GITHUB_API}/releases/latest")
  elif need_cmd wget; then
    latest_json=$(wget -qO- "${GITHUB_API}/releases/latest")
  else
    die "curl or wget is required"
  fi
  VERSION=$(printf '%s' "$latest_json" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$VERSION" ] || die "Failed to determine latest version"
else
  case "$VERSION" in
    v*) ;;
    *) VERSION="v$VERSION" ;;
  esac
fi

ASSET="ponos_${OS}_${ARCH}.tar.gz"
CHECKSUMS="checksums.txt"
BASE_URL="${GITHUB_RELEASES}/download/${VERSION}"
ASSET_URL="${BASE_URL}/${ASSET}"
CHECKSUMS_URL="${BASE_URL}/${CHECKSUMS}"

log "Installing ponos ${VERSION} (${OS}/${ARCH})"
log "Install directory: ${INSTALL_DIR}"

if [ "$DRY_RUN" -eq 1 ]; then
  log "Dry run enabled"
  log "Would download: ${ASSET_URL}"
  log "Would verify: ${CHECKSUMS_URL}"
  log "Would install to: ${INSTALL_DIR}/ponos"
  exit 0
fi

tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t ponos)
cleanup() { rm -rf "$tmp_dir"; }
trap cleanup EXIT INT TERM

fetch "$ASSET_URL" "${tmp_dir}/${ASSET}"
fetch "$CHECKSUMS_URL" "${tmp_dir}/${CHECKSUMS}"

if [ "$NO_VERIFY" -ne 1 ]; then
  expected=$(grep " ${ASSET}\$" "${tmp_dir}/${CHECKSUMS}" | awk '{print $1}')
  [ -n "$expected" ] || die "Checksum not found for ${ASSET}"
  if need_cmd sha256sum; then
    actual=$(sha256sum "${tmp_dir}/${ASSET}" | awk '{print $1}')
  elif need_cmd shasum; then
    actual=$(shasum -a 256 "${tmp_dir}/${ASSET}" | awk '{print $1}')
  else
    die "sha256sum or shasum is required for verification"
  fi
  [ "$expected" = "$actual" ] || die "Checksum verification failed"
fi

tar -xzf "${tmp_dir}/${ASSET}" -C "$tmp_dir"

bin_path="${tmp_dir}/ponos"
if [ ! -f "$bin_path" ]; then
  bin_path=$(find "$tmp_dir" -maxdepth 2 -type f -name ponos | head -n 1)
fi
[ -n "$bin_path" ] || die "Installed binary not found in archive"

SUDO=""
if [ ! -w "$INSTALL_DIR" ]; then
  if need_cmd sudo; then
    if confirm "Install requires sudo to write to ${INSTALL_DIR}. Proceed?"; then
      SUDO="sudo"
    else
      die "Insufficient permissions for ${INSTALL_DIR}"
    fi
  else
    die "Insufficient permissions for ${INSTALL_DIR} (sudo not available)"
  fi
fi

if [ -f "${INSTALL_DIR}/ponos" ] && [ "$FORCE" -ne 1 ]; then
  if confirm "ponos already exists in ${INSTALL_DIR}. Overwrite?"; then
    :
  else
    die "Aborted"
  fi
fi

if need_cmd install; then
  ${SUDO} install -m 0755 "$bin_path" "${INSTALL_DIR}/ponos"
else
  ${SUDO} cp "$bin_path" "${INSTALL_DIR}/ponos"
  ${SUDO} chmod 0755 "${INSTALL_DIR}/ponos"
fi

log "ponos installed to ${INSTALL_DIR}/ponos"

case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    log "Note: ${INSTALL_DIR} is not in your PATH"
    log "Add this line to your shell profile (~/.zshrc or ~/.bashrc):"
    log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

config_example="${tmp_dir}/ponos.yml.example"
if [ ! -f "$config_example" ]; then
  config_example=$(find "$tmp_dir" -maxdepth 2 -type f -name ponos.yml.example | head -n 1 || true)
fi

config_target="${HOME}/.config/ponos/ponos.yml"
if [ ! -f "ponos.yml" ] && [ ! -f "$config_target" ] && [ ! -f "/etc/ponos/ponos.yml" ] && [ -n "$config_example" ] && [ -f "$config_example" ]; then
  if confirm "Create ${config_target} from example config?"; then
    mkdir -p "$(dirname "$config_target")"
    cp "$config_example" "$config_target"
    log "Config created at ${config_target}"
    log "Set PONOS_CONFIG_PATH to point to a different config if needed."
  else
    log "Skipping config creation. Place ponos.yml in your working directory or set PONOS_CONFIG_PATH."
  fi
fi

log "Example config: https://github.com/blockopsnetwork/ponos/blob/main/ponos.yml.example"
log ""
log "Installation completed!"
log "Next steps:"
log "1. Visit https://platform.nodeoperator.ai/"
log "2. Enter your email to receive a magic link"
log "3. Open the link to sign in and copy your API key"
log "4. Paste api_key into ponos.yml"
log "5. Run: ponos"
