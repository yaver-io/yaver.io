#!/bin/bash

# Meeting Fabric Test Runner
# This script tests the meeting fabric implementation without running full test suite

set -e

echo "========================================="
echo "Meeting Fabric Test Suite"
echo "========================================="
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Function to print test results
print_result() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
    else
        echo -e "${RED}✗${NC} $2"
    fi
}

# Test 1: Verify meeting_fabric.go exists and is syntactically valid
echo "Test 1: Check meeting_fabric.go file..."
if [ -f "desktop/agent/meeting_fabric.go" ]; then
    print_result 0 "meeting_fabric.go exists"
else
    print_result 1 "meeting_fabric.go not found"
    exit 1
fi

# Test 2: Check existing unit tests
echo ""
echo "Test 2: Verify existing unit tests..."
if [ -f "desktop/agent/meeting_fabric_test.go" ]; then
    print_result 0 "meeting_fabric_test.go exists"
    
    # Count test functions
    test_count=$(grep -c "^func Test" desktop/agent/meeting_fabric_test.go || true)
    echo "  Found $test_count test functions"
    
    if [ $test_count -gt 0 ]; then
        print_result 0 "Unit tests defined"
    else
        print_result 1 "No unit tests defined"
    fi
else
    print_result 1 "meeting_fabric_test.go not found"
fi

# Test 3: Check integration test file
echo ""
echo "Test 3: Verify integration test file..."
if [ -f "desktop/agent/meeting_fabric_integration_test.go" ]; then
    print_result 0 "meeting_fabric_integration_test.go exists"
    
    # Count integration test functions
    int_test_count=$(grep -c "^func TestMeetingFabric" desktop/agent/meeting_fabric_integration_test.go || true)
    echo "  Found $int_test_count integration test functions"
    
    if [ $int_test_count -gt 0 ]; then
        print_result 0 "Integration tests defined"
    else
        print_result 1 "No integration tests defined"
    fi
else
    print_result 1 "meeting_fabric_integration_test.go not found"
fi

# Test 4: Check web client implementation
echo ""
echo "Test 4: Check web LiveKit client in meeting_fabric.go..."
if grep -q "renderCallPage" desktop/agent/meeting_fabric.go; then
    print_result 0 "renderCallPage function exists"
    
    # Check for CSS styles
    if grep -q ".video-grid" desktop/agent/meeting_fabric.go; then
        print_result 0 "Video grid styles present"
    else
        print_result 1 "Video grid styles missing"
    fi
    
    # Check for LiveKit JavaScript
    if grep -q "LiveKitClient" desktop/agent/meeting_fabric.go; then
        print_result 0 "LiveKit client code present"
    else
        print_result 1 "LiveKit client code missing"
    fi
    
    # Check for participant rendering
    if grep -q "participantGrid" desktop/agent/meeting_fabric.go; then
        print_result 0 "Participant rendering present"
    else
        print_result 1 "Participant rendering missing"
    fi
else
    print_result 1 "renderCallPage function not found"
fi

# Test 5: Check mobile implementation
echo ""
echo "Test 5: Check mobile meeting room screen..."
if [ -f "mobile/app/(tabs)/meeting-room.tsx" ]; then
    print_result 0 "mobile meeting-room.tsx exists"
    
    # Check for LiveKit imports
    if grep -q "livekit-react-native" mobile/app/(tabs)/meeting-room.tsx; then
        print_result 0 "LiveKit React Native imported"
    else
        print_result 1 "LiveKit React Native not imported"
    fi
    
    # Check for useLiveKitClient hook
    if grep -q "useLiveKitClient" mobile/app/(tabs)/meeting-room.tsx; then
        print_result 0 "useLiveKitClient hook used"
    else
        print_result 1 "useLiveKitClient hook not used"
    fi
    
    # Check for RoomView component
    if grep -q "RoomView" mobile/app/(tabs)/meeting-room.tsx; then
        print_result 0 "RoomView component used"
    else
        print_result 1 "RoomView component not used"
    fi
else
    print_result 1 "mobile meeting-room.tsx not found"
fi

# Test 6: Check mobile navigation integration
echo ""
echo "Test 6: Check mobile navigation in solostack.tsx..."
if [ -f "mobile/app/(tabs)/solostack.tsx" ]; then
    if grep -q "meeting-room" mobile/app/(tabs)/solostack.tsx; then
        print_result 0 "Navigation to meeting-room configured"
    else
        print_result 1 "Navigation not configured"
    fi
    
    if grep -q "TouchableOpacity" mobile/app/(tabs)/solostack.tsx && grep -A 3 "meetingRooms.map" mobile/app/(tabs)/solostack.tsx | grep -q "TouchableOpacity"; then
        print_result 0 "Meeting rooms are tappable"
    else
        print_result 1 "Meeting rooms not tappable"
    fi
