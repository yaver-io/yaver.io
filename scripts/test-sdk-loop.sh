#!/bin/bash
set -e

# ─── SDK → CLI → Agent → Fix Loop Test ───
# Tests the full feedback SDK loop:
# 1. Start yaver serve in demo app directory
# 2. Send a task (like SDK would) via HTTP
# 3. Wait for agent to fix the code
# 4. Verify the fix was applied
#
# Requires: Go, Node.js, ANTHROPIC_API_KEY or OPENAI_API_KEY

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
DEMO_DIR="$ROOT_DIR/demo/AcmeStore"
CLI_DIR="$ROOT_DIR/desktop/agent"
YAVER_BIN="$CLI_DIR/yaver"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓${NC} $1"; }
fail() { echo -e "${RED}✗${NC} $1"; exit 1; }
info() { echo -e "${YELLOW}→${NC} $1"; }

cleanup() {
    info "Cleaning up..."
    $YAVER_BIN stop 2>/dev/null || true
    # Revert LoginForm to buggy version
    cd "$DEMO_DIR"
    git checkout -- src/components/LoginForm.tsx 2>/dev/null || true
}
trap cleanup EXIT

# ─── Step 0: Prerequisites ───
info "Checking prerequisites..."

if [ ! -f "$YAVER_BIN" ]; then
    info "Building CLI..."
    cd "$CLI_DIR" && go build -o yaver . || fail "CLI build failed"
fi
pass "CLI binary ready"

if [ ! -d "$DEMO_DIR/node_modules" ]; then
    info "Installing demo app dependencies..."
    cd "$DEMO_DIR" && npm install --legacy-peer-deps || fail "npm install failed"
fi
pass "Demo app dependencies ready"

# ─── Step 1: Verify the bug exists ───
info "Verifying LoginForm has the bug..."
cd "$DEMO_DIR"

if grep -q "TODO.*validation\|TODO.*email" src/components/LoginForm.tsx; then
    pass "LoginForm has TODO comment (no validation)"
else
    # Revert to buggy version
    cat > src/components/LoginForm.tsx << 'BUGGY_EOF'
import React, { useState } from 'react';
import { View, Text, TextInput, TouchableOpacity, StyleSheet, ActivityIndicator } from 'react-native';

interface LoginFormProps {
  onLogin: (email: string, password: string) => Promise<void>;
}

