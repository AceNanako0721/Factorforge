#!/bin/sh
# Isolated method trial, never starts the configured application stack.
set -eu
[ "${1:-}" = --run ] || { echo 'Explicit --run required' >&2; exit 2; }
root=$(git rev-parse --show-toplevel)
cd "$root"
lab="$root/runtime/docker-bundle-lab-$(date -u +%Y%m%dT%H%M%S)-$$"
mkdir -p "$lab/bin" "$lab/context/model"
chmod 700 "$lab"
printf '%s\n' "$lab" > runtime/docker-bundle-last.txt
docker ps -q --no-trunc | xargs docker inspect --format '{{.Id}} {{.State.StartedAt}}' > "$lab/before.txt"
for package in trading strategy applications/soxl_jev; do
  name=$(printf '%s' "$package" | tr / _)
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o "$lab/bin/$name.test" "./tests/$package"
  docker run --rm --network none --user postgres --read-only --cap-drop ALL --security-opt no-new-privileges --memory 384m --cpus 1 --pids-limit 128 --tmpfs /tmp:rw,exec,size=192m --mount "type=bind,src=$lab/bin,dst=/lab,readonly" --mount "type=bind,src=$root/tests,dst=/app/tests,readonly" --workdir "/app/tests/$package" --entrypoint "/lab/$name.test" postgres:17 -test.v -test.timeout=120s -test.run 'TestNativePostgresAtomicityAndIsolation|TestT224PostgresStopArchiveAtomicRestartAndPublicIsolation|TestCompositionPostgresFactorArchiveAtomicRestartAndImmutability|TestInstancePostgresReadIsolationRevocationCASAndRestart|TestP3DynamicStorePostgresAppendCASAndIsolation' > "$lab/$name.log" 2>&1
  cat "$lab/$name.log"
done
# Public source-only build context; no host node_modules, private assets or symlinks.
git ls-files src/factorforge/applications/model_access | while IFS= read -r file; do
  rel=${file#src/factorforge/applications/model_access/}
  mkdir -p "$lab/context/model/$(dirname "$rel")"
  cp "$file" "$lab/context/model/$rel"
done
cp tests/applications/model_access/omp.test.ts "$lab/context/"
cp tests/engineering/experiments/dockerbundle/model.Dockerfile "$lab/context/Dockerfile"
docker build -t factorforge-model-lab:20261030 "$lab/context" > "$lab/model-build.log" 2>&1
docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges --memory 512m --cpus 1 --pids-limit 128 --tmpfs /tmp:rw,exec,size=128m factorforge-model-lab:20261030 > "$lab/model-test.log" 2>&1
cat "$lab/model-test.log"
docker ps -q --no-trunc | xargs docker inspect --format '{{.Id}} {{.State.StartedAt}}' > "$lab/after.txt"
cmp "$lab/before.txt" "$lab/after.txt"
echo "Evidence: $lab"
