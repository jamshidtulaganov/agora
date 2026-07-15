#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Build a self-contained sandbox with stub `curl` and the release tarball that
# the official agora-cli GitHub Release path downloads.
_setup_sandbox() {
  local tmp="$1"
  local stub_bin="$tmp/stub-bin"
  local install_bin="$tmp/install-bin"
  local payload_dir="$tmp/payload"
  mkdir -p "$stub_bin" "$install_bin" "$payload_dir"

  cat >"$payload_dir/agora" <<'STUB'
#!/usr/bin/env bash
echo "agora v0.3.2 (commit: test)"
STUB
  chmod +x "$payload_dir/agora"
  tar -czf "$tmp/agora.tar.gz" -C "$payload_dir" agora
  if command -v shasum >/dev/null 2>&1; then
    checksum="$(shasum -a 256 "$tmp/agora.tar.gz" | awk '{print $1}')"
  else
    checksum="$(sha256sum "$tmp/agora.tar.gz" | awk '{print $1}')"
  fi

  # Keep the fixture portable when the host architecture differs from the
  # current macOS ARM development machine.
  case "$(uname -s)-$(uname -m)" in
    Darwin-x86_64) asset="agora-cli-0.3.2-darwin-amd64.tar.gz" ;;
    Darwin-arm64) asset="agora-cli-0.3.2-darwin-arm64.tar.gz" ;;
    Linux-x86_64) asset="agora-cli-0.3.2-linux-amd64.tar.gz" ;;
    Linux-aarch64|Linux-arm64) asset="agora-cli-0.3.2-linux-arm64.tar.gz" ;;
    *) echo "unsupported test platform" >&2; return 1 ;;
  esac
  printf '%s  %s\n' "$checksum" "$asset" >"$tmp/checksums.txt"

  cat >"$stub_bin/curl" <<'STUB'
#!/usr/bin/env bash
if [[ "$*" == *"-sI"* ]]; then
  printf 'HTTP/2 302\r\nlocation: https://github.com/jamshidtulaganov/agora-cli/releases/tag/v0.3.2\r\n'
  exit 0
fi

out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "$out" ]]; then
  echo "stub curl expected -o" >&2
  exit 2
fi
case "$out" in
  *checksums.txt) cp "$AGORA_TEST_CHECKSUMS" "$out" ;;
  *) cp "$AGORA_TEST_ARCHIVE" "$out" ;;
esac
STUB
  chmod +x "$stub_bin/curl"
}

_run_installer() {
  local tmp="$1"
  local out="$tmp/install.out"
  local err="$tmp/install.err"
  if ! PATH="$tmp/stub-bin:$tmp/install-bin:/usr/bin:/bin" \
    AGORA_BIN_DIR="$tmp/install-bin" \
    AGORA_TEST_ARCHIVE="$tmp/agora.tar.gz" \
    AGORA_TEST_CHECKSUMS="$tmp/checksums.txt" \
    bash "$ROOT_DIR/scripts/install.sh" >"$out" 2>"$err"; then
    echo "install.sh exited non-zero" >&2
    cat "$out" >&2 || true
    cat "$err" >&2 || true
    return 1
  fi

  if [[ ! -x "$tmp/install-bin/agora" ]]; then
    echo "expected fallback binary at $tmp/install-bin/agora" >&2
    cat "$out" >&2 || true
    cat "$err" >&2 || true
    return 1
  fi

  if ! grep -q "Installing Agora CLI from GitHub Releases" "$out"; then
    echo "expected GitHub Release install path" >&2
    cat "$err" >&2 || true
    return 1
  fi
}

test_installs_from_owned_release_even_when_brew_exists() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  cat >"$tmp/stub-bin/brew" <<'STUB'
#!/usr/bin/env bash
echo "brew must not be used by the Agora-owned release installer" >&2
exit 91
STUB
  chmod +x "$tmp/stub-bin/brew"

  _run_installer "$tmp"
}

test_upgrades_existing_cli_from_owned_release() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  cat >"$tmp/install-bin/agora" <<'STUB'
#!/usr/bin/env bash
echo "agora v0.3.1 (commit: previous)"
STUB
  chmod +x "$tmp/install-bin/agora"

  _run_installer "$tmp"

  if [[ "$("$tmp/install-bin/agora" version)" != *"v0.3.2"* ]]; then
    echo "expected existing CLI to be upgraded from the owned release" >&2
    return 1
  fi
}

test_installs_from_owned_release_even_when_brew_exists
test_upgrades_existing_cli_from_owned_release
echo "install.sh tests passed"
