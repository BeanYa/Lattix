# syntax=docker/dockerfile:1.7

FROM oven/bun:1-alpine AS frontend
WORKDIR /src/frontend
COPY src/frontend/package.json src/frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY src/frontend/ ./
RUN bun run build

FROM golang:1.26-alpine AS backend
ARG VERSION=dev
ARG GITHUB_REPO=BeanYa/Lattix
WORKDIR /src
COPY go.work go.work.sum ./
COPY src/agent/go.mod src/agent/go.mod
COPY src/shared/ src/shared/
COPY src/backend/ src/backend/
COPY --from=frontend /src/frontend/dist/ src/backend/internal/web/dist/
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.githubRepo=${GITHUB_REPO}" \
    -o /out/lattix-backend ./src/backend/cmd/backend

FROM alpine:3.22
ARG VERSION=dev
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 lattix \
    && adduser -D -H -u 10001 -G lattix lattix \
    && install -d -o lattix -g lattix /app /data
WORKDIR /app
COPY --from=backend --chown=lattix:lattix /out/lattix-backend /app/lattix-backend
USER lattix:lattix
ENV LATTIX_DEPLOY_MODE=docker \
    LATTIX_DB=/data/lattix.db \
    LATTIX_TLS_DIR=/data/certs \
    LATTIX_ACME_CACHE=/data/acme-cache
EXPOSE 8080
LABEL org.opencontainers.image.source="https://github.com/BeanYa/Lattix" \
      org.opencontainers.image.description="Lattix panel" \
      org.opencontainers.image.version="${VERSION}"
ENTRYPOINT ["/app/lattix-backend"]
CMD ["-addr", ":8080"]
