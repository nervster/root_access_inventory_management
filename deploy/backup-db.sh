#!/usr/bin/env bash
# Backs up the site's database to its backup bucket (runs on the VM as /usr/local/bin/plant-stock-backup,
# nightly from plant-stock-backup.timer; deploy.sh installs both). The bucket deletes backups after 30 days.
# Uploads as the VM's service account, which may only add files to that bucket, not read or delete them.
#   Run now:   sudo systemctl start plant-stock-backup
#   Its logs:  journalctl -u plant-stock-backup
set -euo pipefail

BUCKET=${1:?usage: plant-stock-backup <bucket>}
NAME=nms-$(date -u +%Y-%m-%dT%H%M%SZ).dump
FILE=$(mktemp /var/tmp/plant-stock-backup.XXXXXX)
trap 'rm -f "$FILE"' EXIT

# Custom format: compressed, and pg_restore can restore all of it or single tables.
docker compose -f /opt/plant-stock/compose.yml exec -T postgres pg_dump -U nms -d nms --format custom > "$FILE"
# A plain upload, not gcloud storage cp: cp first reads the destination, which the account may not do.
# ifGenerationMatch=0: only adds, never overwrites. The token is the VM's account, from the metadata server.
TOKEN=$(curl -sSf -H 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
curl -sSf -o /dev/null -X POST --data-binary @"$FILE" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/octet-stream' \
  "https://storage.googleapis.com/upload/storage/v1/b/$BUCKET/o?uploadType=media&name=db%2F$NAME&ifGenerationMatch=0"
echo "Backed up $(du -h "$FILE" | cut -f1) to gs://$BUCKET/db/$NAME"
