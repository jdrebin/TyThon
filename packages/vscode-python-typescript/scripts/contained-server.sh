#!/bin/sh
# Invoked only inside our transient systemd service. Never change a parent group.
set -eu
unit=$1
memory_bytes=$2
swap_bytes=$3
shift 3
case "$unit" in typed-python-*.service) ;; *) echo 'Invalid tython containment unit' >&2; exit 1 ;; esac
case "$memory_bytes:$swap_bytes" in *[!0-9:]*|:*) exit 1 ;; esac

legacy_path=$(awk -F: '$2 ~ /(^|,)memory(,|$)/ { print $3 }' /proc/self/cgroup)
if [ -n "$legacy_path" ]; then
    case "$legacy_path" in */"$unit") ;; *) echo 'Server is not in its dedicated memory cgroup' >&2; exit 1 ;; esac
    group=/sys/fs/cgroup/memory$legacy_path
    # cgroup v1 limits RAM and RAM+swap, not swap independently. Bounding the
    # combined total keeps the whole server below memory_bytes + swap_bytes.
    printf '%s\n' "$memory_bytes" > "$group/memory.limit_in_bytes"
    total_bytes=$((memory_bytes + swap_bytes))
    printf '%s\n' "$total_bytes" > "$group/memory.memsw.limit_in_bytes"
    test "$(awk '{print $1}' "$group/memory.limit_in_bytes")" = "$memory_bytes"
    test "$(awk '{print $1}' "$group/memory.memsw.limit_in_bytes")" = "$total_bytes"
    echo "tython containment verified: RAM $memory_bytes bytes; RAM+swap $total_bytes bytes (cgroup v1)." >&2
else
    unified_path=$(awk -F: '$1 == "0" { print $3 }' /proc/self/cgroup)
    case "$unified_path" in */"$unit") ;; *) echo 'Server is not in its dedicated memory cgroup' >&2; exit 1 ;; esac
    group=/sys/fs/cgroup$unified_path
    test "$(awk '{print $1}' "$group/memory.max")" = "$memory_bytes"
    test "$(awk '{print $1}' "$group/memory.swap.max")" = "$swap_bytes"
    echo "tython containment verified: RAM $memory_bytes bytes; swap $swap_bytes bytes (cgroup v2)." >&2
fi
exec "$@"
