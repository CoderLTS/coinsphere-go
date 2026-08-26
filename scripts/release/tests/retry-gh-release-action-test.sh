#!/usr/bin/env bash

set -Eeuo pipefail

action=.github/actions/retry-gh-release/action.yml
workflow=.github/workflows/release.yml
release_action='uses: softprops/action-gh-release@3d0d9888cb7fd7b750713d6e236d1fcb99157228'

[[ $(grep -Fc "$release_action" "$action") -eq 3 ]]
[[ $(grep -Fc 'continue-on-error: true' "$action") -eq 2 ]]
[[ $(grep -Fc 'NODE_USE_ENV_PROXY: "1"' "$action") -eq 3 ]]
[[ $(grep -Ec 'run: sleep (10|20)$' "$action") -eq 2 ]]
grep -Fq "if: steps.release_attempt_1.outcome == 'failure'" "$action"
grep -Fq "if: steps.release_attempt_1.outcome == 'failure' && steps.release_attempt_2.outcome == 'failure'" "$action"
[[ $(grep -Fc 'uses: ./.github/actions/retry-gh-release' "$workflow") -eq 2 ]]
! grep -Fq "$release_action" "$workflow"
