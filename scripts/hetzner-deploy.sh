#!/bin/bash

# Hetzner Project Deployment Script for Yaver, Talos, OCPP
# Usage: ./scripts/hetzner-deploy.sh [project] [environment] [action]

set -e

PROJECT=${1:-"yaver"}
ENVIRONMENT=${2:-"development"}
ACTION=${3:-"deploy"}
HETZNER_SERVER=${4:-"selected-machine"}

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Project configurations
declare -A PROJECT_PATHS=(
    ["yaver"]="/Users/kivanccakmak/Workspace/yaver.io"
    ["talos"]="/Users/kivanccakmak/Workspace/talos"
    ["ocpp"]="/Users/kivanccakmak/Workspace/ocpp"
)

declare -A PROJECT_REPOS=(
    ["yaver"]="git@github.com:kivanccakmak/yaver.io.git"
    ["talos"]="git@github.com:kivanccakmak/talos.git"
    ["ocpp"]="git@github.com:kivanccakmak/ocpp.git"
)

declare -A PROJECT_PORTS=(
    ["yaver"]="18080"
    ["talos"]="3000"
    ["ocpp"]="8080"
)

# Validate project
if [[ -z "${PROJECT_PATHS[$PROJECT]}" ]]; then
    log_error "Unknown project: $PROJECT"
    log_info "Available projects: ${!PROJECT_PATHS[@]}"
    exit 1
fi

PROJECT_PATH="${PROJECT_PATHS[$PROJECT]}"
PROJECT_REPO="${PROJECT_REPOS[$PROJECT]}"
PROJECT_PORT="${PROJECT_PORTS[$PROJECT]}"

log_info "Project: $PROJECT"
log_info "Environment: $ENVIRONMENT"
log_info "Action: $ACTION"
log_info "Target Server: $HETZNER_SERVER"

# Check if project exists locally
if [[ ! -d "$PROJECT_PATH" ]]; then
    log_warning "Project directory not found locally: $PROJECT_PATH"
    log_info "Cloning project from: $PROJECT_REPO"
    git clone "$PROJECT_REPO" "$PROJECT_PATH"
fi

cd "$PROJECT_PATH"

# Check git status
log_info "Checking git status..."
git_status=$(git status --porcelain)
if [[ -n "$git_status" ]]; then
    log_warning "You have uncommitted changes:"
    echo "$git_status"
    read -p "Do you want to commit these changes before deploying? (y/n): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        log_info "Committing changes..."
        git add .
        git commit -m "Auto-commit before Hetzner deployment"
    fi
fi

# Hetzner server functions
check_hetzner_connection() {
    log_info "Checking connection to Hetzner server: $HETZNER_SERVER"
    if yaver ping "$HETZNER_SERVER" > /dev/null 2>&1; then
        log_success "Connected to $HETZNER_SERVER"
        return 0
    else
        log_error "Cannot connect to $HETZNER_SERVER"
        log_info "Make sure the server is running and accessible via Yaver"
        return 1
    fi
}

deploy_to_hetzner() {
    log_info "Starting deployment to Hetzner..."

    # 1. Push latest code to remote
    log_info "Pushing latest code to remote repository..."
    git push origin $(git branch --show-current)

    # 2. Setup remote directories
    log_info "Setting up remote directories..."
    yaver exec --device "$HETZNER_SERVER" -- mkdir -p "/workspace/$PROJECT"
    yaver exec --device "$HETZNER_SERVER" -- mkdir -p "/workspace/$PROJECT/logs"
    yaver exec --device "$HETZNER_SERVER" -- mkdir -p "/workspace/$PROJECT/.yaver"

    # 3. Clone/Update project on Hetzner
    log_info "Updating project on Hetzner server..."
    if yaver exec --device "$HETZNER_SERVER" -- test -d "/workspace/$PROJECT/.git"; then
        yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && git pull origin $(git branch --show-current)"
    else
        yaver exec --device "$HETZNER_SERVER" -- git clone "$PROJECT_REPO" "/workspace/$PROJECT"
    fi

    # 4. Install dependencies
    log_info "Installing dependencies..."
    case "$PROJECT" in
        "yaver")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm install --legacy-peer-deps"
            ;;
        "talos")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm install"
            ;;
        "ocpp")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm install"
            ;;
    esac

    # 5. Build project
    log_info "Building project..."
    case "$PROJECT" in
        "yaver")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/cli && npm run build"
            ;;
        "talos")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/web && npm run build"
            ;;
        "ocpp")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm run build"
            ;;
    esac

    # 6. Deploy based on environment
    case "$ENVIRONMENT" in
        "development")
            deploy_development
            ;;
        "staging")
            deploy_staging
            ;;
        "production")
            deploy_production
            ;;
        *)
            log_error "Unknown environment: $ENVIRONMENT"
            exit 1
            ;;
    esac

    log_success "Deployment completed successfully!"
}

