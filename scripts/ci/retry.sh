#!/usr/bin/env bash

retry() {
  local attempts="$1"
  local attempt=1
  local status=0
  local delay
  shift

  while true; do
    echo "tentativo ${attempt}/${attempts}: $*" >&2
    if "$@"; then
      return 0
    else
      status=$?
    fi

    if (( attempt >= attempts )); then
      return "${status}"
    fi

    delay=$((10 * (2 ** (attempt - 1))))
    sleep "${delay}"
    ((attempt++))
  done
}
