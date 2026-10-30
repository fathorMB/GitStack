# syntax=docker/dockerfile:1
#
# Il sito della documentazione di GitStack come app interna (GIT-355, dogfooding della v3-a).
# Contesto di build: la RADICE del monorepo, perche' il sito si genera da cli/ (reference dei
# comandi), api/openapi.yaml (reference API), docs/ e SECURITY.md (vedi docs-site/package.json):
#
#   gitstack-build --name docs --tag <tag> --file docs-site/Dockerfile .
#
# Lo costruisce il workflow deploy/dogfooding/docs-app.yml su un runner `runs-on: gitstack-build`;
# l'app `docs` del repo lo segue dal branch (porta 8080, health check HTTP su /).
# Non verificato con un docker build in locale: la prova e' il giro su homehub (docs/operations.md).

# --- Stage 1: build del sito (Node per Docusaurus, Go per la reference dei comandi) ---------------
FROM docker.io/library/node:22-bookworm AS build
COPY --from=docker.io/library/golang:1.26-bookworm /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"
RUN corepack enable
WORKDIR /src
COPY . .
WORKDIR /src/docs-site
RUN pnpm install --frozen-lockfile && pnpm run build

# --- Stage 2: file statici con busybox httpd, senza root -------------------------------------------
FROM docker.io/library/busybox:1.36
ARG COMMIT=""
LABEL org.opencontainers.image.title="gitstack-docs" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /src/docs-site/build /www
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/bin/busybox", "httpd", "-f", "-p", "8080", "-h", "/www"]
