#!/bin/sh
# Fault injection for step 5: waits (through docker) until r3 runs the corrected candidate,
# then takes r1's link to r3 down out of band with a separate pleiades call, while
# runbooks/change_r3.yaml is still verifying the change. Run it beside that run.
# Usage: ../lab/inject-link-failure.sh   (from pleiades/, the project directory, PLEIADES_KNOWN_HOSTS set)
set -e
i=0
until docker exec clab-frrlab-r3 vtysh -c 'show running-config' 2>/dev/null | grep -q 'lan-10.3.3.0 to h2'; do
  i=$((i+1)); [ $i -gt 240 ] && { echo "r3 never ran the candidate" >&2; exit 1; }
  sleep 0.5
done
echo "$(date +%T.%N) r3 now runs the candidate; taking r1 eth2 down"
pleiades adhoc r1 net.frr.interface name=eth2 enabled:=false --json > ../captures/14-injected-linkdown.json
echo "$(date +%T.%N) r1 eth2 shut down (run $(jq -r .run_id ../captures/14-injected-linkdown.json))"
