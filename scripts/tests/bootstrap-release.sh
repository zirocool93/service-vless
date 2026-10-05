#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

PROJECT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
TMP_DIR=$(mktemp -d)
cleanup() { rm -rf -- "$TMP_DIR"; }
trap cleanup EXIT

BUNDLE="$TMP_DIR/bundle"
mkdir -p "$BUNDLE/scripts" "$BUNDLE/packaging/systemd" "$BUNDLE/docs" "$BUNDLE/third-party"
printf 'v0.1.0\n' > "$BUNDLE/VERSION"
for file in ubuntu-vpn-gateway xray LICENSE packaging/systemd/ubuntu-vpn-gateway.service docs/installation.md third-party/Xray-LICENSE third-party/Xray-SOURCE.md; do
  : > "$BUNDLE/$file"
done
cat > "$BUNDLE/scripts/install.sh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" > "${UVG_TEST_MARKER:?}"
EOF
chmod 0755 "$BUNDLE/scripts/install.sh"
for script in bootstrap.sh update.sh uninstall.sh; do : > "$BUNDLE/scripts/$script"; done

ASSET=ubuntu-vpn-gateway_v0.1.0_linux_amd64.tar.gz
tar -czf "$TMP_DIR/$ASSET" -C "$BUNDLE" .
sha256sum "$TMP_DIR/$ASSET" | awk -v asset="$ASSET" '{print $1 "  " asset}' > "$TMP_DIR/SHA256SUMS"
mkdir "$TMP_DIR/mock"
cat > "$TMP_DIR/mock/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
out=""
url=""
while (($#)); do
  case "$1" in
    --output) out=$2; shift 2 ;;
    -H|--header) shift 2 ;;
    --proto|--proto-redir|--tlsv1.2) if [[ $1 == --tlsv1.2 ]]; then shift; else shift 2; fi ;;
    --*) shift ;;
    *) url=$1; shift ;;
  esac
done
case "$url" in
  */SHA256SUMS) cp -- "$UVG_TEST_FIXTURES/SHA256SUMS" "$out" ;;
  *.tar.gz) cp -- "$UVG_TEST_FIXTURES/$(basename "$url")" "$out" ;;
  *) printf '{"tag_name":"v0.1.0"}\n' ;;
esac
EOF
chmod 0755 "$TMP_DIR/mock/curl"

MARKER="$TMP_DIR/installed"
if [[ $EUID -ne 0 ]] && ! sudo -n true 2>/dev/null; then
  echo "bootstrap release test: SKIP (нужен root или passwordless sudo)"
  exit 0
fi
if [[ $EUID -eq 0 ]]; then
  env PATH="$TMP_DIR/mock:/usr/bin:/bin" UVG_TEST_FIXTURES="$TMP_DIR" UVG_TEST_MARKER="$MARKER" \
    bash "$PROJECT_DIR/scripts/bootstrap.sh" --version v0.1.0 --repository zirocool93/service-vless
else
  sudo -n env PATH="$TMP_DIR/mock:/usr/bin:/bin" UVG_TEST_FIXTURES="$TMP_DIR" UVG_TEST_MARKER="$MARKER" \
    bash "$PROJECT_DIR/scripts/bootstrap.sh" --version v0.1.0 --repository zirocool93/service-vless
fi
grep -F -- '--version v0.1.0' "$MARKER" >/dev/null
echo "bootstrap release test: PASS"
