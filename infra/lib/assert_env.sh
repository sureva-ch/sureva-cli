#!/usr/bin/env bash
# infra/lib/assert_env.sh — shared dev/prod safety assertion.
#
# Change: dev-environment-guardrails (R2, criterion 6). Canonical path per the
# design (Engram sdd/dev-environment-guardrails/design, D5) — the copy-source
# for later per-repo propagation (out of scope for this change; see
# dev-environment-plan.md §0.3, which documents copy-paste as the established
# convention across repos, since no `.gitmodules` or shared repo exists).
#
# Sourced by every provisioning and deploy script; call assert_dev_safety()
# after loading infra/lib/config.sh (or the repo's equivalent) so
# ENVIRONMENT/NAME_BASE are already exported.
#
# assert_dev_safety() returns non-zero (2) on:
#   - ENVIRONMENT unset
#   - ENVIRONMENT set to a value other than dev/prod/production
#   - ENVIRONMENT=dev but NAME_BASE lacks the "-dev" suffix (plan §2.3)
#   - ENVIRONMENT=dev but the caller identity is a production CI role
#     (github-ci-sureva-cloud, github-ci-sureva-cloud-builder, or any ARN
#     ending "-github-actions-prod")
#   - ENVIRONMENT=prod|production but the caller identity is a dev-scoped
#     identity (github-ci-sureva-dev, or any ARN containing "sureva-dev")
#
# "production" is accepted as a synonym for "prod": real live tag values are
# fragmented across repos into both spellings (Engram id 1825), and this
# helper must fail closed on real-world drift, not just the plan's own
# canonical two-value convention.
#
# CALLER_ARN is injectable for testing; defaults to the live caller identity
# via `aws sts get-caller-identity`.
set -uo pipefail

assert_dev_safety() {
  if [[ -z "${ENVIRONMENT:-}" ]]; then
    echo "assert_dev_safety: refusing — ENVIRONMENT unset" >&2
    return 2
  fi

  case "$ENVIRONMENT" in
    dev|prod|production) ;;
    *)
      echo "assert_dev_safety: refusing — unknown ENVIRONMENT '${ENVIRONMENT}' (must be 'dev', 'prod', or 'production')" >&2
      return 2
      ;;
  esac

  local caller_arn="${CALLER_ARN:-}"
  if [[ -z "$caller_arn" ]]; then
    caller_arn="$(aws sts get-caller-identity --query Arn --output text 2>/dev/null || true)"
  fi

  if [[ "$ENVIRONMENT" == "dev" ]]; then
    case "${NAME_BASE:-}" in
      *-dev) ;;
      *)
        echo "assert_dev_safety: refusing — ENVIRONMENT=dev but NAME_BASE='${NAME_BASE:-}' lacks the '-dev' suffix" >&2
        return 2
        ;;
    esac

    case "$caller_arn" in
      *github-ci-sureva-cloud*|*github-ci-sureva-cloud-builder*|*-github-actions-prod*)
        echo "assert_dev_safety: refusing — ENVIRONMENT=dev but caller '${caller_arn}' is a production CI role" >&2
        return 2
        ;;
    esac
  fi

  if [[ "$ENVIRONMENT" == "prod" || "$ENVIRONMENT" == "production" ]]; then
    case "$caller_arn" in
      *sureva-dev*)
        echo "assert_dev_safety: refusing — ENVIRONMENT=${ENVIRONMENT} but caller '${caller_arn}' is a dev-scoped identity" >&2
        return 2
        ;;
    esac
  fi

  return 0
}

# Only run when executed directly, mirroring scripts/audit-inventory.sh's
# guard: sourcing this file (the normal usage from another provisioning
# script) defines the function only and calls nothing.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  assert_dev_safety
  exit $?
fi
