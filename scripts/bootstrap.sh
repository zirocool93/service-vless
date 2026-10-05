#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

REPOSITORY=${UVG_REPOSITORY:-zirocool93/service-vless}
VERSION=""

usage() {
  echo "Использование: bootstrap.sh [--version vX.Y.Z] [--repository OWNER/REPO]"
}
while (($#)); do
  case "$1" in
    --version) VERSION=${2:?}; shift 2 ;;
    --repository) REPOSITORY=${2:?}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Неизвестный параметр: $1" >&2; exit 2 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "Запустите команду через sudo или из root shell." >&2; exit 1; }
[[ $REPOSITORY =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Некорректное имя репозитория." >&2; exit 1; }
for command_name in curl tar sha256sum awk; do
  command -v "$command_name" >/dev/null || { echo "Для bootstrap требуется $command_name." >&2; exit 1; }
done

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Архитектура $(uname -m) не поддерживается." >&2; exit 1 ;;
esac

API="https://api.github.com/repos/$REPOSITORY/releases/latest"
if [[ -z $VERSION ]]; then
  VERSION=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
    -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "$API" |
    awk -F '"' '/"tag_name"[[:space:]]*:/ && tag == "" { tag=$4 } END { print tag }')
fi
[[ $VERSION =~ ^v[0-9][0-9A-Za-z._-]*$ ]] || { echo "GitHub вернул некорректный release tag." >&2; exit 1; }

ASSET="ubuntu-vpn-gateway_${VERSION}_linux_${ARCH}.tar.gz"
BASE="https://github.com/$REPOSITORY/releases/download/$VERSION"
TMP_DIR=$(mktemp -d -t ubuntu-vpn-gateway-bootstrap.XXXXXXXX)
cleanup() { rm -rf -- "$TMP_DIR"; }
trap cleanup EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --output "$TMP_DIR/$ASSET" "$BASE/$ASSET"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --output "$TMP_DIR/SHA256SUMS" "$BASE/SHA256SUMS"

EXPECTED=$(awk -v asset="$ASSET" '$2 == asset || $2 == "*" asset { print $1 }' "$TMP_DIR/SHA256SUMS")
[[ $EXPECTED =~ ^[[:xdigit:]]{64}$ ]] || { echo "SHA256SUMS не содержит единственную корректную сумму для $ASSET." >&2; exit 1; }
[[ $(awk -v asset="$ASSET" '$2 == asset || $2 == "*" asset { n++ } END { print n+0 }' "$TMP_DIR/SHA256SUMS") -eq 1 ]] || { echo "SHA256SUMS содержит повторяющуюся запись asset." >&2; exit 1; }
ACTUAL=$(sha256sum "$TMP_DIR/$ASSET" | awk '{print $1}')
[[ ${ACTUAL,,} == ${EXPECTED,,} ]] || { echo "Контрольная сумма release-архива не совпала." >&2; exit 1; }

while IFS= read -r entry; do
  clean=${entry#./}
  [[ $entry == "." || $entry == "./" ]] && continue
  [[ -n $clean && $clean != /* && $clean != ".." && $clean != ../* && $clean != */../* && $clean != */.. && $clean != *\\* ]] || {
    echo "Release-архив содержит небезопасный путь." >&2; exit 1;
  }
done < <(tar -tzf "$TMP_DIR/$ASSET")
if tar -tvzf "$TMP_DIR/$ASSET" | awk 'substr($1,1,1) != "-" && substr($1,1,1) != "d" { bad=1 } END { exit bad ? 0 : 1 }'; then
  echo "Release-архив содержит ссылки или специальные файлы." >&2
  exit 1
fi

install -d -m 0700 "$TMP_DIR/bundle"
tar -xzf "$TMP_DIR/$ASSET" --no-same-owner --no-same-permissions -C "$TMP_DIR/bundle"
ROOT="$TMP_DIR/bundle"
[[ -f $ROOT/VERSION ]] || {
  dirs=("$ROOT"/*)
  [[ ${#dirs[@]} -eq 1 && -d ${dirs[0]} ]] || { echo "Не найден корень release bundle." >&2; exit 1; }
  ROOT=${dirs[0]}
}
bash "$ROOT/scripts/install.sh" --bundle-dir "$ROOT" --version "$VERSION" --repository "$REPOSITORY"