export function LoginForm({ onLogin }: LoginFormProps) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async () => {
    setError(null);
    setLoading(true);

    // TODO: add input validation — email format, empty fields
    try {
      await onLogin(email, password);
    } catch (err: any) {
      setError(err.message || 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <View style={styles.container}>
      <Text style={styles.title}>Welcome back</Text>
      <TextInput style={styles.input} value={email} onChangeText={setEmail} placeholder="you@example.com" placeholderTextColor="#bbb" keyboardType="email-address" autoCapitalize="none" />
      <TextInput style={styles.input} value={password} onChangeText={setPassword} placeholder="Password" placeholderTextColor="#bbb" secureTextEntry />
      {error && <Text style={styles.error}>{error}</Text>}
      <TouchableOpacity style={styles.button} onPress={handleSubmit} disabled={loading}>
        {loading ? <ActivityIndicator color="#fff" /> : <Text style={styles.buttonText}>Sign In</Text>}
      </TouchableOpacity>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { padding: 24 },
  title: { fontSize: 28, fontWeight: '700', color: '#111', marginBottom: 32 },
  input: { backgroundColor: '#f5f5f5', borderRadius: 12, paddingHorizontal: 16, paddingVertical: 14, fontSize: 16, color: '#111', marginBottom: 12 },
  error: { color: '#ef4444', fontSize: 13, marginBottom: 8 },
  button: { backgroundColor: '#111', borderRadius: 12, paddingVertical: 16, alignItems: 'center', marginTop: 16 },
  buttonText: { color: '#fff', fontSize: 16, fontWeight: '700' },
});
BUGGY_EOF
    pass "LoginForm reverted to buggy version"
fi

# Confirm no validation exists
if grep -q "email.*required\|emailRegex\|email.*format\|\.test(email" src/components/LoginForm.tsx; then
    fail "LoginForm already has validation — revert first"
fi
pass "Confirmed: no email validation in LoginForm"

# ─── Step 2: Start yaver serve ───
info "Starting yaver serve..."
$YAVER_BIN stop 2>/dev/null || true
sleep 1

cd "$DEMO_DIR"
$YAVER_BIN serve &
sleep 3

# Get the port
YAVER_PORT=18080
if ! curl -sf "http://localhost:$YAVER_PORT/health" > /dev/null 2>&1; then
    fail "yaver serve not responding on port $YAVER_PORT"
fi
pass "yaver serve running on port $YAVER_PORT"

# ─── Step 3: Get auth token ───
info "Getting auth token..."
TOKEN=$(cat ~/.config/yaver/config.json 2>/dev/null | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4)
if [ -z "$TOKEN" ]; then
    # Try without auth for local testing
    TOKEN="test-token"
    info "No auth token found, using test mode"
fi

# ─── Step 4: Send task (simulating SDK) ───
info "Sending fix task (simulating Feedback SDK)..."

RESPONSE=$(curl -sf -X POST "http://localhost:$YAVER_PORT/tasks" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "title": "Fix the login email validation in src/components/LoginForm.tsx. Add email format validation and empty field checks. The handleSubmit function has a TODO comment where validation should go.",
        "source": "feedback-sdk",
        "description": "[SDK Test] Automated test: fix missing email validation"
    }' 2>&1)

TASK_ID=$(echo "$RESPONSE" | grep -o '"taskId":"[^"]*"' | cut -d'"' -f4)
if [ -z "$TASK_ID" ]; then
    TASK_ID=$(echo "$RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
fi

if [ -z "$TASK_ID" ]; then
    echo "Response: $RESPONSE"
    fail "Failed to create task"
fi
pass "Task created: $TASK_ID"

# ─── Step 5: Poll for completion ───
info "Waiting for agent to fix the code..."
MAX_WAIT=120  # 2 minutes max
ELAPSED=0

while [ $ELAPSED -lt $MAX_WAIT ]; do
    STATUS_RESP=$(curl -sf "http://localhost:$YAVER_PORT/tasks/$TASK_ID" \
        -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo '{}')

    STATUS=$(echo "$STATUS_RESP" | grep -o '"status":"[^"]*"' | head -1 | cut -d'"' -f4)

    if [ "$STATUS" = "completed" ] || [ "$STATUS" = "finished" ]; then
        pass "Task completed in ${ELAPSED}s"
        break
    elif [ "$STATUS" = "failed" ] || [ "$STATUS" = "stopped" ]; then
        echo "Response: $STATUS_RESP"
        fail "Task failed with status: $STATUS"
    fi

    sleep 3
    ELAPSED=$((ELAPSED + 3))
    printf "."
done
echo ""

if [ $ELAPSED -ge $MAX_WAIT ]; then
    fail "Task timed out after ${MAX_WAIT}s (status: $STATUS)"
fi

# ─── Step 6: Verify the fix ───
info "Verifying fix was applied..."

# Check that validation was added
if grep -q "email.*required\|Email is required\|email.*trim\|emailRegex\|\.test(email\|email.*format" src/components/LoginForm.tsx; then
    pass "Email validation added to LoginForm"
else
    echo "Current LoginForm content:"
    head -40 src/components/LoginForm.tsx
    fail "No email validation found in LoginForm after fix"
fi

# Check that the TODO comment was removed or replaced
if grep -q "TODO.*add input validation" src/components/LoginForm.tsx; then
    info "Warning: TODO comment still present (non-critical)"
else
    pass "TODO comment removed"
fi

# ─── Step 7: Summary ───
echo ""
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo -e "${GREEN}  SDK → CLI → Agent → Fix Loop: PASSED ${NC}"
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo ""
echo "  Task ID: $TASK_ID"
echo "  Time:    ${ELAPSED}s"
echo "  File:    src/components/LoginForm.tsx"
echo "  Fix:     Email validation added"
echo ""
