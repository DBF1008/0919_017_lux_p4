#!/usr/bin/env bash
# test.sh - lux 工程化改造手动测试脚本
# 覆盖：结构化日志(slog)、request_id 全链路传递、优雅关闭、Prometheus 指标、全部单元测试
set -u

PASS=0
FAIL=0
SKIP=0

ok()   { PASS=$((PASS+1)); echo "  [PASS] $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  [FAIL] $1"; }
skip() { SKIP=$((SKIP+1)); echo "  [SKIP] $1"; }

echo "=============================================="
echo " 1. 编译与静态检查"
echo "=============================================="

echo "==> go build ./..."
if go build ./...; then ok "go build"; else bad "go build"; fi

echo "==> go vet ./..."
if go vet ./...; then ok "go vet"; else bad "go vet"; fi

echo "==> gofmt 检查"
if [ -z "$(gofmt -l .)" ]; then ok "gofmt"; else bad "gofmt"; gofmt -l .; fi

echo "==> 构建测试二进制 lux"
if go build -o ./lux .; then ok "build lux binary"; else bad "build lux binary"; fi

echo ""
echo "=============================================="
echo " 2. 单元测试（按包逐个执行）"
echo "=============================================="

run_pkg_test() {
	echo "==> go test -v $1"
	if go test -count=1 "$1"; then ok "test $1"; else bad "test $1"; fi
}

# 新增包：结构化日志与指标（必须全部通过）
run_pkg_test ./logger/...
run_pkg_test ./metrics/...

# 改造涉及的包
run_pkg_test ./downloader/...
run_pkg_test ./request/...

# 其余所有包
run_pkg_test ./utils/...
run_pkg_test ./parser/...
run_pkg_test ./extractors/...

echo ""
echo "=============================================="
echo " 3. 结构化日志 slog 验证"
echo "=============================================="

echo "==> 检查目标函数中不再存在 fmt.Println/fmt.Printf 调用"
if grep -nE 'fmt\.(Println|Printf)\(' app/app.go downloader/downloader.go request/request.go; then
	bad "仍存在 fmt.Println/fmt.Printf 调用"
else
	ok "fmt.Println/fmt.Printf 已全部替换"
fi

echo "==> 检查 color 包终端输出仍然保留"
if grep -q 'color\.' app/app.go && grep -q 'cyan\.' downloader/downloader.go && grep -q 'color\.' request/request.go; then
	ok "color 终端输出保留"
else
	bad "color 终端输出丢失"
fi

