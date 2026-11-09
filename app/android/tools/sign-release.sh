#!/usr/bin/env bash
# 用发布密钥签名 Android ARM64 Release APK，验证通过后才替换发布产物。
set +x
set -euo pipefail

if (( $# > 1 )); then
    printf '用法：%s [输出目录]\n' "$0" >&2
    exit 2
fi

: "${ANDROID_KEYSTORE_BASE64:?请设置 ANDROID_KEYSTORE_BASE64}"
: "${ANDROID_KEYSTORE_PASSWORD:?请设置 ANDROID_KEYSTORE_PASSWORD}"
sdk=${ANDROID_HOME:-${ANDROID_SDK_ROOT:?请设置 ANDROID_HOME}}
apksigner=$(find "$sdk/build-tools" -name apksigner -type f | sort -V | tail -n 1)
if [[ ! -x "$apksigner" ]]; then
    printf 'Android SDK 中找不到 apksigner。\n' >&2
    exit 1
fi

android_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
release_dir="$android_dir/app/build/outputs/apk/release"
unsigned_apk="$release_dir/app-release-unsigned.apk"
if [[ ! -f "$unsigned_apk" ]]; then
    printf '找不到未签名 APK %s，请先构建 Android Release。\n' "$unsigned_apk" >&2
    exit 1
fi

output_dir=${1:-$release_dir}
mkdir -p -- "$output_dir"
output_apk="$output_dir/LumenFolio-Android-arm64.apk"
keystore=$(mktemp)
temporary_apk=$(mktemp "$output_dir/.LumenFolio-Android-arm64.XXXXXX.apk")
trap 'rm -f -- "$keystore" "$temporary_apk"' EXIT

printf '%s' "$ANDROID_KEYSTORE_BASE64" | base64 --decode > "$keystore"
unset ANDROID_KEYSTORE_BASE64
"$apksigner" sign --ks "$keystore" --ks-type PKCS12 --ks-key-alias lumenfolio \
    --ks-pass env:ANDROID_KEYSTORE_PASSWORD --key-pass env:ANDROID_KEYSTORE_PASSWORD \
    --v4-signing-enabled false --out "$temporary_apk" "$unsigned_apk"
"$apksigner" verify "$temporary_apk"
mv -f -- "$temporary_apk" "$output_apk"
printf '%s\n' "$output_apk"
