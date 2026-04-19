#!/usr/bin/env bash
set -euo pipefail

# qemu-local-x64-vm.sh
#
# Boots a local x86_64 Linux QEMU guest on macOS using:
# - Ubuntu cloud image
# - cloud-init NoCloud seed
# - forwarded SSH port
#
# This lane exists primarily for Hermes testing because the React Native
# package layout already ships a runnable linux-x64 hermesc binary.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
VM_ROOT_DEFAULT="$ROOT_DIR/.tmp-qemu/x64-guest"

usage() {
  cat <<'EOF'
Usage:
  scripts/qemu-local-x64-vm.sh <init|start|stop|status|ssh|wait-ssh> [options]

Options:
  --vm-root <path>          VM state dir, default .tmp-qemu/x64-guest
  --ssh-port <port>         Host forwarded SSH port, default 2223
  --memory <mb>             RAM MB, default 8192
  --cpus <n>                CPU count, default 4
  --disk-gb <n>             Disk size when creating qcow2, default 40
  --user <name>             Guest username, default ubuntu
  --image-url <url>         Cloud image URL override
  --identity <path>         SSH private key path override

Commands:
  init      Download image, create disk, keys, and cloud-init seed
  start     Boot the VM in the background
  stop      Stop the VM using the saved pid
  status    Show current state files and SSH health
  ssh       SSH into the guest
  wait-ssh  Wait until SSH responds
EOF
}

log() {
  printf '[qemu-x64] %s\n' "$*"
}

fail() {
  printf '[qemu-x64 FAIL] %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

command_name="${1:-}"
if [[ -z "$command_name" ]]; then
  usage
  exit 1
fi
shift || true

vm_root="$VM_ROOT_DEFAULT"
ssh_port="2223"
memory_mb="8192"
cpu_count="4"
disk_gb="40"
guest_user="ubuntu"
image_url="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
identity_path=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vm-root)
      vm_root="${2:-}"
      shift 2
      ;;
    --ssh-port)
      ssh_port="${2:-}"
      shift 2
      ;;
    --memory)
      memory_mb="${2:-}"
      shift 2
      ;;
    --cpus)
      cpu_count="${2:-}"
      shift 2
      ;;
    --disk-gb)
      disk_gb="${2:-}"
      shift 2
      ;;
    --user)
      guest_user="${2:-}"
      shift 2
      ;;
    --image-url)
      image_url="${2:-}"
      shift 2
      ;;
    --identity)
      identity_path="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

require_cmd qemu-system-x86_64
require_cmd /opt/homebrew/bin/qemu-img
require_cmd curl
require_cmd hdiutil
require_cmd ssh
require_cmd ssh-keygen
mkdir -p "$vm_root"
vm_root="$(cd "$vm_root" && pwd)"

base_img="$vm_root/base.img"
disk_img="$vm_root/disk.qcow2"
seed_dir="$vm_root/seed"
seed_img="$vm_root/seed.iso"
cloud_img_tmp="$vm_root/download.tmp"
ssh_priv="${identity_path:-$vm_root/id_ed25519}"
ssh_pub="${ssh_priv}.pub"
pid_file="$vm_root/qemu.pid"
log_file="$vm_root/qemu.log"
monitor_sock="$vm_root/monitor.sock"

ssh_opts=(-p "$ssh_port" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "$ssh_priv")

ensure_keys() {
  if [[ ! -f "$ssh_priv" ]]; then
    ssh-keygen -t ed25519 -N "" -f "$ssh_priv" >/dev/null
  fi
}

