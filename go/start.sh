#!/usr/bin/env bash
# Start Fribok, wait until it responds, and open it in the default browser.
#
#   ./start.sh
#
# Environment:
#   FRIBOK_PORT=8080        port on 127.0.0.1
#   FRIBOK_HOST=127.0.0.1   listen address
#   FRIBOK_DB=...           SQLite database file (default: ./fribok.db)
#   FRIBOK_LOG=...          server log file (default: ./fribok.log)
#   FRIBOK_NO_BROWSER=1     do not open a browser
#   FRIBOK_BROWSER=...      browser command to run with the URL as argument
set -u

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN="$DIR/fribok"
HOST="${FRIBOK_HOST:-127.0.0.1}"
PORT="${FRIBOK_PORT:-8080}"
ADDR="$HOST:$PORT"
DB="${FRIBOK_DB:-$DIR/fribok.db}"
LOG="${FRIBOK_LOG:-$DIR/fribok.log}"

case "$HOST" in
  0.0.0.0|::|"[::]") URL_HOST=127.0.0.1 ;;
  *) URL_HOST="$HOST" ;;
esac
URL="http://$URL_HOST:$PORT"
SERVER_PID=""

cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM HUP

if [ ! -x "$BIN" ]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "Hittar inte $BIN och Go är inte installerat." >&2
    exit 1
  fi
  echo "Bygger fribok ..."
  if ! (cd "$DIR" && go build -o fribok ./cmd/fribok); then
    echo "Bygget misslyckades." >&2
    exit 1
  fi
fi

# Do not start a second server on a port that is already serving something.
if command -v curl >/dev/null 2>&1 && curl -fsS -o /dev/null "$URL/" 2>/dev/null; then
  echo "Något lyssnar redan på $URL. Stoppa det eller välj en annan port med FRIBOK_PORT." >&2
  exit 1
fi

echo "Startar Fribok på $URL (databas: $DB)"
"$BIN" -addr "$ADDR" -db "$DB" >>"$LOG" 2>&1 &
SERVER_PID=$!

ready=0
i=0
while [ "$i" -lt 100 ]; do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "Fribok startade inte. Senaste logg:" >&2
    tail -n 20 "$LOG" >&2 || true
    exit 1
  fi
  if command -v curl >/dev/null 2>&1; then
    if curl -fsS -o /dev/null "$URL/" 2>/dev/null; then
      ready=1
      break
    fi
  elif command -v wget >/dev/null 2>&1; then
    if wget -q -O /dev/null "$URL/" 2>/dev/null; then
      ready=1
      break
    fi
  else
    sleep 1
    ready=1
    break
  fi
  i=$((i + 1))
  sleep 0.1
done

if [ "$ready" -ne 1 ]; then
  echo "Fribok svarade inte på $URL. Senaste logg:" >&2
  tail -n 20 "$LOG" >&2 || true
  exit 1
fi

open_browser() {
  if [ -n "${FRIBOK_NO_BROWSER:-}" ]; then
    return
  fi
  if [ -n "${FRIBOK_BROWSER:-}" ]; then
    "$FRIBOK_BROWSER" "$URL" >/dev/null 2>&1 &
    return
  fi
  if [ -n "${BROWSER:-}" ]; then
    "$BROWSER" "$URL" >/dev/null 2>&1 &
    return
  fi
  if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$URL" >/dev/null 2>&1 &
    return
  fi
  if command -v open >/dev/null 2>&1; then
    open "$URL" >/dev/null 2>&1 &
    return
  fi
  if command -v cmd.exe >/dev/null 2>&1; then
    cmd.exe /c start "" "$URL" >/dev/null 2>&1 &
    return
  fi
  echo "Öppna $URL i webbläsaren."
}

echo "Fribok körs. Stäng med Ctrl+C."
open_browser
wait "$SERVER_PID"
