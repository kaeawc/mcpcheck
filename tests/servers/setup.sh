#!/usr/bin/env bash
# Install dependencies and build artifacts for every server in tests/servers.
#
#   tests/servers/setup.sh                 # everything
#   tests/servers/setup.sh go-sdk ts-v2    # only the named server ids
#
# Idempotent: re-running only redoes work whose inputs changed. Languages
# whose toolchain is missing are skipped with a message (exit status stays 0
# for skips unless MCPCHECK_E2E_STRICT=1; any real build failure makes the
# script exit 1).
#
# Toolchains are looked up on PATH, plus these locations when present:
#   $MCPCHECK_TOOLING (default /workspaces/tooling): bin/uv, cargo/, rustup/, uv-python/
#   /workspaces/gotool/go/bin/go
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOLING="${MCPCHECK_TOOLING:-/workspaces/tooling}"

if [ -d "$TOOLING/bin" ]; then PATH="$TOOLING/bin:$PATH"; fi
if [ -d "$TOOLING/cargo/bin" ]; then
  export RUSTUP_HOME="${RUSTUP_HOME:-$TOOLING/rustup}"
  export CARGO_HOME="${CARGO_HOME:-$TOOLING/cargo}"
  PATH="$TOOLING/cargo/bin:$PATH"
fi
if [ -d "$TOOLING" ]; then
  export UV_PYTHON_INSTALL_DIR="${UV_PYTHON_INSTALL_DIR:-$TOOLING/uv-python}"
fi
if [ -x /workspaces/gotool/go/bin/go ]; then PATH="/workspaces/gotool/go/bin:$PATH"; fi
export PATH
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

BUILT=()
SKIPPED=()
FAILED=()

want() {
  [ "${#SELECTED[@]}" -eq 0 ] && return 0
  local id
  for id in "${SELECTED[@]}"; do [ "$id" = "$1" ] && return 0; done
  return 1
}

have() { command -v "$1" >/dev/null 2>&1; }

run_in() {
  local id="$1"; shift
  echo "==> $id"
  if (cd "$HERE/$id" && "$@"); then BUILT+=("$id"); else FAILED+=("$id"); echo "!! $id failed" >&2; fi
}

skip() { echo "--> skipping $1: $2"; SKIPPED+=("$1 ($2)"); }

# --- python -----------------------------------------------------------------
python_venv() {
  local pyver="$1"
  if [ ! -x .venv/bin/python ]; then
    uv venv --quiet --python "$pyver" .venv || return 1
  fi
  uv pip sync --quiet --python .venv/bin/python requirements.txt
}

setup_python() {
  local id="$1" pyver="$2"
  want "$id" || return 0
  if ! have uv; then skip "$id" "uv not found (curl -LsSf https://astral.sh/uv/install.sh | sh)"; return 0; fi
  run_in "$id" python_venv "$pyver"
}

# --- node -------------------------------------------------------------------
npm_install() {
  # npm ci wipes node_modules, so only run it when the lockfile changed.
  if [ -f node_modules/.package-lock.json ] && [ node_modules/.package-lock.json -nt package-lock.json ]; then
    echo "    node_modules up to date"
    return 0
  fi
  npm ci --no-audit --no-fund --loglevel=error
}

setup_node() {
  local id="$1"
  want "$id" || return 0
  if ! have node || ! have npm; then skip "$id" "node/npm not found"; return 0; fi
  run_in "$id" npm_install
}

# --- rust -------------------------------------------------------------------
setup_rust() {
  local id="$1"
  want "$id" || return 0
  if ! have cargo; then skip "$id" "cargo not found (install via rustup)"; return 0; fi
  run_in "$id" env CARGO_TARGET_DIR="$HERE/$id/target" cargo build --release --locked --quiet
}

# --- go ---------------------------------------------------------------------
setup_go() {
  local id="$1"
  want "$id" || return 0
  if ! have go; then skip "$id" "go not found"; return 0; fi
  run_in "$id" go build -o bin/server .
}

SELECTED=("$@")

if want python-handrolled; then
  if have python3; then BUILT+=("python-handrolled (stdlib only, nothing to build)"); else skip python-handrolled "python3 not found"; fi
fi
setup_python python-fastmcp 3.10
setup_python python-fastmcp2 3.12
setup_python python-lowlevel 3.10
setup_python python-mcpserver 3.10

setup_node ts-sdk-tool
setup_node ts-sdk-register
setup_node ts-lowlevel
setup_node ts-sdk-v2

setup_rust rust-rmcp

setup_go go-sdk
setup_go go-mcpgo

echo
echo "built:   ${BUILT[*]:-none}"
if [ "${#SKIPPED[@]}" -gt 0 ]; then printf 'skipped: %s\n' "${SKIPPED[@]}"; fi
if [ "${#FAILED[@]}" -gt 0 ]; then echo "FAILED:  ${FAILED[*]}"; exit 1; fi
# CI sets MCPCHECK_E2E_STRICT=1 so a missing toolchain fails instead of
# quietly dropping servers from the e2e run.
if [ "${MCPCHECK_E2E_STRICT:-}" = 1 ] && [ "${#SKIPPED[@]}" -gt 0 ]; then
  echo "MCPCHECK_E2E_STRICT=1: skipped servers are an error"; exit 1
fi
exit 0
