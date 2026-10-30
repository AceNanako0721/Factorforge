#!/bin/sh
set -eu
[ "${1:-}" = --run ] || exit 2
root=$(git rev-parse --show-toplevel)
cd "$root"
lab="$root/runtime/docker-persistence-lab-$(date -u +%Y%m%dT%H%M%S)-$$"
mkdir -p "$lab"
chmod 700 "$lab"
name="ff-persistence-lab-$$"
vol="$name-data"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm "$vol" >/dev/null 2>&1 || true; }
trap cleanup EXIT HUP INT TERM
docker volume create --label factorforge.experiment="$name" "$vol" > "$lab/volume.txt"
start() {
 docker run -d --name "$name" --network none --memory 256m --cpus 1 --pids-limit 128 -e POSTGRES_HOST_AUTH_METHOD=trust --mount "type=volume,src=$vol,dst=/var/lib/postgresql/data" postgres:17 > "$lab/container.txt"
 i=0
 until docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; do i=$((i+1)); [ "$i" -lt 60 ]; sleep 1; done
}
start
docker exec "$name" psql -U postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE ff_lab_reader NOLOGIN; CREATE TABLE durable_probe(id integer PRIMARY KEY, body text); INSERT INTO durable_probe VALUES(1,'synthetic-before-rebuild'); GRANT SELECT ON durable_probe TO ff_lab_reader;" > "$lab/seed.log"
docker exec "$name" pg_dump -U postgres -Fc postgres > "$lab/database.dump"
docker exec "$name" pg_dumpall -U postgres --globals-only --no-role-passwords > "$lab/roles.sql"
docker stop -t 15 "$name" > "$lab/stop.log"
docker rm "$name" > "$lab/remove.log"
start
[ "$(docker exec "$name" psql -U postgres -Atc 'SELECT body FROM durable_probe')" = synthetic-before-rebuild ]
docker exec "$name" createdb -U postgres ff_restore
cat "$lab/database.dump" | docker exec -i "$name" pg_restore -U postgres --exit-on-error -d ff_restore
[ "$(docker exec "$name" psql -U postgres -d ff_restore -Atc 'SELECT body FROM durable_probe')" = synthetic-before-rebuild ]
[ "$(docker exec "$name" psql -U postgres -d ff_restore -Atc "SELECT has_table_privilege('ff_lab_reader','durable_probe','SELECT')")" = t ]
printf '%s\n' 'PASS same-volume container replacement' 'PASS pg_dump/pg_restore data and grants' > "$lab/result.txt"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$lab/probe" ./tests/trading/processprobe
printf '%s\n' '{"DeniedFile":"/etc/passwd","DeniedProc":"/proc/self/cmdline","DSN":"postgresql://nobody@127.0.0.1:1/postgres?sslmode=disable"}' > "$lab/probe.json"
docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 1000:1000 --mount "type=bind,src=$lab,dst=/lab,readonly" --entrypoint /lab/probe postgres:17 --mode filesystem --profile /lab/probe.json > "$lab/landlock.json"
cat "$lab/result.txt" "$lab/landlock.json"
echo "Evidence: $lab"
