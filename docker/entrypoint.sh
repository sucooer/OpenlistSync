#!/bin/sh
# 自动修正挂载目录属主(bind mount 到宿主机时免去手动 chown),然后降权运行。
# 幂等:每次启动都会执行。
# 自动检测 /proc/mounts 中所有用户挂载目录并修正属主,无需手动指定路径。
set -e

# 修正 /data(配置与同步数据)
chown -R openlist:openlist /data 2>/dev/null || true

# 自动检测并修正所有非系统挂载目录
if [ -f /proc/mounts ]; then
  while read -r _ mount_point _; do
    case "$mount_point" in
      /data|/proc|/sys|/dev|/run|/boot|/lib*|/usr|/bin|/sbin|/etc|/var|/tmp|/root|/home|/|"" ) continue ;;
    esac
    if [ -d "$mount_point" ]; then
      chown -R openlist:openlist "$mount_point" 2>/dev/null || true
    fi
  done < /proc/mounts
fi

exec setpriv --reuid=10001 --regid=10001 --init-groups /usr/local/bin/openlist-sync "$@"
