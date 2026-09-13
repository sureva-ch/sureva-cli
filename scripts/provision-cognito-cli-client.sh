#!/usr/bin/env bash
set -euo pipefail

# Idempotently provisions the public Cognito app client used by `sureva login`.
# This script deliberately emits only resource identifiers, never API responses.

# Pool id, client name and login domain come from infra/lib/config.sh, derived
# from $ENVIRONMENT. They were `readonly` literals pointing at the production
# pool, so this script could not be run against dev at all — not even with an
# override.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=SCRIPTDIR/../infra/lib/config.sh
source "${SCRIPT_DIR}/../infra/lib/config.sh"
# The only callback list in this script: create, update and validation all use
# it. It must hold exactly the redirect_uri the CLI builds for each port in
# authflow.DefaultPorts (internal/authflow/server.go). Cognito matches
# redirect_uri as an exact string, so `localhost` does not satisfy 127.0.0.1.
# internal/authflow/provision_script_test.go fails `go test` on drift.
readonly CALLBACKS=(
  "http://127.0.0.1:8976/callback"
  "http://127.0.0.1:8977/callback"
  "http://127.0.0.1:8978/callback"
)

command -v aws >/dev/null || { echo "aws CLI is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

callbacks_json="$(jq -nc '$ARGS.positional' --args "${CALLBACKS[@]}")"

account="$(aws sts get-caller-identity --query Account --output text)"
test "$account" = "$EXPECTED_ACCOUNT" || {
  echo "refusing to modify AWS account $account; expected $EXPECTED_ACCOUNT" >&2
  exit 1
}

pool_id="$(aws cognito-idp describe-user-pool \
  --region "$REGION" --user-pool-id "$USER_POOL_ID" \
  --query 'UserPool.Id' --output text)"
test "$pool_id" = "$USER_POOL_ID" || {
  echo "Cognito user pool validation failed" >&2
  exit 1
}

domain_description="$(aws cognito-idp describe-user-pool-domain \
  --region "$REGION" --domain "$LOGIN_DOMAIN" --output json)"
test "$(jq -r '.DomainDescription.UserPoolId // empty' <<<"$domain_description")" = "$USER_POOL_ID" || {
  echo "Managed Login domain is not attached to the expected user pool" >&2
  exit 1
}
test "$(jq -r '.DomainDescription.Status // empty' <<<"$domain_description")" = "ACTIVE" || {
  echo "Managed Login domain is not ACTIVE" >&2
  exit 1
}
managed_login_version="$(jq -r '.DomainDescription.ManagedLoginVersion // 1' <<<"$domain_description")"

client_id="$(aws cognito-idp list-user-pool-clients \
  --region "$REGION" --user-pool-id "$USER_POOL_ID" --max-results 60 \
  --query "UserPoolClients[?ClientName=='$CLIENT_NAME'].ClientId | [0]" --output text)"

# A secret-bearing app client can never be converted safely into the public
# PKCE client used by a locally installed CLI. Describe and reject it before
# creating temporary update input or invoking any mutating AWS operation.
client_description=""
if [[ -n "$client_id" && "$client_id" != "None" ]]; then
  client_description="$(aws cognito-idp describe-user-pool-client \
    --region "$REGION" --user-pool-id "$USER_POOL_ID" --client-id "$client_id" \
    --output json)"
  test "$(jq -r '.UserPoolClient | has("ClientSecret")' <<<"$client_description")" = "false" || {
    echo "refusing to update secret-bearing Cognito app client; create a new public client" >&2
    exit 1
  }
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

if [[ -z "$client_id" || "$client_id" = "None" ]]; then
  jq -n \
    --arg pool "$USER_POOL_ID" \
    --arg name "$CLIENT_NAME" \
    --argjson callbacks "$callbacks_json" \
    '{
      UserPoolId: $pool,
      ClientName: $name,
      GenerateSecret: false,
      AllowedOAuthFlowsUserPoolClient: true,
      AllowedOAuthFlows: ["code"],
      AllowedOAuthScopes: ["openid", "email", "profile"],
      CallbackURLs: $callbacks,
      SupportedIdentityProviders: ["COGNITO"],
      EnableTokenRevocation: true,
      PreventUserExistenceErrors: "ENABLED"
    }' >"$tmp"
  client_id="$(aws cognito-idp create-user-pool-client \
    --region "$REGION" --cli-input-json "file://$tmp" \
    --query 'UserPoolClient.ClientId' --output text)"
  action="created"
