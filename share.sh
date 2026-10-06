#!/usr/bin/env bash
# Start Lotaria and share it on the internet through an ngrok tunnel.
#
#   ./share.sh                 # random https://xxxx.ngrok-free.app address
#   NGROK_URL=my-name.ngrok-free.app ./share.sh   # your free static domain
#   PORT=9000 ./share.sh       # use another local port
#
# Press Ctrl+C to stop both the tunnel and the app.
set -euo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-8080}"
DATA="${DATA:-lotto.json}"

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

# 1. Tools
command -v go >/dev/null || die "Go is not installed. On Omarchy/Arch: sudo pacman -S go"
if ! command -v ngrok >/dev/null; then
  die "ngrok is not installed.
  On Omarchy/Arch:  yay -S ngrok
  Other systems:    https://ngrok.com/download
Then run this script again."
fi

# 2. ngrok account token (free account at https://dashboard.ngrok.com/signup)
if ! ngrok config check >/dev/null 2>&1 || ! grep -rqs "authtoken" "$(ngrok config check 2>/dev/null | grep -o '/[^ ]*\.yml' | head -n1)"; then
  die "ngrok needs your account token (one time only):
  1. Sign up free at https://dashboard.ngrok.com/signup
  2. Copy your token from https://dashboard.ngrok.com/get-started/your-authtoken
  3. Run: ngrok config add-authtoken <your-token>
Then run this script again."
fi

# 3. Build and start the app
say "Building Lotaria…"
go build -o lotto .

if ss -ltn 2>/dev/null | grep -q ":$PORT "; then
  die "Port $PORT is already in use. Stop the other program (or the app if it's already running), or use PORT=9000 ./share.sh"
fi

PUBLIC_FLAG=()
[ -n "${NGROK_URL:-}" ] && PUBLIC_FLAG=(-public-url "https://${NGROK_URL#https://}")
./lotto -addr ":$PORT" -data "$DATA" -trust-proxy "${PUBLIC_FLAG[@]}" &
APP_PID=$!
trap 'kill "$APP_PID" 2>/dev/null || true' EXIT INT TERM

for _ in $(seq 1 50); do
  curl -s -o /dev/null "http://localhost:$PORT/login" && break
  kill -0 "$APP_PID" 2>/dev/null || die "The app failed to start (see the error above)."
  sleep 0.1
done

if curl -s -o /dev/null -w '%{redirect_url}' "http://localhost:$PORT/login" | grep -q /setup; then
  say "No admin account yet: open http://localhost:$PORT/setup on THIS computer first."
  say "(For safety, setup isn't allowed through the shared link.)"
fi

# 4. Open the tunnel. ngrok shows the public https:// address — share that.
say "Starting ngrok… share the https:// address shown under 'Forwarding'. Ctrl+C to stop."
if [ -n "${NGROK_URL:-}" ]; then
  ngrok http "$PORT" --url "$NGROK_URL"
else
  ngrok http "$PORT"
fi
