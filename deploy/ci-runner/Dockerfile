# syntax=docker/dockerfile:1
#
# Immagine del runner del job CI (GIT-316, ADR 0001 §1 e §4): act in modalita' host
# (nessun Docker) piu' ci-runner, che parla runner.v1 col ci-gateway di core ed
# esegue un solo job (act -j). Un pod = un job.
#
# Contesto di build: la RADICE del monorepo (come le immagini dei servizi).
#
#   docker build -f deploy/ci-runner/Dockerfile .
#
# La costruzione e la prova (act vero + gateway finto) le fa il job `ci-runner-image` di ci.yml.

# --- Stage 1: ci-runner (Go) --------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY pkg ./pkg
COPY scripts/docker-local-replace.sh ./scripts/docker-local-replace.sh

WORKDIR /src/services/core
COPY services/core/go.mod services/core/go.sum ./
RUN sh /src/scripts/docker-local-replace.sh /src \
    && for i in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download: tentativo $i fallito" >&2; sleep $((i*5)); done; exit 1

COPY services/core/ .
RUN sh /src/scripts/docker-local-replace.sh /src \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ci-runner ./cmd/ci-runner \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitstack-ci-gate ./cmd/ci-gate

# --- Stage 2: runtime ----------------------------------------------------
# Stessa base della prova di fattibilita' (docs/adr/poc-ci-executor/Dockerfile):
# node fissato per digest, perche' le action JavaScript lo richiedono.
FROM node@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392

# act: versione e SHA256 fissati (nektos/act, MIT). Per cambiarli si aggiornano
# insieme, con lo SHA256 del tarball del rilascio.
ARG ACT_VERSION=v0.2.89
ARG ACT_SHA256=0191d6f1f3b716b5c55820032605d05fc3c1cdbf581ebeff655019e5dd1524c0

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git gcc libc6-dev make jq xz-utils unzip \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL -o /tmp/act.tgz "https://github.com/nektos/act/releases/download/${ACT_VERSION}/act_Linux_x86_64.tar.gz" \
    && echo "${ACT_SHA256}  /tmp/act.tgz" | sha256sum -c - \
    && tar -xzf /tmp/act.tgz -C /usr/local/bin act \
    && rm /tmp/act.tgz \
    && install -d -o 10001 -g 10001 /work

COPY --from=build /out/ci-runner /usr/local/bin/ci-runner
COPY --from=build /out/gitstack-ci-gate /usr/local/bin/gitstack-ci-gate

# buildctl (BuildKit, Apache-2.0, GIT-371): solo il client, per i job `runs-on: gitstack-build`.
# Il demone rootless e' il sidecar del pod di build (ci.build.image), non sta in quest'immagine;
# il digest e' lo stesso di ci.build.image in values.yaml (check-internal-versions non lo guarda:
# si aggiornano insieme). Nei pod normali buildctl non ha nessun demone con cui parlare.
COPY --from=docker.io/moby/buildkit:v0.23.2-rootless@sha256:cab936745de5d673465948f1e93ff4d6e372bbe33f218afd3314eba45a6f85a9 /usr/bin/buildctl /usr/local/bin/buildctl
COPY deploy/ci-runner/gitstack-build /usr/local/bin/gitstack-build

ENV GITSTACK_CI_ACT=/usr/local/bin/act \
    GITSTACK_CI_WORKDIR=/work \
    HOME=/work

# Utente non privilegiato, come impone il Pod Security `restricted` del namespace dei job.
USER 10001:10001
WORKDIR /work

# Servono GITSTACK_CI_GATEWAY_URL e GITSTACK_CI_RUNNER_SECRET (vedi cmd/ci-runner).
ENTRYPOINT ["/usr/local/bin/ci-runner"]
