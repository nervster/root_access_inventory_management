#!/usr/bin/env bash
# Emails ALERT_EMAIL that the nightly database backup failed, with the end of its log. Runs on the VM as
# /usr/local/bin/plant-stock-backup-failed, started by systemd when plant-stock-backup.service fails
# (OnFailure); deploy.sh installs both. Sends through the site's own SMTP settings (Resend), from .env.
#   Try it:  sudo ALERT_EMAIL=you@example.com /usr/local/bin/plant-stock-backup-failed staging
set -euo pipefail

SITE=${1:?usage: plant-stock-backup-failed <site>}
ENV_FILE=/opt/plant-stock/.env
# One setting from .env (Compose's format: maybe quoted).
setting() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"; }

TO=${ALERT_EMAIL:-$(setting ALERT_EMAIL)}
if [ -z "$TO" ]; then
  echo "ALERT_EMAIL isn't set in $ENV_FILE: no email sent"
  exit 0
fi
SMTP_ADDR=$(setting SMTP_ADDR)
SMTP_USERNAME=$(setting SMTP_USERNAME)
SMTP_PASSWORD=$(setting SMTP_PASSWORD)
FROM=$(setting EMAIL_FROM)
FROM_ADDRESS=$(printf '%s' "$FROM" | sed -n 's/.*<\(.*\)>.*/\1/p')
FROM_ADDRESS=${FROM_ADDRESS:-$FROM}

MESSAGE=$(mktemp)
trap 'rm -f "$MESSAGE"' EXIT
{
  printf 'From: %s\r\nTo: %s\r\nSubject: Plant Stock %s: the nightly database backup failed\r\n' "$FROM" "$TO" "$SITE"
  printf 'Content-Type: text/plain; charset=UTF-8\r\n\r\n'
  echo "The database backup on $(hostname) failed at $(date -u '+%Y-%m-%d %H:%M UTC'). The end of its log:"
  echo
  journalctl -u plant-stock-backup -n 30 --no-pager
  echo
  echo "To try again on the VM: sudo systemctl start plant-stock-backup"
} > "$MESSAGE"

# Port 587: curl switches to TLS (STARTTLS) and refuses to go on without it (--ssl-reqd).
curl --silent --show-error --ssl-reqd --url "smtp://$SMTP_ADDR" --user "$SMTP_USERNAME:$SMTP_PASSWORD" \
  --mail-from "$FROM_ADDRESS" --mail-rcpt "$TO" --upload-file "$MESSAGE"
echo "Emailed $TO that the backup failed"