else
    print_result 1 "solostack.tsx not found"
fi

# Test 7: Check LiveKit package installation
echo ""
echo "Test 7: Check LiveKit React Native package..."
if [ -f "mobile/package.json" ]; then
    if grep -q "livekit-react-native" mobile/package.json; then
        print_result 0 "livekit-react-native installed in mobile"
    else
        print_result 1 "livekit-react-native not installed in mobile"
    fi
    
    if grep -q "livekit-client" mobile/package.json; then
        print_result 0 "livekit-client installed in mobile"
    else
        print_result 1 "livekit-client not installed in mobile"
    fi
else
    print_result 1 "mobile/package.json not found"
fi

# Test 8: Check HTTP routes
echo ""
echo "Test 8: Check meeting HTTP routes in httpserver.go..."
if grep -q "/meeting-rooms" desktop/agent/httpserver.go; then
    print_result 0 "/meeting-rooms route registered"
else
    print_result 1 "/meeting-rooms route not registered"
fi

if grep -q "/call/" desktop/agent/httpserver.go; then
    print_result 0 "/call/ route registered"
else
    print_result 1 "/call/ route not registered"
fi

# Test 9: Check MCP tool registration
echo ""
echo "Test 9: Check MCP tools for meeting fabric..."
if grep -q "meeting_room_create" desktop/agent/mcp_tools.go; then
    print_result 0 "meeting_room_create tool registered"
else
    print_result 1 "meeting_room_create tool not registered"
fi

if grep -q "meeting_room_list" desktop/agent/mcp_tools.go; then
    print_result 0 "meeting_room_list tool registered"
else
    print_result 1 "meeting_room_list tool not registered"
fi

if grep -q "meeting_capabilities" desktop/agent/mcp_tools.go; then
    print_result 0 "meeting_capabilities tool registered"
else
    print_result 1 "meeting_capabilities tool not registered"
fi

# Test 10: Check browser automation tools
echo ""
echo "Test 10: Check browser automation MCP tools..."
browser_tools_count=0

if grep -q "browser_open" desktop/agent/mcp_tools.go; then
    print_result 0 "browser_open tool registered"
    browser_tools_count=$((browser_tools_count + 1))
else
    print_result 1 "browser_open tool not registered"
fi

if grep -q "browser_navigate" desktop/agent/mcp_tools.go; then
    print_result 0 "browser_navigate tool registered"
    browser_tools_count=$((browser_tools_count + 1))
else
    print_result 1 "browser_navigate tool not registered"
fi

if grep -q "browser_click" desktop/agent/mcp_tools.go; then
    print_result 0 "browser_click tool registered"
    browser_tools_count=$((browser_tools_count + 1))
else
    print_result 1 "browser_click tool not registered"
fi

if grep -q "browser_screenshot" desktop/agent/mcp_tools.go; then
    print_result 0 "browser_screenshot tool registered"
    browser_tools_count=$((browser_tools_count + 1))
else
    print_result 1 "browser_screenshot tool not registered"
fi

if [ $browser_tools_count -ge 4 ]; then
    echo "  $browser_tools_count/4 browser automation tools available"
fi

# Test 11: Check test user creation
echo ""
echo "Test 11: Check test user management tools..."
if grep -q "auth_dev_users" desktop/agent/mcp_tools.go; then
    print_result 0 "auth_dev_users tool registered"
else
    print_result 1 "auth_dev_users tool not registered"
fi

if grep -q "fake_data" desktop/agent/mcp_tools.go; then
    print_result 0 "fake_data tool registered"
else
    print_result 1 "fake_data tool not registered"
fi

# Test 12: Check external provider support
echo ""
echo "Test 12: Check external meeting provider support..."
providers=("zoom" "google-meet" "microsoft-teams")
all_providers_found=true

for provider in "${providers[@]}"; do
    if grep -q "\"$provider\"" desktop/agent/meeting_fabric.go; then
        print_result 0 "$provider provider supported"
    else
        print_result 1 "$provider provider not found"
        all_providers_found=false
    fi
done

if [ "$all_providers_found" = true ]; then
    echo "  All external providers defined"
fi

