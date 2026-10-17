#!/usr/bin/env bash
# Sets up a fresh Debian 12 VM to run the Compose stack: swap, Docker, automatic security updates.
# create-vm.sh runs it once (sudo bash setup-vm.sh); safe to run again.
# The firewall is Google Cloud's (create-vm.sh): only 80/443 are open, and SSH only through IAP.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive # no install prompts (there's no terminal to answer them)
APP_DIR=/opt/plant-stock
APP_USER=${SUDO_USER:?run with sudo as your own user}

# 1 GB of memory is tight for Postgres + Go + nginx + Caddy; swap keeps a spike from killing a container.
if ! swapon --show | grep -q /swapfile; then
  fallocate -l 2G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  echo '/swapfile none swap sw 0 0' >> /etc/fstab
  echo 'vm.swappiness=10' > /etc/sysctl.d/99-swappiness.conf
  sysctl --system > /dev/null
fi

# Docker from Docker's own apt repository (Debian's is older and has no Compose plugin).
apt-get update
apt-get install -y ca-certificates curl unattended-upgrades
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Cap container logs so they can't fill the 30 GB disk.
cat > /etc/docker/daemon.json <<'EOF'
{ "log-driver": "local", "log-opts": { "max-size": "10m", "max-file": "5" } }
EOF
systemctl restart docker
usermod -aG docker "$APP_USER"

# Security updates install daily. If one needs a reboot, it happens at 09:30 UTC (3:30–4:30 am in Houston);
# the containers come back on their own (restart: unless-stopped).
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
cat > /etc/apt/apt.conf.d/52plant-stock-upgrades <<'EOF'
Unattended-Upgrade::Origins-Pattern { "origin=Debian,codename=${distro_codename}-security"; "origin=Docker"; };
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "09:30";
EOF

# Where deploy.sh puts compose.yml, Caddyfile, and .env.
install -d -o "$APP_USER" -g "$APP_USER" -m 0750 "$APP_DIR"

echo "Set up. Deploy from the Mac with deploy/deploy.sh."
