#!/usr/bin/env bash
set -euo pipefail

# Idempotently provisions the public Cognito app client used by `sureva login`.
# This script deliberately emits only resource identifiers, never API responses.

readonly EXPECTED_ACCOUNT="255398768146"
readonly REGION="eu-central-2"
readonly USER_POOL_ID="eu-central-2_NcwrZjuL3"
readonly CLIENT_NAME="sureva-cli"
readonly LOGIN_DOMAIN="auth.sureva.com"
readonly CALLBACKS=(
  "http://127.0.0.1:8976/callback"
  "http://127.0.0.1:8977/callback"
  "http://127.0.0.1:8978/callback"
)

command -v aws >/dev/null || { echo "aws CLI is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

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
    --argjson callbacks '[
      "http://127.0.0.1:8976/callback",
      "http://127.0.0.1:8977/callback",
      "http://127.0.0.1:8978/callback"
    ]' \
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
  # update-user-pool-client resets omitted values. Start from the complete
  # current client, remove response-only/immutable fields, then set every
  # field owned by this flow so unrelated validity/attribute settings survive.
  jq --argjson callbacks '[
        "http://127.0.0.1:8976/callback",
        "http://127.0.0.1:8977/callback",
        "http://127.0.0.1:8978/callback"
      ]' '
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
  "$(printf '%s\n' "${CALLBACKS[@]}" | jq -Rsc 'split("\n")[:-1] | sort')" || {
  echo "Cognito callback validation failed" >&2
  exit 1
}
test "$(jq -c '.UserPoolClient.AllowedOAuthFlows | sort' <<<"$client")" = '["code"]'
test "$(jq -c '.UserPoolClient.AllowedOAuthScopes | sort' <<<"$client")" = '["email","openid","profile"]'
test "$(jq -c '.UserPoolClient.SupportedIdentityProviders | sort' <<<"$client")" = '["COGNITO"]'
test "$(jq -r '.UserPoolClient.AllowedOAuthFlowsUserPoolClient' <<<"$client")" = "true"
test "$(jq -r '.UserPoolClient.EnableTokenRevocation' <<<"$client")" = "true"

branding_id="$(aws cognito-idp list-managed-login-branding-by-client \
  --region "$REGION" --user-pool-id "$USER_POOL_ID" --client-id "$client_id" \
  --query 'ManagedLoginBranding[0].ManagedLoginBrandingId' --output text)"
if [[ -z "$branding_id" || "$branding_id" = "None" ]]; then
  aws cognito-idp create-managed-login-branding \
    --region "$REGION" --user-pool-id "$USER_POOL_ID" --client-id "$client_id" \
    --use-cognito-provided-values >/dev/null
else
  aws cognito-idp update-managed-login-branding \
    --region "$REGION" --user-pool-id "$USER_POOL_ID" \
    --managed-login-branding-id "$branding_id" \
    --use-cognito-provided-values >/dev/null
fi

printf 'Cognito CLI client %s: %s\n' "$action" "$client_id"
printf 'Set the GitHub Actions variable SUREVA_COGNITO_CLIENT_ID to this public client ID.\n'
