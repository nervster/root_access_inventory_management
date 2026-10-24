#!/usr/bin/env bash
# Watches a site from outside (run once per site, from the Mac): deploy/setup-monitoring.sh staging you@example.com.
# A Google Cloud uptime check asks https://<site>/api/health every 5 minutes from several regions, and an alert
# emails you when it fails for 10 minutes (and again when it's back). Safe to run again: it reuses what exists.
# Calls the Monitoring API directly with your gcloud sign-in (its gcloud commands need the alpha/beta components).
set -euo pipefail

SITE=${1:-}
EMAIL=${2:-}
case "$SITE" in
  staging) DOMAIN=stock-staging.rootaccesshtx.com ;;
  production) DOMAIN=stock.rootaccesshtx.com ;;
  *) echo "Usage: deploy/setup-monitoring.sh staging|production you@example.com"; exit 1 ;;
esac
test -n "$EMAIL" || { echo "Usage: deploy/setup-monitoring.sh $SITE you@example.com"; exit 1; }
PROJECT=plant-stock-rah
NAME="Plant Stock $SITE"
API=https://monitoring.googleapis.com/v3/projects/$PROJECT

gcloud config set project "$PROJECT"
gcloud services enable monitoring.googleapis.com
TOKEN=$(gcloud auth print-access-token)
api() { curl -sSf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" "$@"; }
# The first of a list's items whose field equals value, or nothing.
find_in() { python3 -c 'import json,sys; items=json.load(sys.stdin).get(sys.argv[1],[]); print(next((i["name"] for i in items if i.get(sys.argv[2])==sys.argv[3] or i.get("labels",{}).get(sys.argv[2])==sys.argv[3]),""))' "$@"; }

# The uptime check: /api/health over https, a 2xx answer within 10 seconds, every 5 minutes.
CHECK=$(api "$API/uptimeCheckConfigs" | find_in uptimeCheckConfigs displayName "$NAME")
if [ -z "$CHECK" ]; then
  gcloud monitoring uptime create "$NAME" --resource-type=uptime-url \
    --resource-labels="host=$DOMAIN,project_id=$PROJECT" --protocol=https --path=/api/health \
    --period=5 --timeout=10
  CHECK=$(api "$API/uptimeCheckConfigs" | find_in uptimeCheckConfigs displayName "$NAME")
fi
CHECK_ID=${CHECK##*/}
echo "Uptime check: $CHECK_ID"

# Where alerts go: an email channel for this address.
CHANNEL=$(api "$API/notificationChannels" | find_in notificationChannels email_address "$EMAIL")
if [ -z "$CHANNEL" ]; then
  CHANNEL=$(api -X POST "$API/notificationChannels" \
    -d "{\"type\": \"email\", \"displayName\": \"Plant Stock alerts ($EMAIL)\", \"labels\": {\"email_address\": \"$EMAIL\"}}" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')
fi
echo "Alerts go to: $EMAIL"

# The alert: the check failing from more than one region for 10 minutes (Google's usual uptime alert).
if [ -z "$(api "$API/alertPolicies" | find_in alertPolicies displayName "$NAME is down")" ]; then
  api -X POST "$API/alertPolicies" -d @- > /dev/null <<JSON
{
  "displayName": "$NAME is down",
  "combiner": "OR",
  "conditions": [{
    "displayName": "https://$DOMAIN/api/health isn't answering",
    "conditionThreshold": {
      "filter": "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND metric.label.check_id=\"$CHECK_ID\" AND resource.type=\"uptime_url\"",
      "aggregations": [{
        "alignmentPeriod": "1200s", "perSeriesAligner": "ALIGN_NEXT_OLDER",
        "crossSeriesReducer": "REDUCE_COUNT_FALSE", "groupByFields": ["resource.label.*"]
      }],
      "comparison": "COMPARISON_GT", "thresholdValue": 1, "duration": "600s", "trigger": {"count": 1}
    }
  }],
  "notificationChannels": ["$CHANNEL"],
  "documentation": {
    "mimeType": "text/markdown",
    "content": "Plant Stock ($SITE) isn't answering at https://$DOMAIN/api/health. Check the VM: \`gcloud compute ssh plant-stock-$SITE --zone us-central1-a --tunnel-through-iap\`, then \`cd /opt/plant-stock && sudo docker compose ps\` and \`sudo docker compose logs --tail 50 api\`."
  }
}
JSON
fi
echo "Done: $EMAIL is emailed when https://$DOMAIN/api/health fails for 10 minutes, and when it's back."
echo "Also set ALERT_EMAIL=$EMAIL in deploy/.env.$SITE and deploy, so a failed nightly backup emails you too."
