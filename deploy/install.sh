#!/usr/bin/env bash
# Piumy one-command installer for Raspberry Pi OS Lite (64-bit).
#
# Provisions the switchboard + e-paper display as systemd services and applies
# the power-loss resilience measures (3rd commandment): the Pi has no safe
# shutdown button, so nothing may corrupt or hang on a sudden power cut.
#
# Idempotent: safe to re-run. Run ON the Pi:
#     sudo ./install.sh                # install / update, then prompt to reboot
#     sudo ./install.sh --reboot       # install / update and reboot at the end
#     sudo ./install.sh --overlay      # also enable read-only overlay root (strong)
#     sudo ./install.sh --no-display   # core only (headless, no e-paper)
#     sudo ./install.sh --no-power     # skip the CW2015 battery service
#
# Expects the built artifacts next to this script's parent (the coderoot tree):
#     ../core/pimywa            (linux/arm64 binary, pre-built; or built here if Go present)
#     ../adapters/display/      (Python display adapter)
#     ../adapters/power/        (Python power/battery adapter)
set -euo pipefail

# ---- args -------------------------------------------------------------------
DO_REBOOT=0; DO_OVERLAY=0; WITH_DISPLAY=1; WITH_POWER=1
for a in "$@"; do
  case "$a" in
    --reboot) DO_REBOOT=1 ;;
    --overlay) DO_OVERLAY=1 ;;
    --no-display) WITH_DISPLAY=0 ;;
    --no-power) WITH_POWER=0 ;;
    *) echo "unknown arg: $a"; exit 2 ;;
  esac
done

[ "$(id -u)" -eq 0 ] || { echo "run with sudo"; exit 1; }

# Deploying user = the one who owns the repo / invoked sudo.
RUN_USER="${SUDO_USER:-$(logname 2>/dev/null || echo pi)}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
INSTALL_DIR=/opt/pimywa
DATA_DIR="$INSTALL_DIR/data"           # persistent (DBs, WhatsApp session)
BOOT_CFG=/boot/firmware/config.txt
[ -f "$BOOT_CFG" ] || BOOT_CFG=/boot/config.txt

log() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

# ---- 1. packages ------------------------------------------------------------
log "Installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# Base set (these exist on Raspberry Pi OS / Debian trixie).
apt-get install -y python3 python3-pip python3-pil python3-spidev \
                   fonts-dejavu-core avahi-daemon zram-tools || true
# libgpiod python binding: package name varies by release; the Pi usually
# already ships it. Try both names, never fail the install over it.
apt-get install -y python3-libgpiod 2>/dev/null \
  || apt-get install -y python3-gpiod 2>/dev/null || true
# qrcode: apt if present, else pip (needed only for the QR linking screen).
apt-get install -y python3-qrcode 2>/dev/null \
  || pip3 install --break-system-packages "qrcode[pil]>=7" 2>/dev/null || true
# smbus2: generic i2c-dev binding for the CW2015 battery backend (D3) -- apt
# if present, else pip. Not a CW2015 vendor SDK, same "generic Linux
# interface" rule as spidev/gpiod above.
apt-get install -y python3-smbus2 2>/dev/null \
  || pip3 install --break-system-packages "smbus2>=0.4" 2>/dev/null || true
# Verify the imports the display/power adapters actually need; warn (don't
# abort) if missing.
python3 -c 'import PIL' 2>/dev/null   || echo "WARN: Pillow missing (display will no-op)"
python3 -c 'import gpiod' 2>/dev/null || echo "WARN: gpiod missing (e-paper GPIO will no-op)"
python3 -c 'import spidev' 2>/dev/null|| echo "WARN: spidev missing (e-paper SPI will no-op)"
python3 -c 'import qrcode' 2>/dev/null|| echo "WARN: qrcode missing (QR link screen will be blank)"
python3 -c 'import smbus2' 2>/dev/null|| echo "WARN: smbus2 missing (battery reading will no-op)"

# ---- 2. SPI + I2C -----------------------------------------------------------
log "Enabling SPI + I2C ($BOOT_CFG)"
enable_param() { grep -q "^$1" "$BOOT_CFG" || echo "$1" >> "$BOOT_CFG"; }
enable_param "dtparam=spi=on"
enable_param "dtparam=i2c_arm=on"
grep -q '^i2c-dev' /etc/modules || echo 'i2c-dev' >> /etc/modules

# ---- 3. hardware watchdog (auto-reboot if the system hangs) ------------------
log "Enabling hardware watchdog"
enable_param "dtparam=watchdog=on"
sed -i 's/^#\?RuntimeWatchdogSec=.*/RuntimeWatchdogSec=15/' /etc/systemd/system.conf
grep -q '^RuntimeWatchdogSec=' /etc/systemd/system.conf || echo 'RuntimeWatchdogSec=15' >> /etc/systemd/system.conf

# ---- 4. journald: cap + volatile (protect the SD, keep RAM tidy) ------------
log "Capping journald (volatile, 50M)"
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/pimywa.conf <<'EOF'
[Journal]
Storage=volatile
RuntimeMaxUse=50M
EOF

