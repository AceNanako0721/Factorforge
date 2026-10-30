#!/bin/sh
# Isolated DeepSeek JSON-output trial. Never starts the application stack.
set -eu
[ "${1:-}" = --run ] || { echo 'Explicit --run required' >&2; exit 2; }
root=$(git rev-parse --show-toplevel)
lab="$root/runtime/deepseek-json-lab-$(date -u +%Y%m%dT%H%M%S)-$$"
mkdir -p "$lab"
chmod 700 "$lab"
key="$root/runtime/deepseek-json-lab/key"
compose="docker compose -p ff-deepseek-json-lab-$$ -f $root/tests/engineering/experiments/deepseekjson/compose.yaml"
export FF_DEEPSEEK_EVIDENCE="$lab"
export FF_DEEPSEEK_ROOT="$root"
export FF_DEEPSEEK_PKG="$root/tests/engineering/experiments/deepseekjson"
export FF_DEEPSEEK_LANG="${FF_DEEPSEEK_LANG:-en}"
if [ "${2:-}" = --dry ]; then
  FF_DEEPSEEK_KEY=/dev/null $compose run --rm --no-deps probe go run ./tests/engineering/experiments/deepseekjson --key-file /private/key --dry --lang "$FF_DEEPSEEK_LANG"
  exit 0
fi
[ -f "$key" ] || { echo "missing $key (single line, chmod 600)" >&2; exit 3; }
FF_DEEPSEEK_KEY="$key" $compose up --abort-on-container-exit --exit-code-from probe
$compose down --remove-orphans
echo "Evidence: $lab"