write_cloud_init() {
  mkdir -p "$seed_dir"
  cat > "$seed_dir/user-data" <<EOF
#cloud-config
users:
  - default
  - name: $guest_user
    sudo: ALL=(ALL) NOPASSWD:ALL
    groups: sudo
    shell: /bin/bash
    ssh_authorized_keys:
      - $(cat "$ssh_pub")
ssh_pwauth: false
disable_root: true
package_update: false
packages:
  - openssh-server
runcmd:
  - systemctl enable ssh
  - systemctl restart ssh || systemctl restart sshd || true
EOF
  cat > "$seed_dir/meta-data" <<EOF
instance-id: yaver-qemu-x64
local-hostname: yaver-qemu-x64
EOF
  cat > "$seed_dir/network-config" <<'EOF'
version: 2
ethernets:
  qemu0:
    match:
      name: "en*"
    dhcp4: true
EOF
  rm -f "$seed_img"
  hdiutil makehybrid -quiet -o "$seed_img" "$seed_dir" -iso -joliet -default-volume-name cidata >/dev/null
}

wait_ssh() {
  local tries=180
  for _ in $(seq 1 "$tries"); do
    if ssh "${ssh_opts[@]}" "$guest_user@127.0.0.1" "echo ok" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

case "$command_name" in
  init)
    ensure_keys
    if [[ ! -f "$base_img" ]]; then
      log "downloading base image"
      curl -L --fail --retry 3 --connect-timeout 20 --max-time 1800 -o "$cloud_img_tmp" "$image_url"
      mv "$cloud_img_tmp" "$base_img"
    fi
    if [[ ! -f "$disk_img" ]]; then
      log "creating qcow2 disk"
      /opt/homebrew/bin/qemu-img create -f qcow2 -F qcow2 -b "$base_img" "$disk_img" "${disk_gb}G" >/dev/null
    fi
    write_cloud_init
    log "init complete"
    ;;
  start)
    [[ -f "$disk_img" ]] || fail "disk image missing; run init first"
    [[ -f "$seed_dir/user-data" ]] || fail "cloud-init user-data missing; run init first"
    [[ -f "$seed_img" ]] || fail "cloud-init seed image missing; run init first"
    if [[ -f "$pid_file" ]] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      fail "vm already running with pid $(cat "$pid_file")"
    fi
    rm -f "$monitor_sock"
    log "starting VM on ssh port $ssh_port"
    OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES qemu-system-x86_64 \
      -name yaver-qemu-x64 \
      -daemonize \
      -pidfile "$pid_file" \
      -machine q35,accel=tcg \
      -cpu max \
      -smp "$cpu_count" \
      -m "$memory_mb" \
      -display none \
      -drive if=virtio,format=qcow2,file="$disk_img" \
      -cdrom "$seed_img" \
      -nic "user,hostfwd=tcp:127.0.0.1:${ssh_port}-:22" \
      -monitor "unix:$monitor_sock,server,nowait" \
      -serial "file:$log_file"
    log "VM started (pid $(cat "$pid_file"))"
    ;;
  stop)
    if [[ ! -f "$pid_file" ]]; then
      fail "pid file not found; vm may not be running"
    fi
    pid="$(cat "$pid_file")"
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      fail "process $pid is not running"
    fi
    log "stopping VM pid $pid"
    kill "$pid"
    for _ in $(seq 1 30); do
      if ! kill -0 "$pid" 2>/dev/null; then
        rm -f "$pid_file"
        log "stopped"
        exit 0
      fi
      sleep 1
    done
    fail "vm did not stop within timeout"
    ;;
  status)
    if [[ -f "$pid_file" ]] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      echo "running pid=$(cat "$pid_file")"
    else
      echo "stopped"
    fi
    echo "vm_root=$vm_root"
    echo "ssh_port=$ssh_port"
    echo "identity=$ssh_priv"
    if wait_ssh; then
      echo "ssh=ok"
    else
      echo "ssh=down"
    fi
    ;;
  ssh)
    wait_ssh || fail "ssh is not ready"
    exec ssh "${ssh_opts[@]}" "$guest_user@127.0.0.1"
    ;;
  wait-ssh)
    if wait_ssh; then
      log "ssh is ready"
    else
      fail "ssh did not become ready in time"
    fi
    ;;
  *)
    usage
    fail "unknown command: $command_name"
    ;;
esac
