#!/bin/sh
# 自动修正数据目录属主(bind mount 到宿主机时免去手动 chown),然后降权运行。
# 幂等:每次启动都会执行,将 /data 属主归给 openlist(uid 10001)。
set -e

if [ -d /data ]; then
  chown -R openlist:openlist /data
fi

exec setpriv --reuid=10001 --regid=10001 --init-groups /usr/local/bin/openlist-sync "$@"