else
  # update-user-pool-client replaces the whole client: every omitted field is
  # reset to its default. Start from the complete describe-user-pool-client
  # snapshot taken above, remove response-only fields, then set every field
  # owned by this flow so unrelated validity/attribute settings survive.
  # CallbackURLs is replaced outright, so any callback outside CALLBACKS is
  # removed.
  jq --argjson callbacks "$callbacks_json" '
      .UserPoolClient
      | del(.ClientSecret, .LastModifiedDate, .CreationDate)
      | .AllowedOAuthFlowsUserPoolClient = true
      | .AllowedOAuthFlows = ["code"]
      | .AllowedOAuthScopes = ["openid", "email", "profile"]
      | .CallbackURLs = $callbacks
      | del(.LogoutURLs, .DefaultRedirectURI)
      | .SupportedIdentityProviders = ["COGNITO"]
      | .EnableTokenRevocation = true
      | .PreventUserExistenceErrors = "ENABLED"
    ' <<<"$client_description" >"$tmp"
  aws cognito-idp update-user-pool-client \
    --region "$REGION" --cli-input-json "file://$tmp" >/dev/null
  action="updated"
fi

test -n "$client_id" && test "$client_id" != "None" || {
  echo "Cognito app client provisioning returned no client ID" >&2
  exit 1
}

client="$(aws cognito-idp describe-user-pool-client \
  --region "$REGION" --user-pool-id "$USER_POOL_ID" --client-id "$client_id" \
  --output json)"
test "$(jq -r '.UserPoolClient | has("ClientSecret")' <<<"$client")" = "false" || {
  echo "refusing secret-bearing Cognito app client" >&2
  exit 1
}
test "$(jq -c '.UserPoolClient.CallbackURLs | sort' <<<"$client")" = \
  "$(jq -c 'sort' <<<"$callbacks_json")" || {
  echo "Cognito callback validation failed" >&2
  exit 1
}
test "$(jq -c '.UserPoolClient.AllowedOAuthFlows | sort' <<<"$client")" = '["code"]'
test "$(jq -c '.UserPoolClient.AllowedOAuthScopes | sort' <<<"$client")" = '["email","openid","profile"]'
test "$(jq -c '.UserPoolClient.SupportedIdentityProviders | sort' <<<"$client")" = '["COGNITO"]'
test "$(jq -r '.UserPoolClient.AllowedOAuthFlowsUserPoolClient' <<<"$client")" = "true"
test "$(jq -r '.UserPoolClient.EnableTokenRevocation' <<<"$client")" = "true"

# Under Managed Login version 2, an app client without a branding style gets
# "Login pages unavailable" instead of the sign-in page. /oauth2/authorize still
# answers 302 to /login, so no HTTP-level check reveals it. Version 1 (classic
# hosted UI) does not use branding styles.
#
# `describe-managed-login-branding-by-client` raises ResourceNotFoundException
# when the client has no style, so absence must be caught rather than tested
# for; any other failure aborts. An existing style may carry custom branding,
# so it is never modified.
branding_action="not required (Managed Login version $managed_login_version)"
if [[ "$managed_login_version" = "2" ]]; then
  if branding_error="$(aws cognito-idp describe-managed-login-branding-by-client \
    --region "$REGION" --user-pool-id "$USER_POOL_ID" --client-id "$client_id" \
    2>&1 >/dev/null)"; then
    branding_action="kept existing"
  elif [[ "$branding_error" == *ResourceNotFoundException* ]]; then
    aws cognito-idp create-managed-login-branding \
      --region "$REGION" --user-pool-id "$USER_POOL_ID" --client-id "$client_id" \
      --use-cognito-provided-values >/dev/null
    branding_action="created with Cognito-provided values"
  else
    echo "Managed Login branding lookup failed" >&2
    exit 1
  fi
fi

printf 'Cognito CLI client %s: %s\n' "$action" "$client_id"
printf 'Managed Login branding: %s\n' "$branding_action"
printf 'Set the GitHub Actions variable SUREVA_COGNITO_CLIENT_ID to this public client ID.\n'