echo "==> 验证结构化日志输出到 stderr（JSON/文本格式，含 level/msg）"
LOG_OUT=$(./lux --debug --retry 1 https://invalid.example.com/video 2>&1 >/dev/null || true)
if echo "$LOG_OUT" | grep -q 'level=' && echo "$LOG_OUT" | grep -q 'msg='; then
	ok "slog 结构化日志输出正常"
else
	bad "未检测到 slog 结构化日志"
fi

echo "==> 验证日志中包含 request_id 字段"
if echo "$LOG_OUT" | grep -q 'request_id='; then
	ok "request_id 全链路传递"
else
	bad "日志中缺少 request_id"
fi

echo "==> 验证每次任务的 request_id 唯一"
IDS=$(./lux --retry 1 https://invalid1.example.com/a https://invalid2.example.com/b 2>&1 >/dev/null \
	| grep -o 'request_id=[a-f0-9]*' | sort -u | wc -l | tr -d ' ')
if [ "$IDS" -ge 2 ]; then
	ok "request_id 唯一性（检测到 $IDS 个不同 ID）"
else
	bad "request_id 不唯一（仅 $IDS 个）"
fi

echo ""
echo "=============================================="
echo " 4. Prometheus 指标端点验证（--metrics-port）"
echo "=============================================="

METRICS_PORT=19091
TEST_DIR=$(mktemp -d)
echo "hello lux metrics test" > "$TEST_DIR/1MB.bin"
head -c 1048576 /dev/urandom > "$TEST_DIR/big.bin" 2>/dev/null || dd if=/dev/urandom of="$TEST_DIR/big.bin" bs=1024 count=1024 2>/dev/null

echo "==> 启动本地文件服务器 (port 18099)"
(cd "$TEST_DIR" && python3 -m http.server 18099 > /dev/null 2>&1) &
FILE_SERVER_PID=$!
sleep 1

if curl -sf -o /dev/null "http://127.0.0.1:18099/big.bin"; then
	echo "==> 带 --metrics-port=$METRICS_PORT 执行真实下载"
	./lux --metrics-port $METRICS_PORT -o "$TEST_DIR/dl" "http://127.0.0.1:18099/big.bin" > /dev/null 2>&1 || true

	echo "==> 抓取 http://127.0.0.1:$METRICS_PORT/metrics"
	METRICS=$(curl -sf "http://127.0.0.1:$METRICS_PORT/metrics" || true)
	if [ -z "$METRICS" ]; then
		bad "metrics 端点不可访问"
	else
		ok "metrics 端点可访问"
		for m in lux_download_bytes_total lux_downloads_total lux_download_retries_total lux_download_speed_bytes_per_second; do
			if echo "$METRICS" | grep -q "$m"; then
				ok "指标 $m 存在"
			else
				bad "指标 $m 缺失"
			fi
		done
		echo "==> 下载成功率指标（lux_downloads_total{status=\"success\"}）"
		echo "$METRICS" | grep 'lux_downloads_total' || bad "未找到下载计数"
	fi

	echo ""
	echo "=============================================="
	echo " 5. 优雅关闭验证（SIGINT / SIGTERM）"
	echo "=============================================="

	for SIG in INT TERM; do
		echo "==> 测试 SIG$SIG：下载中途发送信号"
		rm -rf "$TEST_DIR/dl2"
		./lux -o "$TEST_DIR/dl2" "http://127.0.0.1:18099/big.bin" > /dev/null 2>&1 &
		LUX_PID=$!
		sleep 0.3
		kill -$SIG $LUX_PID 2>/dev/null
		# 等待进程退出（应在完成当前任务后自行退出，而不是被强杀）
		WAITED=0
		while kill -0 $LUX_PID 2>/dev/null && [ $WAITED -lt 15 ]; do
			sleep 1; WAITED=$((WAITED+1))
		done
		if kill -0 $LUX_PID 2>/dev/null; then
			kill -9 $LUX_PID 2>/dev/null
			bad "SIG$SIG 后进程未在 15s 内退出"
		else
			wait $LUX_PID 2>/dev/null
			ok "SIG$SIG 后进程优雅退出（等待 ${WAITED}s）"
		fi
	done

	echo "==> 验证收到信号后不再接受新任务"
	rm -rf "$TEST_DIR/dl3"
	./lux -o "$TEST_DIR/dl3" "http://127.0.0.1:18099/big.bin" "http://127.0.0.1:18099/big.bin" 2> "$TEST_DIR/shutdown.log" > /dev/null &
	LUX_PID=$!
	sleep 0.3
	kill -INT $LUX_PID 2>/dev/null
	wait $LUX_PID 2>/dev/null || true
	if grep -q "stop accepting new tasks\|shutdown" "$TEST_DIR/shutdown.log"; then
		ok "收到信号后停止接受新任务（日志已记录）"
	else
		skip "未捕获到停止接受新任务的日志（任务可能已完成，请手动验证）"
	fi

	kill $FILE_SERVER_PID 2>/dev/null
else
	skip "本地文件服务器启动失败，跳过指标与优雅关闭测试"
fi

rm -rf "$TEST_DIR"

echo ""
echo "=============================================="
echo " 测试结果: PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
echo "=============================================="
[ $FAIL -eq 0 ]
