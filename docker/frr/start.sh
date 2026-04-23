#!/bin/bash
set -euo pipefail

# frrinit.sh creates /var/run/frr (→ /run/frr) itself, but pre-create with
# correct ownership so the volume mount is writable by the frr user.
mkdir -p /run/frr
chown frr:frrvty /run/frr
chmod 755 /run/frr

# Start all daemons listed as enabled in /etc/frr/daemons via watchfrr.
/usr/lib/frr/frrinit.sh start

echo "Waiting for mgmtd_fe.sock..."
for i in $(seq 1 60); do
    [ -S /run/frr/mgmtd_fe.sock ] && { echo "FRR ready"; break; }
    sleep 1
done
[ -S /run/frr/mgmtd_fe.sock ] || { echo "Timeout waiting for mgmtd_fe.sock"; exit 1; }

exec sleep infinity
