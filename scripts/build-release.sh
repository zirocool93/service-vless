#!/usr/bin/env bash
set -Eeuo pipefail
# Сборка выполняется в Linux/CI; сервер получает готовые бинарники.
VERSION=${1:?Укажите версию вида v0.1.0}
[[ $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Некорректная версия релиза' >&2; exit 2; }
PROJECT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$PROJECT_DIR"
OUTPUT_DIR="$PROJECT_DIR/bin/releases/$VERSION"
rm -rf -- "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"
TMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TMP_DIR"' EXIT
XRAY_VERSION=v26.3.27
for ARCH in amd64 arm64; do
  case "$ARCH" in
    amd64) ASSET=Xray-linux-64.zip; DIGEST=23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae ;;
    arm64) ASSET=Xray-linux-arm64-v8a.zip; DIGEST=4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c ;;
  esac
  BUNDLE="$TMP_DIR/$ARCH"
  mkdir -p "$BUNDLE/scripts" "$BUNDLE/packaging/systemd" "$BUNDLE/docs" "$BUNDLE/third-party" "$TMP_DIR/xray-$ARCH"
  curl --fail --location --retry 3 --proto '=https' --proto-redir '=https' --tlsv1.2 \
    "https://github.com/XTLS/Xray-core/releases/download/$XRAY_VERSION/$ASSET" -o "$TMP_DIR/$ASSET"
  printf '%s  %s\n' "$DIGEST" "$TMP_DIR/$ASSET" | sha256sum --check --status
  unzip -q "$TMP_DIR/$ASSET" xray LICENSE -d "$TMP_DIR/xray-$ARCH"
  cp "$TMP_DIR/xray-$ARCH/xray" "$BUNDLE/xray"
  cp "$TMP_DIR/xray-$ARCH/LICENSE" "$BUNDLE/third-party/Xray-LICENSE"
  printf 'Xray %s, MPL-2.0. Исходники: https://github.com/XTLS/Xray-core/tree/%s\nАрхив исходников: https://github.com/XTLS/Xray-core/archive/refs/tags/%s.tar.gz\nБинарник получен из официального релиза без изменений.\n' \
    "$XRAY_VERSION" "$XRAY_VERSION" "$XRAY_VERSION" > "$BUNDLE/third-party/Xray-SOURCE.md"
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$BUNDLE/ubuntu-vpn-gateway" ./cmd/gateway
  cp scripts/install.sh scripts/bootstrap.sh scripts/update.sh scripts/uninstall.sh "$BUNDLE/scripts/"
  cp packaging/systemd/ubuntu-vpn-gateway.service "$BUNDLE/packaging/systemd/"
  cp docs/installation.md "$BUNDLE/docs/"
  cp LICENSE "$BUNDLE/"
  printf '%s\n' "$VERSION" > "$BUNDLE/VERSION"
  chmod 0755 "$BUNDLE/ubuntu-vpn-gateway" "$BUNDLE/xray" "$BUNDLE/scripts/"*.sh
  tar -C "$BUNDLE" -czf "$OUTPUT_DIR/ubuntu-vpn-gateway_${VERSION}_linux_${ARCH}.tar.gz" .
done
(cd "$OUTPUT_DIR" && sha256sum ubuntu-vpn-gateway_*.tar.gz > SHA256SUMS)
test "$(find "$OUTPUT_DIR" -maxdepth 1 -type f | wc -l)" -eq 3
echo "Релиз собран: $OUTPUT_DIR"
