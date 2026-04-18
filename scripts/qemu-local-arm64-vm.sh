#!/usr/bin/env bash
set -euo pipefail

# qemu-local-arm64-vm.sh
#
# Bootstraps a local ARM64 Linux QEMU guest on Apple Silicon macOS using:
# - Ubuntu cloud image
# - cloud-init NoCloud seed
# - forwarded SSH port
#
# The goal is a reproducible disposable guest for Yaver's phone-first
# portability tests, not a general VM manager.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
VM_ROOT_DEFAULT="$ROOT_DIR/.tmp-qemu/arm64-guest"

usage() {
  cat <<'EOF'
Usage:
  scripts/qemu-local-arm64-vm.sh <init|start|stop|status|ssh|wait-ssh> [options]

Options:
  --vm-root <path>          VM state dir, default .tmp-qemu/arm64-guest
  --ssh-port <port>         Host forwarded SSH port, default 2222
  --memory <mb>             RAM MB, default 8192
  --cpus <n>                CPU count, default 4
  --disk-gb <n>             Disk size when creating qcow2, default 40
  --user <name>             Guest username, default ubuntu
  --image-url <url>         Cloud image URL override
  --kernel-url <url>        Direct-boot kernel URL override
  --initrd-url <url>        Direct-boot initrd URL override
  --identity <path>         SSH private key path override

Commands:
  init      Download image, create disk, keys, cloud-init seed, firmware vars
  start     Boot the VM in the background
  stop      Stop the VM using the saved pid
  status    Show current state files and SSH health
  ssh       SSH into the guest
  wait-ssh  Wait until SSH responds
EOF
}

log() {
  printf '[qemu-vm] %s\n' "$*"
}

fail() {
  printf '[qemu-vm FAIL] %s\n' "$*" >&2
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
ssh_port="2222"
memory_mb="8192"
cpu_count="4"
disk_gb="40"
guest_user="ubuntu"
image_url="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-arm64.img"
kernel_url="https://cloud-images.ubuntu.com/noble/current/unpacked/noble-server-cloudimg-arm64-vmlinuz-generic"
initrd_url="https://cloud-images.ubuntu.com/noble/current/unpacked/noble-server-cloudimg-arm64-initrd-generic"
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
    --kernel-url)
      kernel_url="${2:-}"
      shift 2
      ;;
    --initrd-url)
      initrd_url="${2:-}"
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

require_cmd qemu-system-aarch64
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
kernel_img="$vm_root/vmlinuz"
initrd_img="$vm_root/initrd"
ssh_priv="${identity_path:-$vm_root/id_ed25519}"
ssh_pub="${ssh_priv}.pub"
firmware_code="/opt/homebrew/Cellar/qemu/10.2.2/share/qemu/edk2-aarch64-code.fd"
firmware_vars_src="/opt/homebrew/Cellar/qemu/10.2.2/share/qemu/edk2-arm-vars.fd"
firmware_code_img="$vm_root/efi.img"
firmware_vars="$vm_root/vars.img"
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
instance-id: yaver-qemu-arm64
local-hostname: yaver-qemu-arm64
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

ensure_firmware_vars() {
  [[ -f "$firmware_code" ]] || fail "missing firmware code: $firmware_code"
  if [[ ! -f "$firmware_code_img" ]]; then
    dd if=/dev/zero of="$firmware_code_img" bs=1m count=64 >/dev/null 2>&1
    dd if="$firmware_code" of="$firmware_code_img" conv=notrunc >/dev/null 2>&1
  fi
  if [[ ! -f "$firmware_vars" ]]; then
    dd if=/dev/zero of="$firmware_vars" bs=1m count=64 >/dev/null 2>&1
    dd if="$firmware_vars_src" of="$firmware_vars" conv=notrunc >/dev/null 2>&1
  fi
}

