#!/usr/bin/env bash
# Lets GitHub Actions deploy one site (run once per site, from the Mac): deploy/setup-github-deploy.sh staging.
# GitHub signs in to Google through Workload Identity Federation (no stored key): only workflow runs from this
# repo's matching branch (staging → staging, main → production) get a short-lived token for the deploy account,
# which may only open IAP tunnels and sign in to that site's VM as an admin through OS Login.
set -euo pipefail

SITE=${1:-}
case "$SITE" in
  staging) BRANCH=staging ;;
  production) BRANCH=main ;;
  *) echo "Usage: deploy/setup-github-deploy.sh staging|production"; exit 1 ;;
esac
PROJECT=plant-stock-rah
ZONE=us-central1-a
VM=plant-stock-$SITE
REPO=nervster/root_access_inventory_management
POOL=github
PROVIDER=github-$SITE
DEPLOYER=github-deploy-$SITE
DEPLOYER_EMAIL=$DEPLOYER@$PROJECT.iam.gserviceaccount.com

gcloud config set project "$PROJECT"
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format 'value(projectNumber)')
gcloud services enable iamcredentials.googleapis.com sts.googleapis.com iam.googleapis.com oslogin.googleapis.com

# The pool is shared by both sites; each site's provider only accepts its own branch.
if ! gcloud iam workload-identity-pools describe "$POOL" --location global > /dev/null 2>&1; then
  gcloud iam workload-identity-pools create "$POOL" --location global --display-name "GitHub Actions"
fi
gcloud iam workload-identity-pools providers create-oidc "$PROVIDER" --location global \
  --workload-identity-pool "$POOL" --display-name "Plant Stock repo, $BRANCH branch" \
  --issuer-uri https://token.actions.githubusercontent.com \
  --attribute-mapping "google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.ref=assertion.ref" \
  --attribute-condition "assertion.repository == '$REPO' && assertion.ref == 'refs/heads/$BRANCH'"

gcloud iam service-accounts create "$DEPLOYER" --display-name "GitHub Actions: deploy $SITE"
sleep 10 # a new service account takes a moment to be usable in IAM bindings
# The workflow runs in the GitHub environment named after the site, so its token's subject is
# repo:<repo>:environment:<site> (the provider above already checked the branch).
gcloud iam service-accounts add-iam-policy-binding "$DEPLOYER_EMAIL" --role roles/iam.workloadIdentityUser \
  --member "principal://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL/subject/repo:$REPO:environment:$SITE" \
  --format none

# Open IAP tunnels (the firewall only lets IAP reach the VMs, on port 22), look up the VM, and sign in to
# this VM only, with sudo (Docker needs it).
gcloud projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$DEPLOYER_EMAIL" \
  --role roles/iap.tunnelResourceAccessor --condition None --format none
gcloud projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$DEPLOYER_EMAIL" \
  --role roles/compute.viewer --condition None --format none
gcloud compute instances add-iam-policy-binding "$VM" --zone "$ZONE" --member "serviceAccount:$DEPLOYER_EMAIL" \
  --role roles/compute.osAdminLogin --format none

# Sign-in through OS Login (create-vm.sh turns it on for new VMs), and the app folder owned by root.
gcloud compute instances add-metadata "$VM" --zone "$ZONE" --metadata enable-oslogin=TRUE
echo "Waiting for the VM to switch to OS Login…"
until gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command true 2> /dev/null; do sleep 10; done
gcloud compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command \
  'sudo chown -R root:root /opt/plant-stock && sudo chmod 0755 /opt/plant-stock && sudo chmod 0600 /opt/plant-stock/.env'

echo
echo "Done. In GitHub (Settings → Environments → $SITE), add these variables:"
echo "  GCP_WORKLOAD_IDENTITY_PROVIDER = projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL/providers/$PROVIDER"
echo "  GCP_DEPLOY_SERVICE_ACCOUNT     = $DEPLOYER_EMAIL"
echo "  VITE_CLERK_PUBLISHABLE_KEY     = the site's Clerk publishable key (pk_…, public)"
