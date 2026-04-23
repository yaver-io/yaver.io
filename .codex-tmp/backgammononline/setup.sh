#!/bin/bash
# setup.sh — One-time project setup script
# Run this from the BackgammonOnline directory: bash setup.sh

set -e
cd "$(dirname "$0")"

echo ""
echo "🎲 Backgammon Platform — Setup"
echo "================================"
echo ""

# 1. Ensure Node.js is available via Homebrew
if ! command -v node &> /dev/null; then
  echo "📦 Node.js not found. Installing via Homebrew..."
  if ! command -v brew &> /dev/null; then
    echo "❌ Homebrew not found. Please install it from https://brew.sh and re-run."
    exit 1
  fi
  # Accept Xcode license (requires sudo)
  sudo xcodebuild -license accept 2>/dev/null || true
  brew install node
  echo "✅ Node.js installed: $(node --version)"
else
  echo "✅ Node.js found: $(node --version)"
fi

# 2. Install root-level turbo + deps
echo ""
echo "📦 Installing root dependencies..."
npm install

# 3. Install web app dependencies
echo ""
echo "📦 Installing apps/web dependencies..."
npm install --workspace=apps/web

# 4. Install game engine dependencies
echo ""
echo "📦 Installing packages/game-engine dependencies..."
npm install --workspace=packages/game-engine

# 5. Done
echo ""
echo "✅ Setup complete!"
echo ""
echo "🚀 To start the dev server:"
echo "   cd apps/web && npx vite"
echo "   — OR —"
echo "   npm run dev (from root, uses Turborepo)"
echo ""
echo "🌐 Then open: http://localhost:5173"
echo ""
echo "🔮 To set up Convex backend (Phase 2):"
echo "   npx convex dev"
echo ""
