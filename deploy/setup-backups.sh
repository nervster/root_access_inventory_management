#!/usr/bin/env bash
# Sets up a site's database backups in Google Cloud (run once per site, from the Mac): deploy/setup-backups.sh staging.
# A private bucket that deletes backups after 30 days, and a service account for the VM that may only add
# files to it (so nothing on the VM can read or delete backups). The VM is stopped for about a minute to get
# the account. deploy.sh then installs the nightly job (backup-db.sh); restore-db.sh checks or restores.
set -euo pipefail
cd "$(dirname "$0")"

SITE=${1:-}
case "$SITE" in
  staging | production) ;;
  *) echo "Usage: deploy/setup-backups.sh staging|production"; exit 1 ;;
esac
PROJECT=plant-stock-rah
REGION=us-central1
ZONE=us-central1-a
VM=plant-stock-$SITE
BUCKET=plant-stock-rah-$SITE-backups
VM_ACCOUNT=$SITE-vm
VM_ACCOUNT_EMAIL=$VM_ACCOUNT@$PROJECT.iam.gserviceaccount.com

gcloud config set project "$PROJECT"

LIFECYCLE=$(mktemp)
trap 'rm -f "$LIFECYCLE"' EXIT
echo '{"rule": [{"action": {"type": "Delete"}, "condition": {"age": 30}}]}' > "$LIFECYCLE"
gcloud storage buckets create "gs://$BUCKET" --location "$REGION" --default-storage-class STANDARD \
  --uniform-bucket-level-access --public-access-prevention --soft-delete-duration 7d
gcloud storage buckets update "gs://$BUCKET" --lifecycle-file "$LIFECYCLE"

gcloud iam service-accounts create "$VM_ACCOUNT" --display-name "Plant Stock $SITE VM: adds database backups"
sleep 10 # a new service account takes a moment to be usable in IAM bindings
gcloud storage buckets add-iam-policy-binding "gs://$BUCKET" --member "serviceAccount:$VM_ACCOUNT_EMAIL" \
  --role roles/storage.objectCreator --format none

echo "Stopping $VM for about a minute to give it the account…"
gcloud compute instances stop "$VM" --zone "$ZONE"
gcloud compute instances set-service-account "$VM" --zone "$ZONE" \
  --service-account "$VM_ACCOUNT_EMAIL" --scopes storage-rw
gcloud compute instances start "$VM" --zone "$ZONE"
until gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command true 2> /dev/null; do sleep 10; done

echo "Done. Next: deploy (merge into staging, or deploy/deploy.sh $SITE) to install the nightly backup,"
echo "then test it: back up now on the VM (sudo systemctl start plant-stock-backup), and deploy/restore-db.sh $SITE"
