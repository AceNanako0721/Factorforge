FROM oven/bun:1.3.14@sha256:e10577f0db68676a7024391c6e5cb4b879ebd17188ab750cf10024a6d700e5c4 AS bun
FROM node:22.23.1-bookworm-slim@sha256:6c74791e557ce11fc957704f6d4fe134a7bc8d6f5ca4403205b2966bd488f6b3
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
WORKDIR /app/src/factorforge/applications/model_access
COPY model/package*.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY model/ ./
RUN npm run build
COPY omp.test.ts /app/tests/applications/model_access/omp.test.ts
WORKDIR /app
USER 1000:1000
CMD ["bun","test","tests/applications/model_access/omp.test.ts"]
