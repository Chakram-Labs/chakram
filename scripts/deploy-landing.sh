#!/usr/bin/env bash
# Deploy the static landing page to the VM.
# Run from repo root: ./scripts/deploy-landing.sh [--init-nginx]
#
# Normal usage (just update files):
#   ./scripts/deploy-landing.sh
#
# First-time setup (write nginx config + certbot):
#   ./scripts/deploy-landing.sh --init-nginx
set -euo pipefail

VM="chakram-node"
REMOTE_DIR="/var/www/chakram-landing"
INIT_NGINX=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --init-nginx) INIT_NGINX=true; shift ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

echo "==> Creating web root directory on VM..."
ssh "$VM" "sudo mkdir -p ${REMOTE_DIR} && sudo chown \$(id -u):\$(id -g) ${REMOTE_DIR}"

echo "==> Syncing landing/ to VM..."
rsync -az --delete \
  --exclude='*.DS_Store' \
  landing/ "${VM}:${REMOTE_DIR}/"

if $INIT_NGINX; then
  echo "==> Writing Nginx config (first-time setup)..."
  ssh "$VM" "sudo tee /etc/nginx/sites-available/chakram > /dev/null" << 'NGINX'
server {
    listen 80;
    server_name chakram.one www.chakram.one;

    root /var/www/chakram-landing;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location ~* \.(css|js|png|jpg|svg|ico|woff2?)$ {
        expires 7d;
        add_header Cache-Control "public, immutable";
    }
}

server {
    listen 80;
    server_name node.chakram.one;
    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}

server {
    listen 80;
    server_name rpc.chakram.one;
    location / {
        proxy_pass http://localhost:8545;
        proxy_hide_header Access-Control-Allow-Origin;
        proxy_hide_header Vary;
        add_header Access-Control-Allow-Origin '*' always;
        add_header Access-Control-Allow-Methods 'GET, POST, OPTIONS' always;
        add_header Access-Control-Allow-Headers 'Content-Type' always;
        if ($request_method = OPTIONS) { return 204; }
    }
}
NGINX

  echo "==> Enabling site and reloading Nginx..."
  ssh "$VM" "sudo ln -sf /etc/nginx/sites-available/chakram /etc/nginx/sites-enabled/chakram \
    && sudo nginx -t \
    && sudo systemctl reload nginx"

  echo ""
  echo "==> Running certbot for HTTPS..."
  ssh "$VM" "sudo certbot --nginx \
    -d chakram.one -d www.chakram.one -d node.chakram.one -d rpc.chakram.one \
    --non-interactive --agree-tos --email adithyansatheesh707@gmail.com --redirect"
fi

echo ""
echo "Done. Landing page deployed."
if ! $INIT_NGINX; then
  echo "(Nginx config untouched — HTTPS cert preserved)"
fi
