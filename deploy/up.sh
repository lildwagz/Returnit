#!/usr/bin/env bash
# docker compose for the production stack. Examples:
#   ./up.sh up -d --build      start / update everything
#   ./up.sh ps                 what is running and healthy
#   ./up.sh logs -f worker     follow one service's logs
#   ./up.sh down               stop (data is kept in volumes)
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
dc "$@"
