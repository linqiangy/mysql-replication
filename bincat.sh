#!/bin/bash
# bincat 服务控制脚本
# 用法: ./bincat.sh {start|stop|restart|status}
set -u

# ---------- 配置 ----------
APP_NAME="bincat"
PROJ_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN="${PROJ_DIR}/bin/bincat"
CONF="${PROJ_DIR}/configs/config.yaml"
PID_FILE="${PROJ_DIR}/bin/bincat.pid"
LOG_FILE="${PROJ_DIR}/logs/bincat.log"

mkdir -p "${PROJ_DIR}/logs" "${PROJ_DIR}/bin"

# ---------- 函数 ----------
pid_alive() {
    local pid="$1"
    [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

get_pid() {
    if [ -f "$PID_FILE" ]; then
        local p
        p="$(cat "$PID_FILE" 2>/dev/null)"
        if pid_alive "$p"; then
            echo "$p"
            return 0
        fi
        # pid 文件失效，清理
        rm -f "$PID_FILE"
    fi
    return 1
}

wait_stop() {
    local pid="$1" i=0
    while pid_alive "$pid" && [ $i -lt 100 ]; do
        sleep 0.1; i=$((i+1))
    done
    ! pid_alive "$pid"
}

do_start() {
    if p="$(get_pid)"; then
        echo "[${APP_NAME}] 已在运行 (pid ${p})"; return 0
    fi
    if [ ! -x "$BIN" ]; then
        echo "[${APP_NAME}] 二进制不存在: ${BIN}"
        echo "  请先构建: make build"
        return 1
    fi
    if [ ! -f "$CONF" ]; then
        echo "[${APP_NAME}] 配置不存在: ${CONF}"
        echo "  请先复制: cp configs/config.example.yaml configs/config.yaml"
        return 1
    fi
    nohup "$BIN" -config "$CONF" >> "$LOG_FILE" 2>&1 &
    local pid=$!
    echo "$pid" > "$PID_FILE"
    sleep 1
    if pid_alive "$pid"; then
        echo "[${APP_NAME}] 已启动 (pid ${pid})"
        echo "  日志: tail -f ${LOG_FILE}"
        echo "  后台: http://localhost:8080 (配置文件 server.addr)"
    else
        echo "[${APP_NAME}] 启动失败，最近日志:"
        tail -5 "$LOG_FILE"
        rm -f "$PID_FILE"
        return 1
    fi
}

do_stop() {
    if ! p="$(get_pid)"; then
        echo "[${APP_NAME}] 未在运行"
        return 0
    fi
    echo "[${APP_NAME}] 停止中 (pid ${p})，发送 SIGTERM 优雅停机..."
    kill -TERM "$p" 2>/dev/null
    if wait_stop "$p"; then
        rm -f "$PID_FILE"
        echo "[${APP_NAME}] 已停止"
    else
        echo "[${APP_NAME}] 优雅停机超时，强制 SIGKILL"
        kill -KILL "$p" 2>/dev/null
        rm -f "$PID_FILE"
        echo "[${APP_NAME}] 已强制停止"
    fi
    return 0
}

do_restart() {
    do_stop
    sleep 1
    do_start
}

do_status() {
    if p="$(get_pid)"; then
        echo "[${APP_NAME}] 运行中 (pid ${p})"
        for port in 8080 9090; do
            if nc -z 127.0.0.1 $port 2>/dev/null; then
                echo "  端口 ${port}: 监听中"
            fi
        done
        echo "  日志: tail -f ${LOG_FILE}"
        return 0
    else
        echo "[${APP_NAME}] 未在运行"
        return 1
    fi
}

do_build() {
    echo "[${APP_NAME}] 编译中..."
    if command -v go >/dev/null 2>&1; then
        GO=go
    elif [ -x "$HOME/sdk/go/bin/go" ]; then
        GO="$HOME/sdk/go/bin/go"
    else
        echo "找不到 go，请安装或 export PATH=\$HOME/sdk/go/bin:\$PATH"; exit 1
    fi
    (cd "$PROJ_DIR" && "$GO" build -o bin/bincat ./cmd/bincat) || exit 1
    echo "[${APP_NAME}] 编译完成: ${BIN}"
}

case "${1:-}" in
    start)   do_start ;;
    stop)    do_stop ;;
    restart) do_restart ;;
    status)  do_status ;;
    build)   do_build ;;
    *)
        echo "用法: $0 {start|stop|restart|status|build}"
        echo ""
        echo "  start    启动（后台运行，日志 logs/bincat.log）"
        echo "  stop     停止（SIGTERM 优雅停机，超时强杀）"
        echo "  restart  重启"
        echo "  status   查看运行状态"
        echo "  build    编译（make build 等价）"
        exit 1
        ;;
esac
