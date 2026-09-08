#!/bin/bash

# Keep CocoaPods' generated relative React Native paths operational when Pods
# is relocated to an external artifact volume. CocoaPods resolves symlinks when
# it writes PODS_ROOT, so ${PODS_ROOT}/../../node_modules points beside the
# physical Pods directory rather than back into the checkout. The archive must
# be able to execute React Native and Hermes scripts from that resolved path.
apple_ensure_pods_directory() {
  local pods_dir="$1"

  # A relocated Pods directory may outlive the generated directory on its
  # external volume. To the shell that is a dangling symlink; to CocoaPods it
  # is an existing path that mkdir(2) cannot create, producing the opaque
  # "File exists @ dir_s_mkdir" failure before an archive starts. The link
  # itself is the operator's explicit destination, so recreating only its
  # missing directory is unambiguous and preserves the configured layout.
  if [ ! -L "$pods_dir" ] || [ -d "$pods_dir" ]; then
    return 0
  fi
  if [ -e "$pods_dir" ]; then
    echo "ERROR: CocoaPods path is a symlink to a non-directory: $pods_dir" >&2
    return 1
  fi

  local link_target resolved_target
  link_target="$(readlink "$pods_dir")" || return 1
  [ -n "$link_target" ] || {
    echo "ERROR: CocoaPods symlink has an empty target: $pods_dir" >&2
    return 1
  }
  case "$link_target" in
    /*) resolved_target="$link_target" ;;
    *) resolved_target="$(dirname "$pods_dir")/$link_target" ;;
  esac

  if ! mkdir -p "$resolved_target" 2>/dev/null; then
    # The symlink is generated CocoaPods state. An external build volume can
    # disappear between releases, leaving (for example) Pods pointed below an
    # unmounted /Volumes entry that the current user cannot recreate. Retrying
    # mkdir there only emits "Permission denied" and strands an otherwise
    # healthy local release. Fall back to a real checkout-local Pods directory;
    # `pod install` immediately below will repopulate it from the lockfile.
    rm "$pods_dir" || {
      echo "ERROR: could not remove unavailable CocoaPods symlink: $pods_dir" >&2
      return 1
    }
    mkdir -p "$pods_dir" || {
      echo "ERROR: could not create local CocoaPods directory: $pods_dir" >&2
      return 1
    }
    echo "External CocoaPods target is unavailable; restored local generated directory: $pods_dir"
    return 0
  fi
  if [ ! -d "$pods_dir" ]; then
    echo "ERROR: restored CocoaPods symlink target is still not a directory: $resolved_target" >&2
    return 1
  fi
  echo "Restored missing external CocoaPods directory: $resolved_target"
}

apple_ensure_pods_node_modules_layout() {
  local pods_dir="$1"
  local source_node_modules="$2"
  local required_rel="react-native/scripts/xcode/with-environment.sh"

  [ -d "$pods_dir" ] || {
    echo "ERROR: CocoaPods directory is missing: $pods_dir" >&2
    return 1
  }
  [ -f "$source_node_modules/$required_rel" ] || {
    echo "ERROR: mobile dependencies are incomplete: $source_node_modules/$required_rel" >&2
    echo "       Restore the lockfile dependencies before archiving." >&2
    return 1
  }

  local physical_pods physical_mobile expected_node_modules source_physical
  physical_pods="$(cd "$pods_dir" && pwd -P)" || return 1
  physical_mobile="$(cd "$physical_pods/../.." && pwd -P)" || return 1
  expected_node_modules="$physical_mobile/node_modules"
  source_physical="$(cd "$source_node_modules" && pwd -P)" || return 1

  if [ -f "$expected_node_modules/$required_rel" ]; then
    return 0
  fi
  if [ "$expected_node_modules" = "$source_physical" ]; then
    echo "ERROR: React Native archive script is missing: $expected_node_modules/$required_rel" >&2
    return 1
  fi

  # An empty directory at the resolved location cannot contain user data and
  # is safe to replace. Refuse a non-empty directory or an existing symlink:
  # choosing which contents to discard is not an unambiguous self-heal.
  if [ -d "$expected_node_modules" ] && [ ! -L "$expected_node_modules" ]; then
    if [ -n "$(find "$expected_node_modules" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
      echo "ERROR: external CocoaPods resolves node_modules to an incomplete non-empty directory:" >&2
      echo "       $expected_node_modules" >&2
      echo "       Review that directory, then link it to $source_physical or restore its dependencies." >&2
      return 1
    fi
    rmdir "$expected_node_modules" || return 1
  elif [ -e "$expected_node_modules" ] || [ -L "$expected_node_modules" ]; then
    echo "ERROR: external CocoaPods resolves node_modules to an unusable existing path:" >&2
    echo "       $expected_node_modules" >&2
    echo "       Review that path, then link it to $source_physical." >&2
    return 1
  fi

  mkdir -p "$physical_mobile"
  ln -s "$source_physical" "$expected_node_modules"
  if [ ! -f "$expected_node_modules/$required_rel" ]; then
    echo "ERROR: linked external node_modules still cannot execute the Hermes archive script." >&2
    return 1
  fi
  echo "Linked external CocoaPods dependencies: $expected_node_modules -> $source_physical"
}