wait_ssh() {
  local tries=120
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
    if [[ -n "$kernel_url" && ! -f "$kernel_img" ]]; then
      log "downloading kernel"
      curl -L --fail --retry 3 --connect-timeout 20 --max-time 1800 -o "$kernel_img" "$kernel_url"
    fi
    if [[ -n "$initrd_url" && ! -f "$initrd_img" ]]; then
      log "downloading initrd"
      curl -L --fail --retry 3 --connect-timeout 20 --max-time 1800 -o "$initrd_img" "$initrd_url"
    fi
    if [[ ! -f "$disk_img" ]]; then
      log "creating qcow2 disk"
      /opt/homebrew/bin/qemu-img create -f qcow2 -F qcow2 -b "$base_img" "$disk_img" "${disk_gb}G" >/dev/null
    fi
    write_cloud_init
    ensure_firmware_vars
    log "init complete"
    ;;
  start)
    [[ -f "$disk_img" ]] || fail "disk image missing; run init first"
    [[ -f "$seed_dir/user-data" ]] || fail "cloud-init user-data missing; run init first"
    [[ -f "$seed_img" ]] || fail "cloud-init seed image missing; run init first"
    ensure_firmware_vars
    if [[ -f "$pid_file" ]] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      fail "vm already running with pid $(cat "$pid_file")"
    fi
    rm -f "$monitor_sock"
    log "starting VM on ssh port $ssh_port"
    qemu_args=(
      -name yaver-qemu-arm64
      -daemonize
      -pidfile "$pid_file"
      -machine virt,accel=hvf,highmem=on
      -cpu host
      -smp "$cpu_count"
      -m "$memory_mb"
      -display none
      -serial file:"$log_file"
      -device virtio-net-pci,netdev=net0
      -netdev user,id=net0,hostfwd=tcp::"$ssh_port"-:22
      -device virtio-rng-pci
      -device virtio-blk-pci,drive=hd0,bootindex=0
      -drive if=none,file="$disk_img",format=qcow2,id=hd0
      -device virtio-blk-pci,drive=cloud,bootindex=1
      -drive if=none,file="$seed_img",format=raw,media=cdrom,id=cloud
      -drive if=pflash,format=raw,readonly=on,file="$firmware_code_img"
      -drive if=pflash,format=raw,file="$firmware_vars"
      -monitor unix:"$monitor_sock",server,nowait
    )
    if [[ -f "$kernel_img" ]]; then
      qemu_args+=(-kernel "$kernel_img" -append "root=/dev/vda1 console=ttyS0")
    fi
    if [[ -f "$initrd_img" ]]; then
      qemu_args+=(-initrd "$initrd_img")
    fi
    qemu-system-aarch64 "${qemu_args[@]}"
    [[ -f "$pid_file" ]] || fail "qemu started but pid file was not created"
    log "pid $(cat "$pid_file")"
    ;;
  stop)
    [[ -f "$pid_file" ]] || fail "pid file missing"
    pid="$(cat "$pid_file")"
    kill "$pid" 2>/dev/null || true
    sleep 2
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
    log "stopped"
    ;;
  status)
    if [[ -f "$pid_file" ]] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      log "running pid $(cat "$pid_file")"
    else
      log "not running"
    fi
    if [[ -f "$log_file" ]]; then
      log "log file $log_file"
    fi
    if [[ -f "$ssh_priv" ]]; then
      log "ssh key $ssh_priv"
    fi
    if ssh "${ssh_opts[@]}" "$guest_user@127.0.0.1" "echo ok" >/dev/null 2>&1; then
      log "ssh healthy on 127.0.0.1:$ssh_port"
    else
      log "ssh not ready on 127.0.0.1:$ssh_port"
    fi
    ;;
  ssh)
    exec ssh "${ssh_opts[@]}" "$guest_user@127.0.0.1"
    ;;
  wait-ssh)
    if wait_ssh; then
      log "ssh ready"
    else
      tail -n 80 "$log_file" >&2 || true
      fail "ssh did not become ready"
    fi
    ;;
  *)
    usage
    exit 1
    ;;
esac
