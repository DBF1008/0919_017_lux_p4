#!/usr/bin/env bash
#
# test.sh — lux 工程化特性手动测试脚本
#
# 覆盖以下特性：
#   1. 结构化日志 (log/slog, JSON 输出到 stderr)
#   2. 每个 HTTP 请求的唯一 request_id 全链路传递 (X-Request-ID 头 + 日志字段)
#   3. --metrics-port 暴露的 Prometheus 指标 (下载速度 / 成功率 / 重试次数)
#   4. SIGINT / SIGTERM 优雅关闭 (不再接受新任务，等待当前任务完成后退出)
#
# 用法：
#   ./test.sh            # 运行全部测试
#   ./test.sh build      # 只运行指定分组 (build / unit / log / metrics / shutdown)
#
# 依赖：go、python3、curl

set -u
cd "$(dirname "$0")"

# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------
WORKDIR="${LUX_TEST_WORKDIR:-/tmp/lux_manual_test}"
FILE_PORT="${LUX_TEST_FILE_PORT:-18181}"     # 静态文件服务端口
SLOW_PORT="${LUX_TEST_SLOW_PORT:-18182}"     # 限速服务端口（用于优雅关闭测试）
METRICS_PORT="${LUX_TEST_METRICS_PORT:-21112}"
LUX_BIN="$WORKDIR/lux"
export GOCACHE="${GOCACHE:-/tmp/lux-go-build-cache}"

PASS=0
FAIL=0
PIDS=()

# ---------------------------------------------------------------------------
# 工具函数
# ---------------------------------------------------------------------------
c_green()  { printf '\033[32m%s\033[0m\n' "$1"; }
c_red()    { printf '\033[31m%s\033[0m\n' "$1"; }
c_yellow() { printf '\033[33m%s\033[0m\n' "$1"; }

ok()   { PASS=$((PASS+1)); c_green  "  [PASS] $1"; }
bad()  { FAIL=$((FAIL+1)); c_red    "  [FAIL] $1"; }
info() { c_yellow "== $1"; }

cleanup() {
  for pid in "${PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null
  done
}
trap cleanup EXIT

start_bg() { # start_bg <cmd...> -> echoes pid
  "$@" > /dev/null 2>&1 &
  PIDS+=($!)
  echo "${PIDS[-1]}"
}

wait_port() { # wait_port <port> <seconds>
  local port=$1 timeout=$2 i
  for ((i=0; i<timeout*10; i++)); do
    if curl -s -o /dev/null "http://127.0.0.1:$port/"; then return 0; fi
    sleep 0.1
  done
  return 1
}

# ---------------------------------------------------------------------------
# 准备测试环境：编译二进制、生成测试文件、启动本地 HTTP 服务
# ---------------------------------------------------------------------------
setup() {
  info "准备测试环境 ($WORKDIR)"
  mkdir -p "$WORKDIR/files" "$WORKDIR/out"
  go build -o "$LUX_BIN" . || { c_red "编译失败"; exit 1; }

  python3 - << 'PYEOF'
import os
base = os.environ.get("LUX_TEST_WORKDIR", "/tmp/lux_manual_test")
os.makedirs(f"{base}/files", exist_ok=True)
# 小文件：基础下载 / 日志测试
with open(f"{base}/files/small.mp4", "wb") as f:
    f.write(os.urandom(2 * 1024 * 1024))
# 大文件：优雅关闭 / 指标测试（限速服务器下约需 8 秒）
with open(f"{base}/files/big1.mp4", "wb") as f:
    f.write(os.urandom(100 * 1024 * 1024))
with open(f"{base}/files/big2.mp4", "wb") as f:
    f.write(os.urandom(100 * 1024 * 1024))
PYEOF

  cat > "$WORKDIR/slow_server.py" << 'PYEOF'
import http.server, sys, time

PORT = int(sys.argv[1])

class Handler(http.server.SimpleHTTPRequestHandler):
    # 限速 ~12MB/s，让下载持续数秒以便测试信号处理与指标抓取
    def copyfile(self, src, dst, length=None):
        while True:
            buf = src.read(256 * 1024)
            if not buf:
                break
            dst.write(buf)
            time.sleep(0.02)
    def log_message(self, *args):
        pass

http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
PYEOF

  (cd "$WORKDIR/files" && exec python3 -m http.server "$FILE_PORT" --bind 127.0.0.1) > /dev/null 2>&1 &
  PIDS+=($!)
  (cd "$WORKDIR/files" && exec python3 "$WORKDIR/slow_server.py" "$SLOW_PORT") > /dev/null 2>&1 &
  PIDS+=($!)

  wait_port "$FILE_PORT" 5 && wait_port "$SLOW_PORT" 5 \
    || { c_red "本地 HTTP 服务启动失败"; exit 1; }
  ok "测试环境就绪 (文件服务 :$FILE_PORT, 限速服务 :$SLOW_PORT)"
}

