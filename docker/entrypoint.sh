#!/bin/sh
# 自动修正挂载目录属主(bind mount 到宿主机时免去手动 chown),然后降权运行。
# 幂等:每次启动都会执行。
# 自动检测 /proc/mounts 中所有用户挂载目录并修正属主,无需手动指定路径。
# 当 WEB_LISTEN 绑定到非环回地址且 WEB_API_TOKEN 为空时直接拒绝启动,
# 避免公网/局域网暴露。
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

# 安全闸门:仅当绑定到本机环回时允许 token 为空,其余情况强制要求 token。
# 这里只拦截以 "127.0.0.1:" / "[::1]:" 开头的环回地址。
case "${WEB_LISTEN:-}" in
  "127.0.0.1:"*|"::1:"*|"localhost:"*)
    : ;;  # loopback only, token 可为空
  *)
    if [ -z "${WEB_API_TOKEN:-}" ]; then
      echo "FATAL: WEB_LISTEN=${WEB_LISTEN} 绑定了非环回地址,但 WEB_API_TOKEN 为空。" >&2
      echo "       任何能访问该端口的人都可以控制你的同步任务——不允许公网/局域网裸跑。" >&2
      echo "       修复: 在 .env(本参宿上)设置 WEB_API_TOKEN=一串随机令牌,或" >&2
      echo "       将 WEB_LISTEN 改为 127.0.0.1:18222 仅本机环回。" >&2
      exit 1
    fi
    ;;
esac

exec setpriv --reuid=10001 --regid=10001 --init-groups /usr/local/bin/openlist-sync "$@"
