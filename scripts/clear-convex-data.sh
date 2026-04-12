#!/bin/bash
# Clear all Convex tables except platformConfig.
# Used for fresh-start testing. Run from repo root.
set -e

CONVEX_SITE_URL="https://shocking-echidna-394.eu-west-1.convex.site"

# Tables to clear (everything except platformConfig)
TABLES=(
  users
  passwordResets
  pendingAuth
  sessions
  devices
  downloads
  developerSurveys
  authLogs
  userSettings
  aiRunners
  aiModels
  deviceMetrics
  deviceEvents
  runnerUsage
  dailyTaskCounts
  developerLogs
  deviceCodes
  subscriptions
  managedRelays
  teams
  teamMembers
  cloudMachines
  guestInvitations
  guestAccess
  guestUsage
  sdkTokens
  securityEvents
  mobileStreamLogs
)

echo "Clearing Convex tables (keeping platformConfig)..."

for table in "${TABLES[@]}"; do
  echo -n "  $table: "
  # May need multiple passes for tables with >500 rows
  total=0
  while true; do
    result=$(cd backend && npx convex run admin:clearTable "{\"table\": \"$table\"}" 2>/dev/null || echo '{"deleted":0}')
    deleted=$(echo "$result" | grep -o '"deleted":[0-9]*' | cut -d: -f2)
    if [ -z "$deleted" ] || [ "$deleted" = "0" ]; then
      break
    fi
    total=$((total + deleted))
    hasMore=$(echo "$result" | grep -o '"hasMore":true')
    if [ -z "$hasMore" ]; then
      break
    fi
  done
  echo "${total} rows deleted"
done

echo ""
echo "Done. Only platformConfig remains."
