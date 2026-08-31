#!/bin/bash
set -euo pipefail

ARCHIVE="${1:?Usage: $0 <path-to-tar.gz> [target-dir]}"
TARGET_DIR="${2:-./gophish}"

mkdir -p "$TARGET_DIR"
tar -xzf "$ARCHIVE" -C "$TARGET_DIR"

cd "$TARGET_DIR"

# ---------------------------------------------------------------------------
# Domain configuration (must happen before building the Docker image)
# ---------------------------------------------------------------------------

CUSTOM_YAML="/home/user-data/www/custom.yaml"
WEB_UPDATE="/root/mailinabox/tools/web_update"
DOMAIN_REGEX='^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$'

validate_domain() {
    [[ "$1" =~ $DOMAIN_REGEX ]]
}

# --- Phishing server domain (reverse-proxied to port 8080) ---
while true; do
    read -rp "Enter the full domain for the phishing server (e.g. phish.example.com): " PHISH_DOMAIN
    if validate_domain "$PHISH_DOMAIN"; then
        break
    fi
    echo "  -> '$PHISH_DOMAIN' is not a valid domain. Please try again." >&2
done

# --- Admin server domain (reverse-proxied to port 3333) ---
while true; do
    read -rp "Enter the full domain for the admin server (e.g. admin.example.com): " ADMIN_DOMAIN
    if validate_domain "$ADMIN_DOMAIN"; then
        break
    fi
    echo "  -> '$ADMIN_DOMAIN' is not a valid domain. Please try again." >&2
done

if [[ "$PHISH_DOMAIN" == "$ADMIN_DOMAIN" ]]; then
    echo "Error: the phishing domain and admin domain must be different." >&2
    exit 1
fi

echo
echo "Phishing server : $PHISH_DOMAIN  ->  http://127.0.0.1:8080"
echo "Admin server    : $ADMIN_DOMAIN  ->  http://127.0.0.1:3333"
echo

# --- Write the MiaB custom.yaml proxy configuration ---
if [[ ! -d "$(dirname "$CUSTOM_YAML")" ]]; then
    echo "Error: directory $(dirname "$CUSTOM_YAML") does not exist." >&2
    echo "       Is this a Mail-in-a-Box server?" >&2
    exit 1
fi

if [[ -f "$CUSTOM_YAML" ]]; then
    cp "$CUSTOM_YAML" "${CUSTOM_YAML}.bak.$(date +%Y%m%d%H%M%S)"
    echo "Backed up existing $CUSTOM_YAML"
fi

cat > "$CUSTOM_YAML" <<EOF
${ADMIN_DOMAIN}:
  proxies:
    /: "http://127.0.0.1:3333"

${PHISH_DOMAIN}:
  proxies:
    /: "http://127.0.0.1:8080"
EOF

echo "Wrote proxy configuration to $CUSTOM_YAML"

# --- Trigger MiaB web update to apply the new nginx configuration ---
if [[ -x "$WEB_UPDATE" ]]; then
    echo "Running $WEB_UPDATE ..."
    "$WEB_UPDATE"
else
    echo "Warning: $WEB_UPDATE not found or not executable — skipping web update." >&2
fi

# --- Set ADMIN_TRUSTED_ORIGINS in docker-compose.yml ---
sed -i "s|ADMIN_TRUSTED_ORIGINS: \".*\"|ADMIN_TRUSTED_ORIGINS: \"${ADMIN_DOMAIN}\"|" docker-compose.yml
echo "Set ADMIN_TRUSTED_ORIGINS=${ADMIN_DOMAIN} in docker-compose.yml"

# --- Build and start the containers ---
docker compose up -d --build
