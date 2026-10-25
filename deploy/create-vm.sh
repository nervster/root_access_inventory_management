#!/usr/bin/env bash
# Creates one site's VM in Google Cloud (run once per site, from the Mac): deploy/create-vm.sh staging|production.
# An e2-micro in us-central1 (next to the file buckets), a static IP, and firewall rules (shared by both
# sites); then sets it up with setup-vm.sh. The free tier covers one e2-micro's hours a month in total.
# SSH only works through Google's IAP tunnel (gcloud compute ssh --tunnel-through-iap), never from the internet.
set -euo pipefail
cd "$(dirname "$0")"

SITE=${1:-}
case "$SITE" in
  staging | production) ;;
  *) echo "Usage: deploy/create-vm.sh staging|production"; exit 1 ;;
esac
PROJECT=plant-stock-rah
REGION=us-central1
ZONE=us-central1-a
VM=plant-stock-$SITE

gcloud config set project "$PROJECT"
gcloud services enable compute.googleapis.com iap.googleapis.com

# A static address, so the DNS record never has to change.
gcloud compute addresses create "$VM-ip" --region "$REGION"

# Open only the web ports to everyone, and SSH only to Google's IAP range (made by the first site's run).
if ! gcloud compute firewall-rules describe allow-web > /dev/null 2>&1; then
  gcloud compute firewall-rules create allow-web --network default \
    --allow tcp:80,tcp:443,udp:443 --target-tags web
  gcloud compute firewall-rules create allow-ssh-from-iap --network default \
    --allow tcp:22 --source-ranges 35.235.240.0/20 --target-tags web
fi
# The default network's rules open SSH and RDP to the whole internet; remove them.
for rule in default-allow-ssh default-allow-rdp; do
  if gcloud compute firewall-rules describe "$rule" > /dev/null 2>&1; then
    gcloud compute firewall-rules delete "$rule" --quiet
  fi
done

# Free tier: e2-micro in us-central1, us-east1, or us-west1, with up to 30 GB of standard disk.
# No service account: the app reaches Cloud Storage with its own HMAC key. Sign-in through OS Login:
# access comes from Google accounts' roles (owners, and GitHub Actions' deploy account), not copied keys.
gcloud compute instances create "$VM" --zone "$ZONE" \
  --machine-type e2-micro \
  --image-family debian-12 --image-project debian-cloud \
  --boot-disk-size 30GB --boot-disk-type pd-standard \
  --address "$VM-ip" --tags web \
  --no-service-account --no-scopes \
  --metadata enable-oslogin=TRUE \
  --shielded-secure-boot

echo "Waiting for SSH…"
until gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command true 2> /dev/null; do sleep 10; done
gcloud compute scp setup-vm.sh "$VM:" --zone "$ZONE" --tunnel-through-iap
gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command 'sudo bash setup-vm.sh && rm setup-vm.sh'

echo "Point an A record for the $SITE domain at: $(gcloud compute addresses describe "$VM-ip" --region "$REGION" --format 'value(address)')"
