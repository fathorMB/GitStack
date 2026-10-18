# Immagine della prova di fattibilita' (ADR 0001): act in modalita' host dentro
# un pod senza privilegi. Nessun Docker dentro l'immagine.
FROM node@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392
ARG ACT_VERSION=v0.2.89
ARG ACT_SHA256=0191d6f1f3b716b5c55820032605d05fc3c1cdbf581ebeff655019e5dd1524c0
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL -o /tmp/act.tgz "https://github.com/nektos/act/releases/download/${ACT_VERSION}/act_Linux_x86_64.tar.gz" \
    && echo "${ACT_SHA256}  /tmp/act.tgz" | sha256sum -c - \
    && tar -xzf /tmp/act.tgz -C /usr/local/bin act \
    && rm /tmp/act.tgz
USER 10001:10001
WORKDIR /work