# ---- 5. zram swap (swap in RAM, never thrash the SD) ------------------------
log "Configuring zram swap"
cat > /etc/default/zramswap <<'EOF'
ALGO=lz4
PERCENT=50
EOF
systemctl enable --now zramswap.service 2>/dev/null || true

# ---- 6. tmpfs runtime dir for status.json (transient -> no SD wear) ---------
log "Runtime dir /run/pimywa (tmpfs via tmpfiles.d)"
echo "d /run/pimywa 0755 $RUN_USER $RUN_USER -" > /etc/tmpfiles.d/pimywa.conf
systemd-tmpfiles --create /etc/tmpfiles.d/pimywa.conf

# ---- 7. files ---------------------------------------------------------------
log "Installing files to $INSTALL_DIR"
mkdir -p "$INSTALL_DIR" "$DATA_DIR" "$INSTALL_DIR/display" "$INSTALL_DIR/power"

# binary: prefer a pre-built linux/arm64 binary; else build if Go is present.
if [ -x "$ROOT/core/pimywa" ]; then
  install -m755 "$ROOT/core/pimywa" "$INSTALL_DIR/pimywa"
elif command -v go >/dev/null 2>&1; then
  ( cd "$ROOT/core" && CGO_ENABLED=0 go build -ldflags "-s -w" -o "$INSTALL_DIR/pimywa" . )
else
  echo "ERROR: no $ROOT/core/pimywa binary and no Go toolchain to build it" >&2
  exit 1
fi

cp -r "$ROOT/adapters/display/." "$INSTALL_DIR/display/"
cp -r "$ROOT/adapters/power/."   "$INSTALL_DIR/power/"
[ -f "$ROOT/core/router.example.json" ] && [ ! -f "$INSTALL_DIR/router.json" ] && \
  cp "$ROOT/core/router.example.json" "$INSTALL_DIR/router.json" || true

# env file: create if missing, else MIGRATE (append any missing keys). This
# preserves a user-set PIMYWA_API_KEY while still adding new keys on upgrade.
ENV_FILE="$INSTALL_DIR/pimywa.env"
DISPLAY_VAL=$([ "$WITH_DISPLAY" -eq 1 ] && echo epaper-waveshare || echo none)
POWER_VAL=$([ "$WITH_POWER" -eq 1 ] && echo cw2015-i2c || echo none)
touch "$ENV_FILE"
set_default() {  # KEY VALUE -> add KEY=VALUE only if KEY is absent
  grep -q "^$1=" "$ENV_FILE" || echo "$1=$2" >> "$ENV_FILE"
}
set_default PIMYWA_DB         "$DATA_DIR/pimywa.db"
set_default PIMYWA_SESSION_DB "$DATA_DIR/wa.db"
set_default PIMYWA_STATUS     "/run/pimywa/status.json"
set_default PIMYWA_ROUTER     "$INSTALL_DIR/router.json"
set_default PIMYWA_API_ADDR   ":8080"
set_default PIMYWA_MCP_ADDR   ":8081"
set_default PIMYWA_API_KEY    ""
set_default PIMYWA_NAME        "Piumy"
set_default PIMYWA_HOSTNAME    "$(hostname).local"
set_default PIMYWA_GATEWAY     "none"
set_default PIMYWA_DISPLAY     "$DISPLAY_VAL"
set_default PIMYWA_WIFI_IFACE  "wlan0"
set_default PIMYWA_ANIM_FAST_SEC "2.0"
set_default PIMYWA_ANIM_SLOW_SEC "18.0"
set_default PIMYWA_ANIM_RAMP_SEC "60.0"
set_default PIMYWA_EPAPER_GHOST_PARTIALS "0"
set_default PIMYWA_EPAPER_FULL_REFRESH "0"
set_default PIMYWA_POWER_BACKEND "$POWER_VAL"
set_default PIMYWA_POWER_I2C_BUS  "1"
set_default PIMYWA_POWER_I2C_ADDR "0x62"
set_default PIMYWA_POWER_INTERVAL "30"
# battery.json sidecar (single-writer fix): the power
# service writes ONLY this file; the core (the sole status.json writer)
# reads it back in on its own heartbeat. Same key, same default path, read
# by both the Python service and the Go core via this one shared env file
# -- they can never disagree on where it lives.
set_default PIMYWA_BATTERY_FILE      "/run/pimywa/battery.json"
set_default PIMYWA_BATTERY_MAX_AGE   "120s"
set_default PIMYWA_STATUS_HEARTBEAT  "15s"
# face.json sidecar: same single-writer pattern as
# PIMYWA_BATTERY_FILE above -- the display service writes the live kaomoji
# face here, the core merges it into status.json for the dashboard.
set_default PIMYWA_FACE_FILE         "/run/pimywa/face.json"
set_default PIMYWA_FACE_MAX_AGE      "120s"
# Adaptive time-remaining estimator (milestone D) --
# capacity/draw are only a cold-start guess (Pi Zero 2 W + panel); the real
# learned discharge rate replaces them once enough samples exist. The learn
# file is DURABLE (SD, not tmpfs) on purpose -- it must survive a restart --
# but is written rarely (only at SoC milestones), so this stays cheap.
set_default PIMYWA_BATTERY_CAPACITY_MAH  "1200"
set_default PIMYWA_BATTERY_DRAW_MA       "180"
set_default PIMYWA_BATTERY_LEARN_FILE    "$DATA_DIR/battery_learn.json"
set_default PIMYWA_BATTERY_LEARN_WINDOW_MIN "15"
set_default PIMYWA_BATTERY_LEARN_ALPHA   "0.3"
# Discharge/charge trace log (item A) -- same shared-env
# pattern as PIMYWA_BATTERY_FILE above: the Python power service WRITES this
# CSV, the Go core only READS it (GET /api/battery/log for the dashboard).
# Durable SD on purpose (a full discharge cycle can outlive a restart).
set_default PIMYWA_BATTERY_LOG_FILE      "$DATA_DIR/battery_log.csv"
set_default PIMYWA_BATTERY_LOG_SEC       "60"
set_default PIMYWA_BATTERY_LOG_ENABLED   "1"
set_default PIMYWA_BATTERY_LOG_MAX_BYTES "2097152"
# Dashboard (web UI on :80). Leave PIMYWA_DASH_PASS empty on first install so
# a secure random password is generated at startup and logged once via journalctl.
set_default PIMYWA_DASH         "on"
set_default PIMYWA_DASH_ADDR    ":80"
set_default PIMYWA_DASH_USER    "admin"
set_default PIMYWA_DASH_PASS    ""
set_default PIMYWA_DASH_PASS_HASH ""