deploy_development() {
    log_info "Deploying to development environment..."

    # Start development server
    case "$PROJECT" in
        "yaver")
            log_info "Starting Yaver agent in development mode..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/desktop/agent && go run . serve --port=$PROJECT_PORT"
            ;;
        "talos")
            log_info "Starting Talos web in development mode..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/web && npm run dev --port=$PROJECT_PORT"
            ;;
        "ocpp")
            log_info "Starting OCPP server in development mode..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm run dev --port=$PROJECT_PORT"
            ;;
    esac
}

deploy_staging() {
    log_info "Deploying to staging environment..."

    # Build and run staging
    case "$PROJECT" in
        "yaver")
            log_info "Building Yaver CLI for staging..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/cli && npm run build"
            log_info "Starting Yaver agent for staging..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && ./cli/dist/yaver serve --port=$PROJECT_PORT"
            ;;
        "talos")
            log_info "Building Talos for staging..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/web && npm run build"
            log_info "Starting Talos for staging..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/web && npm start --port=$PROJECT_PORT"
            ;;
        "ocpp")
            log_info "Building OCPP for staging..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm run build"
            log_info "Starting OCPP for staging..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm start --port=$PROJECT_PORT"
            ;;
    esac
}

deploy_production() {
    log_info "Deploying to production environment..."

    # Production deployment with additional safety checks
    case "$PROJECT" in
        "yaver")
            log_info "Running production deployment for Yaver..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && ./scripts/deploy-production.sh"
            ;;
        "talos")
            log_info "Running production deployment for Talos..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && ./scripts/deploy-production.sh"
            ;;
        "ocpp")
            log_info "Running production deployment for OCPP..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm run deploy:production"
            ;;
    esac
}

restart_service() {
    log_info "Restarting $PROJECT service on Hetzner..."
    yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && ./scripts/restart.sh"
}

stop_service() {
    log_info "Stopping $PROJECT service on Hetzner..."
    yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && ./scripts/stop.sh"
}

view_logs() {
    log_info "Viewing logs for $PROJECT on Hetzner..."
    yaver exec --device "$HETZNER_SERVER" -- bash -c "tail -f /workspace/$PROJECT/logs/app.log"
}

run_tests() {
    log_info "Running tests for $PROJECT on Hetzner..."
    case "$PROJECT" in
        "yaver")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/desktop/agent && go test ./..."
            ;;
        "talos")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm test"
            ;;
        "ocpp")
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm test"
            ;;
    esac
}

hot_reload() {
    log_info "Enabling hot-reload for $PROJECT..."

    case "$PROJECT" in
        "yaver")
            log_info "Starting Yaver agent with hot-reload..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/desktop/agent && go run . serve --port=$PROJECT_PORT --hot-reload"
            ;;
        "talos")
            log_info "Starting Talos with hot-reload..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT/web && npm run dev -- --hot"
            ;;
        "ocpp")
            log_info "Starting OCPP with hot-reload..."
            yaver exec --device "$HETZNER_SERVER" -- bash -c "cd /workspace/$PROJECT && npm run dev -- --hot"
            ;;
    esac
}

mobile_test_setup() {
    log_info "Setting up mobile testing for $PROJECT..."

    # Expose Hetzner service for mobile access
    log_info "Creating tunnel for mobile access..."
    yaver tunnel expose --device "$HETZNER_SERVER" --local-port "$PROJECT_PORT" --label "$PROJECT-$ENVIRONMENT"

    log_info "Mobile test setup complete!"
    log_info "Access the app from your mobile device using the Yaver app"
}

# Main execution
main() {
    if ! check_hetzner_connection; then
        exit 1
    fi

    case "$ACTION" in
        "deploy")
            deploy_to_hetzner
            ;;
        "restart")
            restart_service
            ;;
        "stop")
            stop_service
            ;;
        "logs")
            view_logs
            ;;
        "test")
            run_tests
            ;;
        "hot-reload")
            hot_reload
            ;;
        "mobile-test")
            mobile_test_setup
            ;;
        *)
            log_error "Unknown action: $ACTION"
            log_info "Available actions: deploy, restart, stop, logs, test, hot-reload, mobile-test"
            exit 1
            ;;
    esac
}

# Run main function
main "$@"