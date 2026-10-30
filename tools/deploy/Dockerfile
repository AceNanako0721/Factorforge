# All stages use public inputs only; root .dockerignore is an allowlist.
FROM golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 AS go-build
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY src/factorforge/ src/factorforge/
COPY tools/deploy-schema/ tools/deploy-schema/
RUN set -eu; mkdir /out; for entry in src/factorforge/trading/entrypoints/*/main.go src/factorforge/strategy/entrypoints/*/main.go src/factorforge/applications/soxl_jev/entrypoints/*/main.go src/factorforge/applications/console/entrypoints/*/main.go src/factorforge/applications/deployment/entrypoints/*/main.go tools/deploy-schema/main.go; do dir=$(dirname "$entry"); CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "/out/$(basename "$dir")" "./$dir"; done
FROM node:22.23.1-bookworm-slim@sha256:6c74791e557ce11fc957704f6d4fe134a7bc8d6f5ca4403205b2966bd488f6b3 AS web-build
WORKDIR /app/src/factorforge/applications/console/web
COPY src/factorforge/applications/console/web/package.json src/factorforge/applications/console/web/pnpm-lock.yaml ./
RUN npm install -g pnpm@10.33.0 && pnpm install --frozen-lockfile --ignore-scripts
COPY src/factorforge/applications/console/web/ ./
COPY contracts/ /app/contracts/
COPY THIRD_PARTY_NOTICES.md /app/THIRD_PARTY_NOTICES.md
RUN pnpm build
FROM postgres:17@sha256:7958605b474b3d264a969cb3a123d6aa00ad1e1fe9da8a69984dabb704d93317 AS postgres
ARG FF_UID=1000
ARG FF_GID=1000
RUN getent group "$FF_GID" >/dev/null || groupadd -g "$FF_GID" ffapp; getent passwd "$FF_UID" >/dev/null || useradd -u "$FF_UID" -g "$FF_GID" -M ffapp
COPY --from=go-build /out/ff-runtime /usr/local/bin/ff-runtime
ENTRYPOINT ["/usr/local/bin/ff-runtime","run","--","/usr/local/bin/docker-entrypoint.sh"]
CMD ["postgres"]
FROM postgres AS core
STOPSIGNAL SIGTERM
# Public roots are required for direct venue/model HTTPS. The private gateway
# CA stays at /trust/ca.pem; SSL_CERT_DIR adds these system roots at runtime.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=go-build /out/ /usr/local/bin/
COPY --from=web-build /app/runtime/web-build/ /app/runtime/web-build/
COPY contracts/ /app/contracts/
WORKDIR /app
USER ${FF_UID}:${FF_GID}
ENTRYPOINT ["/usr/local/bin/ff-runtime","run","--"]
CMD ["trading-api","--help"]