# python deps not packaged by apt (qrcode is apt above; ensure Pillow present)
python3 -c 'import PIL' 2>/dev/null || pip3 install --break-system-packages "Pillow>=10" || true

# MCP auth token: the MCP endpoint (:8081) is
# fail-closed -- it rejects every request without a PIMYWA_MCP_KEY, so the
# install is incomplete (MCP unusable) without one. `auth setup` is
# idempotent (a no-op if a key is already in $ENV_FILE, e.g. an upgrade),
# so it's safe to always run here. Prints the token ONCE for the owner to
# save; `pimywa auth rotate` regenerates it later if lost/compromised.
"$INSTALL_DIR/pimywa" auth setup --env-file "$ENV_FILE"

chown -R "$RUN_USER:$RUN_USER" "$INSTALL_DIR" "$DATA_DIR"

# ---- 8. systemd services ----------------------------------------------------
log "Installing systemd services"
inst_unit() {  # tweak User + add RuntimeDirectory, then install
  local src="$1" dst="/etc/systemd/system/$(basename "$1")"
  sed "s/^User=.*/User=$RUN_USER/" "$src" > "$dst"
  grep -q '^RuntimeDirectory=' "$dst" || \
    sed -i '/^\[Service\]/a RuntimeDirectory=pimywa\nRuntimeDirectoryPreserve=yes' "$dst"
}
inst_unit "$HERE/pimywa-core.service"
systemctl daemon-reload
systemctl enable pimywa-core
if [ "$WITH_DISPLAY" -eq 1 ]; then
  inst_unit "$HERE/pimywa-display.service"
  systemctl enable pimywa-display
fi
if [ "$WITH_POWER" -eq 1 ]; then
  inst_unit "$HERE/pimywa-power.service"
  systemctl enable pimywa-power
fi

# ---- 9. avahi (pimywa.local) ------------------------------------------------
systemctl enable --now avahi-daemon 2>/dev/null || true

# ---- 10. optional read-only overlay root ------------------------------------
if [ "$DO_OVERLAY" -eq 1 ]; then
  log "Enabling overlay root (read-only). Data stays on $DATA_DIR? NOTE: overlay makes / read-only; ensure data is on a writable mount."
  raspi-config nonint enable_overlayfs || echo "overlayfs: enable manually via raspi-config if this failed"
fi

log "Done. Services enabled: pimywa-core$([ "$WITH_DISPLAY" -eq 1 ] && echo ' + pimywa-display')$([ "$WITH_POWER" -eq 1 ] && echo ' + pimywa-power')"
echo "Reachable at: http://$(hostname).local/  (dashboard)  ·  :8080 (REST)  ·  :8081 (MCP)"
echo "Dashboard first-run password: journalctl -u pimywa-core | grep 'random password'"
echo "MCP auth token: shown above (once) -- lost it? sudo $INSTALL_DIR/pimywa auth rotate --env-file $ENV_FILE"
echo "Link WhatsApp: set PIMYWA_GATEWAY=whatsmeow in $INSTALL_DIR/pimywa.env, then restart pimywa-core and scan the QR at http://$(hostname).local/"

if [ "$DO_REBOOT" -eq 1 ]; then
  log "Rebooting"
  systemctl reboot
else
  echo
  echo "Reboot recommended to apply SPI/I2C/watchdog: sudo reboot"
fi