# ---------------------------------------------------------------------------
# 1. 编译与静态检查
# ---------------------------------------------------------------------------
test_build() {
  info "测试 1: 编译与静态检查"
  go build ./... && ok "go build ./..." || bad "go build ./..."
  go vet ./... && ok "go vet ./..." || bad "go vet ./..."
  gofmt -l . | grep -q . && bad "gofmt 存在未格式化文件: $(gofmt -l .)" || ok "gofmt 无未格式化文件"
}

# ---------------------------------------------------------------------------
# 2. 单元测试
# ---------------------------------------------------------------------------
test_unit() {
  info "测试 2: 单元测试 (部分 extractor 用例依赖外网，失败属预期)"
  go test ./logging/... ./metrics/... ./parser/... && ok "logging/metrics/parser 单元测试" || bad "logging/metrics/parser 单元测试"
  go test ./... ; c_yellow "  [INFO] go test ./... 退出码 $? (依赖外网的用例失败可忽略)"
}

# ---------------------------------------------------------------------------
# 3. 结构化日志 + request_id 全链路
# ---------------------------------------------------------------------------
test_log() {
  info "测试 3: 结构化日志 (slog) 与 request_id"
  local out="$WORKDIR/out/log" url="http://127.0.0.1:$FILE_PORT/small.mp4"
  mkdir -p "$out" && cd "$out" || return 1

  "$LUX_BIN" --debug --chunk-size 0 -o "$out" "$url" > stdout.log 2> stderr.log
  [ $? -eq 0 ] && ok "下载成功" || bad "下载失败 (见 $out/stderr.log)"
  [ -f "$out/small.mp4" ] && ok "产物文件存在" || bad "产物文件缺失"

  # 3.1 stderr 为 JSON 结构化日志，且包含 request_id 字段
  if grep -q '"msg":"download task started"' stderr.log && grep -q '"request_id":"[0-9a-f]\{32\}"' stderr.log; then
    ok "stderr 输出 JSON 结构化日志且含 request_id 字段"
  else
    bad "stderr 缺少 JSON 结构化日志或 request_id (见 stderr.log)"
  fi

  # 3.2 所有结构化日志行都携带 request_id 字段（每个 HTTP 请求拥有唯一 id，
  #     同一任务内的日志可通过任务级 request_id 关联）
  local missing
  missing=$(grep '"msg"' stderr.log | grep -cv '"request_id":"[0-9a-f]\{32\}"')
  [ "$missing" = "0" ] && ok "所有结构化日志均携带 request_id 字段" \
                       || bad "有 $missing 行结构化日志缺少 request_id"

  # 3.3 --debug 终端输出保留 color 格式并显示 X-Request-ID
  if grep -q "Request-ID:" stdout.log; then
    ok "终端 debug 输出包含 Request-ID (color 输出保留)"
  else
    bad "终端 debug 输出缺少 Request-ID"
  fi

  # 3.4 HTTP 请求头携带 X-Request-ID（通过本地服务访问日志之外的方式验证：
  #     debug 输出的 Request-ID 与日志中的 request_id 相同）
  local term_id log_id
  term_id=$(grep "Request-ID:" stdout.log | head -1 | awk '{print $NF}' | tr -d '\r')
  log_id=$(grep -o '"request_id":"[0-9a-f]*"' stderr.log | head -1 | cut -d'"' -f4)
  if [ -n "$term_id" ] && [ "$term_id" = "$log_id" ]; then
    ok "终端 Request-ID 与结构化日志 request_id 一致 ($term_id)"
  else
    bad "终端 Request-ID ($term_id) 与日志 request_id ($log_id) 不一致"
  fi
  cd - > /dev/null
}

