#!/usr/bin/env bash
# Deploys the current checkout to one site's VM: deploy/deploy.sh staging|production.
# Builds the amd64 images (the VM is too small to build them), loads them onto the VM over SSH, copies
# compose.yml and Caddyfile, installs the nightly database backup (backup-db.sh and its systemd timer),
# and restarts what changed. Runs from the Mac, or from GitHub Actions on each
# merge into staging (.github/workflows/deploy-staging.yml).
# The site's settings (secrets included) live on the VM in /opt/plant-stock/.env. When deploy/.env.<site>
# exists (on the Mac), it replaces them; GitHub Actions has no copy, so the VM's settings are kept.
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

FILES=(deploy/compose.yml deploy/Caddyfile deploy/backup-db.sh deploy/plant-stock-backup.service deploy/plant-stock-backup.timer)
if [ -f "$ENV_FILE" ]; then
  FILES+=("$ENV_FILE")
  # Read just this key (and drop any quotes): the file is Compose's format, not always valid shell.
  VITE_CLERK_PUBLISHABLE_KEY=${VITE_CLERK_PUBLISHABLE_KEY:-$(grep '^VITE_CLERK_PUBLISHABLE_KEY=' "$ENV_FILE" | cut -d= -f2- | tr -d '"')}
fi
test -n "${VITE_CLERK_PUBLISHABLE_KEY:-}" || {
  echo "Set VITE_CLERK_PUBLISHABLE_KEY (in $ENV_FILE, or in the environment)"
  exit 1
}

# Each deploy's images are tagged with the commit (-dirty for uncommitted changes), then :current.
TAG=$(git rev-parse --short HEAD)$(git diff --quiet HEAD || echo -dirty)
docker build --platform linux/amd64 -t "plant-stock-api:$TAG" backend
docker build --platform linux/amd64 --build-arg VITE_CLERK_PUBLISHABLE_KEY="$VITE_CLERK_PUBLISHABLE_KEY" \
  -t "plant-stock-web:$TAG" frontend

# /opt/plant-stock belongs to root, so the files go to the home folder first and sudo moves them.
echo "Loading images onto ${VM}…"
docker save "plant-stock-api:$TAG" "plant-stock-web:$TAG" | gzip | ssh_vm --command 'gunzip | sudo docker load'
ssh_vm --command 'rm -rf ~/plant-stock-upload && mkdir ~/plant-stock-upload'
gcloud compute scp "${FILES[@]}" "$VM:plant-stock-upload/" --zone "$ZONE" --tunnel-through-iap

ssh_vm --command "set -e; cd ~/plant-stock-upload
  sudo install -m 0644 compose.yml Caddyfile $APP_DIR/
  if [ -f .env.$SITE ]; then sudo install -m 0600 .env.$SITE $APP_DIR/.env; fi
  sudo install -m 0755 backup-db.sh /usr/local/bin/plant-stock-backup
  sed 's/@SITE@/$SITE/' plant-stock-backup.service | sudo tee /etc/systemd/system/plant-stock-backup.service > /dev/null
  sudo install -m 0644 plant-stock-backup.timer /etc/systemd/system/
  sudo systemctl daemon-reload && sudo systemctl enable --now --quiet plant-stock-backup.timer
  cd $APP_DIR && rm -rf ~/plant-stock-upload
  sudo docker tag plant-stock-api:$TAG plant-stock-api:current
  sudo docker tag plant-stock-web:$TAG plant-stock-web:current
  sudo docker compose up -d --quiet-pull --remove-orphans
  sudo docker image prune -af --filter until=720h > /dev/null # keeps a month of images to roll back to
  sudo docker compose ps"
echo "Deployed $TAG to $SITE"
