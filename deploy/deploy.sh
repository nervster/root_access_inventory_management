#!/usr/bin/env bash
# Deploys the current checkout to one site's VM (run from the Mac): deploy/deploy.sh staging|production.
# Builds the amd64 images here (the VM is too small to build them), loads them onto the VM over SSH,
# copies compose.yml, Caddyfile, and the site's settings (deploy/.env.<site>, as .env), and restarts what
# changed. Until GitHub Actions does this (see docs/ROADMAP.md).
set -euo pipefail
cd "$(dirname "$0")/.."

SITE=${1:-}
case "$SITE" in
  staging | production) ;;
  *) echo "Usage: deploy/deploy.sh staging|production"; exit 1 ;;
esac
ZONE=us-central1-a
VM=plant-stock-$SITE
APP_DIR=/opt/plant-stock
ENV_FILE=deploy/.env.$SITE
ssh_vm() { gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap "$@"; }

test -f "$ENV_FILE" || { echo "Missing $ENV_FILE (copy deploy/.env.example and fill it in)"; exit 1; }
# Read just this key: the file is Compose's format, not always valid shell.
VITE_CLERK_PUBLISHABLE_KEY=$(grep '^VITE_CLERK_PUBLISHABLE_KEY=' "$ENV_FILE" | cut -d= -f2-)
test -n "$VITE_CLERK_PUBLISHABLE_KEY" || { echo "Set VITE_CLERK_PUBLISHABLE_KEY in $ENV_FILE"; exit 1; }

# Each deploy's images are tagged with the commit (-dirty for uncommitted changes), then :current.
TAG=$(git rev-parse --short HEAD)$(git diff --quiet HEAD || echo -dirty)
docker build --platform linux/amd64 -t "plant-stock-api:$TAG" backend
docker build --platform linux/amd64 --build-arg VITE_CLERK_PUBLISHABLE_KEY="$VITE_CLERK_PUBLISHABLE_KEY" \
  -t "plant-stock-web:$TAG" frontend

echo "Loading images onto ${VM}…"
docker save "plant-stock-api:$TAG" "plant-stock-web:$TAG" | gzip | ssh_vm --command 'gunzip | docker load'
gcloud compute scp deploy/compose.yml deploy/Caddyfile "$ENV_FILE" "$VM:$APP_DIR/" --zone "$ZONE" --tunnel-through-iap

ssh_vm --command "set -e; cd $APP_DIR; mv .env.$SITE .env; chmod 600 .env
  docker tag plant-stock-api:$TAG plant-stock-api:current
  docker tag plant-stock-web:$TAG plant-stock-web:current
  docker compose up -d --quiet-pull --remove-orphans
  docker image prune -af --filter until=720h > /dev/null # keeps a month of images to roll back to
  docker compose ps"
echo "Deployed $TAG to $SITE"