# ---------------------------------------------------------------------------
# 4. Prometheus 指标端点
# ---------------------------------------------------------------------------
test_metrics() {
  info "测试 4: Prometheus 指标 (--metrics-port $METRICS_PORT)"
  local out="$WORKDIR/out/metrics"
  mkdir -p "$out" && cd "$out" || return 1
  local slow_url="http://127.0.0.1:$SLOW_PORT/big1.mp4"

  # 后台下载大文件（约 8 秒），期间抓取 /metrics
  "$LUX_BIN" --metrics-port "$METRICS_PORT" --chunk-size 0 -o "$out" "$slow_url" \
    > stdout.log 2> stderr.log &
  local lux_pid=$!

  local scraped="" i
  for ((i=0; i<40; i++)); do
    if curl -s "http://127.0.0.1:$METRICS_PORT/metrics" > metrics.snap 2>/dev/null \
       && grep -q "lux_downloads_in_flight 1" metrics.snap; then
      scraped=1; break
    fi
    sleep 0.5
  done
  [ -n "$scraped" ] && ok "下载进行中 /metrics 可抓取且 lux_downloads_in_flight=1" \
                    || bad "无法抓取 /metrics 或 in_flight 指标异常"

  # 等待下载完成后再抓一次（进程退出前），验证结果类指标
  local final="" 
  for ((i=0; i<40; i++)); do
    kill -0 $lux_pid 2>/dev/null || break
    if curl -s "http://127.0.0.1:$METRICS_PORT/metrics" > metrics_final.snap 2>/dev/null \
       && grep -q 'lux_downloads_total{result="success"} 1' metrics_final.snap; then
      final=1; break
    fi
    sleep 0.5
  done
  wait $lux_pid; local rc=$?
  [ $rc -eq 0 ] && ok "下载进程正常退出 (exit 0)" || bad "下载进程退出码 $rc"

  if [ -n "$final" ]; then
    ok 'lux_downloads_total{result="success"} = 1'
    grep -q "lux_download_success_ratio 1" metrics_final.snap \
      && ok "lux_download_success_ratio = 1 (成功率)" || bad "成功率指标异常"
    grep -qE "lux_download_speed_bytes_per_second [0-9.e+]+" metrics_final.snap \
      && ok "lux_download_speed_bytes_per_second 已上报 (下载速度)" || bad "下载速度指标缺失"
    grep -q "lux_download_bytes_total" metrics_final.snap \
      && ok "lux_download_bytes_total 已上报" || bad "下载字节数指标缺失"
  else
    bad "未能在进程退出前抓取到最终指标 (可增大 big1.mp4 或降低限速)"
  fi

  # 4.1 重试指标：请求一个 404 的 URL，--retry 3 应产生 2 次重试
  "$LUX_BIN" --metrics-port "$((METRICS_PORT+1))" --retry 3 \
    "http://127.0.0.1:$FILE_PORT/not-exist.mp4" > /dev/null 2> retry_err.log &
  local retry_pid=$!
  local retried=""
  for ((i=0; i<20; i++)); do
    if curl -s "http://127.0.0.1:$((METRICS_PORT+1))/metrics" > retry.snap 2>/dev/null \
       && grep -qE "lux_http_request_retries_total [1-9]" retry.snap; then
      retried=1; break
    fi
    sleep 0.5
  done
  wait $retry_pid
  [ -n "$retried" ] && ok "lux_http_request_retries_total 记录了 HTTP 重试" \
                    || bad "未观察到 HTTP 重试指标"
  grep -q '"request_id"' retry_err.log && ok "失败请求的错误日志同样携带 request_id" \
                                    || bad "失败请求日志缺少 request_id"
  cd - > /dev/null
}

# ---------------------------------------------------------------------------
# 5. 优雅关闭 (SIGINT / SIGTERM)
# ---------------------------------------------------------------------------
graceful_case() { # graceful_case <signal>
  local sig=$1
  local out="$WORKDIR/out/shutdown_$sig"
  mkdir -p "$out" && cd "$out" || return 1
  local url1="http://127.0.0.1:$SLOW_PORT/big1.mp4"
  local url2="http://127.0.0.1:$SLOW_PORT/big2.mp4"

  "$LUX_BIN" --chunk-size 0 -o "$out" "$url1" "$url2" > stdout.log 2> stderr.log &
  local lux_pid=$!
  sleep 2                       # 等第一个任务进入下载中
  kill -"$sig" "$lux_pid"       # 发送信号
  wait "$lux_pid"; local rc=$?

  # 5.x.1 当前任务完整下载完成
  local expect actual
  expect=$(stat -f%z "$WORKDIR/files/big1.mp4")
  if [ -f "$out/big1.mp4" ]; then
    actual=$(stat -f%z "$out/big1.mp4")
    [ "$actual" = "$expect" ] && ok "[$sig] 进行中的任务已完整下载 ($actual 字节)" \
                              || bad "[$sig] big1.mp4 大小 $actual != $expect"
  else
    bad "[$sig] big1.mp4 未生成"
  fi

  # 5.x.2 未开始的新任务被拒绝
  if [ ! -f "$out/big2.mp4" ] && grep -q "skipping remaining tasks" stderr.log; then
    ok "[$sig] 收到信号后不再接受新任务 (big2 被跳过)"
  else
    bad "[$sig] 新任务未被正确跳过"
  fi

  # 5.x.3 进程正常退出
  [ $rc -eq 0 ] && ok "[$sig] 进程优雅退出 (exit 0)" || bad "[$sig] 进程退出码 $rc"
  cd - > /dev/null
}

test_shutdown() {
  info "测试 5: 优雅关闭 (SIGINT / SIGTERM)"
  graceful_case INT
  graceful_case TERM
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------
ONLY="${1:-all}"
setup
case "$ONLY" in
  build)    test_build ;;
  unit)     test_unit ;;
  log)      test_log ;;
  metrics)  test_metrics ;;
  shutdown) test_shutdown ;;
  all)      test_build; test_unit; test_log; test_metrics; test_shutdown ;;
  *) c_red "未知分组: $ONLY (可选: build/unit/log/metrics/shutdown)"; exit 1 ;;
esac

echo
info "测试结果: $PASS 通过, $FAIL 失败"
[ $FAIL -eq 0 ]