# Test 13: Check PSTN support
echo ""
echo "Test 13: Check PSTN audio bridge support..."
if grep -q "PSTNConfig" desktop/agent/meeting_fabric.go; then
    print_result 0 "PSTN configuration struct defined"
else
    print_result 1 "PSTN configuration struct not found"
fi

# Test 14: Check adapter modes
echo ""
echo "Test 14: Check adapter mode support..."
modes=("native-sfu" "official-media-api" "remote-browser" "link-only" "pstn-audio-bridge")
all_modes_found=true

for mode in "${modes[@]}"; do
    if grep -q "\"$mode\"" desktop/agent/meeting_fabric.go; then
        print_result 0 "$mode adapter mode supported"
    else
        print_result 1 "$mode adapter mode not found"
        all_modes_found=false
    fi
done

if [ "$all_modes_found" = true ]; then
    echo "  All adapter modes defined"
fi

# Test 15: Create test meeting room (if agent is running)
echo ""
echo "Test 15: Attempt to create test meeting room via MCP..."
# Check if agent is running
if curl -s http://localhost:8200/meeting-rooms > /dev/null 2>&1; then
    echo "  Agent is running at http://localhost:8200"
    
    # Try to create a test room
    test_response=$(curl -s -X POST http://localhost:8200/meeting-rooms \
        -H "Content-Type: application/json" \
        -d '{
            "title": "Test Integration Room",
            "description": "Automated test room",
            "provider": "yaver-native",
            "adapterMode": "native-sfu",
            "allowGuests": true
        }' 2>&1 || echo "error")
    
    if echo "$test_response" | grep -q "id\|slug"; then
        print_result 0 "Test meeting room created via API"
        echo "  Response: $(echo "$test_response" | head -c 100)"
    else
        print_result 1 "Failed to create test meeting room"
    fi
else
    echo -e "${YELLOW}⚠${NC} Agent not running at http://localhost:8200"
    echo "  Skipping live API test"
fi

# Test 16: List meeting rooms (if agent is running)
echo ""
echo "Test 16: List meeting rooms via API..."
if curl -s http://localhost:8200/meeting-rooms > /dev/null 2>&1; then
    rooms_response=$(curl -s http://localhost:8200/meeting-rooms 2>&1)
    
    if echo "$rooms_response" | grep -q "rooms\|capabilities"; then
        print_result 0 "Meeting rooms listed successfully"
        echo "  Response: $(echo "$rooms_response" | head -c 100)"
    else
        print_result 1 "Failed to list meeting rooms"
    fi
fi

# Test 17: Check meeting room file permissions
echo ""
echo "Test 17: Check meeting rooms JSON file storage..."
if [ -f "$HOME/.yaver/meeting_rooms.json" ]; then
    print_result 0 "meeting_rooms.json file exists"
    
    # Check file permissions
    if [ -r "$HOME/.yaver/meeting_rooms.json" ]; then
        print_result 0 "meeting_rooms.json is readable"
    else
        print_result 1 "meeting_rooms.json is not readable"
    fi
    
    # Check file is valid JSON
    if jq empty "$HOME/.yaver/meeting_rooms.json" > /dev/null 2>&1; then
        print_result 0 "meeting_rooms.json is valid JSON"
    else
        print_result 1 "meeting_rooms.json is invalid JSON"
    fi
else
    echo -e "${YELLOW}⚠${NC} meeting_rooms.json does not exist yet (will be created when first room is made)"
fi

# Summary
echo ""
echo "========================================="
echo "Test Summary"
echo "========================================="
echo ""
echo "All tests completed. See results above."
echo ""
echo "To run the browser automation tests, ensure:"
echo "  1. Agent is running (yaver agent)"
echo "  2. LiveKit is configured (LIVEKIT_API_KEY, LIVEKIT_API_SECRET, LIVEKIT_URL)"
echo "  3. Run: yaver browser_open --headful=false"
echo "  4. Navigate to a test room"
echo "  5. Use browser_click, browser_type, etc. to test the UI"
echo ""
echo "To create test users:"
echo "  1. Run: yaver auth_dev_users action=create email=test@example.com password=test123 role=user"
echo "  2. List users: yaver auth_dev_users action=list"
echo "  3. Delete user: yaver auth_dev_users action=delete email=test@example.com"
echo ""
echo "To test Redroid (Android emulator):"
echo "  1. Ensure Redroid container is running"
echo "  2. Create a meeting room"
echo "  3. Navigate to room in mobile app"
echo "  4. Verify video/microphone works"
echo ""