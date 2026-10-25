#!/usr/bin/env bash
# Restores a site's database from its backup bucket (run from the Mac, which can read the bucket).
#   deploy/restore-db.sh staging                     check: restore the newest backup into a throwaway
#                                                    database on the VM, show what's in it, then drop it
#   deploy/restore-db.sh staging --replace [backup]  disaster: replace the site's database with a backup
#                                                    (the newest, or one named like nms-2026-10-18T070112Z.dump)
# --replace stops the API and worker, saves the current database on the VM first (~/before-restore-*.dump),
# then restores and starts them again.
set -euo pipefail

SITE=${1:-}
case "$SITE" in
  staging | production) ;;
  *) echo "Usage: deploy/restore-db.sh staging|production [--replace [backup]]"; exit 1 ;;
esac
MODE=check
if [ "${2:-}" = --replace ]; then MODE=replace; fi
NAME=${3:-}
ZONE=us-central1-a
VM=plant-stock-$SITE
BUCKET=plant-stock-rah-$SITE-backups
ssh_vm() { gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap "$@"; }

if [ -z "$NAME" ]; then
  NAME=$(gcloud storage ls "gs://$BUCKET/db/" | sort | tail -1 | xargs basename)
fi
test -n "$NAME" || { echo "No backups in gs://$BUCKET/db/ yet"; exit 1; }
echo "Backup: $NAME"

if [ "$MODE" = replace ]; then
  read -r -p "This replaces the $SITE database with $NAME. Type '$SITE' to go ahead: " answer
  test "$answer" = "$SITE" || { echo "Stopped; nothing changed."; exit 1; }
fi

LOCAL=$(mktemp -d)
trap 'rm -rf "$LOCAL"' EXIT
gcloud storage cp "gs://$BUCKET/db/$NAME" "$LOCAL/restore.dump"
gcloud compute scp "$LOCAL/restore.dump" "$VM:restore.dump" --zone "$ZONE" --tunnel-through-iap

# Inside the Postgres container; nms is the superuser, connecting to the postgres database.
psql_on_vm='sudo docker compose exec -T postgres psql -U nms -d postgres -v ON_ERROR_STOP=1'
counts="select 'organizations', count(*) from organizations union all select 'users', count(*) from users
  union all select 'plants', count(*) from plants union all select 'products', count(*) from products
  union all select 'orders', count(*) from orders union all select 'last migration', max(version_id) from goose_db_version"

if [ "$MODE" = check ]; then
  ssh_vm --command "set -e; cd /opt/plant-stock
    $psql_on_vm -c 'drop database if exists restore_check' -c 'create database restore_check'
    sudo docker compose exec -T postgres pg_restore -U nms -d restore_check --exit-on-error < ~/restore.dump
    echo 'Restored into a throwaway database:'
    sudo docker compose exec -T postgres psql -U nms -d restore_check -At -F ': ' -c \"$counts\"
    $psql_on_vm -c 'drop database restore_check'
    rm ~/restore.dump"
  echo "The backup restores. (The site's own database wasn't touched.)"
else
  ssh_vm --command "set -e; cd /opt/plant-stock
    sudo docker compose stop api worker
    sudo docker compose exec -T postgres pg_dump -U nms -d nms --format custom > ~/before-restore-\$(date -u +%Y-%m-%dT%H%M%SZ).dump
    $psql_on_vm -c 'drop database nms with (force)' -c 'create database nms'
    sudo docker compose exec -T postgres pg_restore -U nms -d nms --exit-on-error < ~/restore.dump
    sudo docker compose exec -T postgres psql -U nms -d nms -At -F ': ' -c \"$counts\"
    sudo docker compose start api worker
    rm ~/restore.dump"
  echo "Restored $NAME into $SITE. The database from before is saved on the VM as ~/before-restore-*.dump."
fi
