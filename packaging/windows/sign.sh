#!/bin/bash
# Signs a Windows file (.exe or .msi) in place: packaging/dist.sh runs it on
# each program and .msi, and the NSIS script on its uninstaller and on the
# setup itself. Without signing set up it does nothing, so unsigned builds
# keep working. Set up one of:
#
#   A certificate file (.pfx/.p12, with its key) and osslsigncode:
#     SIGN_PFX=/path/to/cert.pfx  SIGN_PASSWORD=...
#
#   jsign (https://ebourg.github.io/jsign/), for a key in a cloud service or
#   on a token, which certificates issued since 2023 must be; for example
#   Azure Trusted Signing:
#     SIGN_JSIGN="java -jar /path/to/jsign.jar"
#     SIGN_JSIGN_ARGS="--storetype TRUSTEDSIGNING --keystore weu.codesigning.azure.net
#                      --storepass <access token> --alias <account>/<profile>"
#
#   SIGN_TIMESTAMP is the timestamp server (default DigiCert's); "none"
#   leaves the timestamp out (only for testing: the signature then expires
#   with the certificate).
#
#   sign.sh <file>
set -euo pipefail

if (( $# != 1 )); then
  echo "usage: sign.sh <file.exe|file.msi>" >&2
  exit 1
fi
file=$1
name="TrunkLine"
url="https://github.com/FrancoCoetzer/TrunkLine"
tsa=${SIGN_TIMESTAMP:-http://timestamp.digicert.com}

if [[ -n ${SIGN_PFX:-} ]]; then
  ts=()
  if [[ $tsa != "none" ]]; then
    ts=(-ts "$tsa")
  fi
  osslsigncode sign -pkcs12 "$SIGN_PFX" -pass "${SIGN_PASSWORD:-}" -h sha256 -n "$name" -i "$url" "${ts[@]}" \
    -in "$file" -out "$file.signed" >/dev/null
  mv -f "$file.signed" "$file"
  echo "signed $file"
elif [[ -n ${SIGN_JSIGN:-} ]]; then
  ts=()
  if [[ $tsa != "none" ]]; then
    ts=(--tsaurl "$tsa")
  fi
  # SIGN_JSIGN and SIGN_JSIGN_ARGS are words on purpose: a command and its options.
  # shellcheck disable=SC2086
  $SIGN_JSIGN $SIGN_JSIGN_ARGS --alg SHA-256 --name "$name" --url "$url" "${ts[@]}" "$file"
  echo "signed $file"
fi
