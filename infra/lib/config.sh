#!/usr/bin/env bash
# infra/lib/config.sh — canonical configuration surface for sureva-cli.
#
# Sourced, side-effect-free. Every value derives from $ENVIRONMENT, with NO
# default: a script invoked without it must fail rather than silently target
# production.
#
# Usage:
#   ENVIRONMENT=dev source infra/lib/config.sh
#
# This repo provisions no infrastructure of its own — it has exactly one script,
# which registers a Cognito app client. Its pool id and login domain were
# `readonly` literals pointing at production, so the script could not be run
# against the dev pool at all, not even with an override.

set -euo pipefail

export PROJECT="sureva"
export APP="sureva-cli"
export ENVIRONMENT="${ENVIRONMENT:?set ENVIRONMENT (dev|prod)}"

export REGION="${AWS_REGION:-eu-central-2}"
export EXPECTED_ACCOUNT="${EXPECTED_ACCOUNT:-255398768146}"

case "$ENVIRONMENT" in
  prod)
    ENV_TAG="production"
    NAME_SUFFIX=""
    USER_POOL_ID="eu-central-2_NcwrZjuL3"
    LOGIN_DOMAIN="auth.sureva.com"
    API_URL="https://api.sureva.com"
    ;;
  dev)
    ENV_TAG="development"
    NAME_SUFFIX="-dev"
    USER_POOL_ID="eu-central-2_UR0k0FVwr"
    LOGIN_DOMAIN="auth.dev.sureva.com"
    API_URL="https://api.dev.sureva.com"
    ;;
  *)
    echo "unknown ENVIRONMENT: $ENVIRONMENT" >&2
    exit 2
    ;;
esac

export ENV_TAG NAME_SUFFIX USER_POOL_ID LOGIN_DOMAIN API_URL
export CLIENT_NAME="${PROJECT}-cli${NAME_SUFFIX}"

# shellcheck source=infra/lib/assert_env.sh
source "$(dirname "${BASH_SOURCE[0]}")/assert_env.sh"
