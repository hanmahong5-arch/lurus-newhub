package helper

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return config.Get().Relay.StreamScannerMaxBuffer
}

// streamEndReasonForStop classifies why a stream ended, for the case where the
// upstream read loop finished (stopChan closed).
//
// It takes the request context's error rather than reading a channel because
// the alternative is a race with a wrong answer, not a missed event. A caller
// that hangs up mid-stream normally ends the upstream read as well, so
// stopChan and ctx.Done() become ready at the same moment — and Go's select
// picks among ready cases at RANDOM. The reason recorded for one physical
// event was therefore a coin flip.
//
// The losing side of that flip is the expensive one. A client_gone stream
// recorded as upstream_closed writes an ERR line blaming the provider for a
// customer pressing Ctrl-C, and that is precisely the series the planned
// upstream-error alerting reads: the false pages would arrive in proportion
// to how many callers disconnect.
//
// Reproducing the old behaviour takes one line: insert a
// time.Sleep(30*time.Millisecond) immediately before the select in
// StreamScannerHandler, which guarantees both cases are ready, and
// TestOaiStreamHandler_IncompleteStream_ClientGoneWritesNothing fails on
// random wires. Without the sleep the local rate is about 2 in 900 trials;
// under -race in CI it is high enough to fail a run outright.
//
// An empty return means "nothing to report": the upstream delivered its
// terminator, so the stream ended normally and there is no incomplete-stream
// reason to record.
func streamEndReasonForStop(ctxErr error, terminalSeen bool) string {
	if terminalSeen {
		return ""
	}
	if ctxErr != nil {
		return relaycommon.StreamEndClientGone
	}
	return relaycommon.StreamEndUpstreamClosed
}

// StreamScannerHandler scans an SSE response body and invokes dataHandler per event.
// sawTerminalMarker is an optional out-param (variadic so existing callers are
// unaffected): when provided, it is set to true only if a "[DONE]" terminator was
// actually observed before the scan loop exited. Callers that need to distinguish a
// clean stream end from an abnormal one (ctx cancel, timeout, upstream EOF without
// [DONE]) can pass a *bool and check it after this call returns.
func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string) bool, sawTerminalMarker ...*bool) {

	if resp == nil || dataHandler == nil {
		return
	}

	var terminalSeen atomic.Bool
	setTerminalMarkerSeen := func() {
		terminalSeen.Store(true)
		if len(sawTerminalMarker) > 0 && sawTerminalMarker[0] != nil {
			*sawTerminalMarker[0] = true
		}
	}

	// 确保响应体总是被关闭
	defer func() {
		if resp.Body != nil {
			resp.Body.Close()
		}
	}()

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	relayCfg := config.Get().Relay

	var (
		stopChan   = make(chan bool, relayCfg.StopChannelBuffer)
		scanner    = bufio.NewScanner(resp.Body)
		ticker     = time.NewTicker(streamingTimeout)
		pingTicker *time.Ticker
		writeMutex sync.Mutex     // Mutex to protect concurrent writes
		wg         sync.WaitGroup // 用于等待所有 goroutine 退出
		// detached 在本函数返回前置位:此后 c 会被 gin 放回 sync.Pool 复用,
		// 任何仍存活的写 goroutine 必须短路,否则会写进下一个请求的连接。
		detached atomic.Bool
	)

	generalSettings := operation_setting.GetGeneralSetting()
	pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = relayCfg.PingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	if common.DebugEnabled {
		// print timeout and ping interval for debugging
		println("relay timeout seconds:", common.RelayTimeout)
		println("relay max idle conns:", common.RelayMaxIdleConns)
		println("relay max idle conns per host:", common.RelayMaxIdleConnsPerHost)
		println("streaming timeout seconds:", int64(streamingTimeout.Seconds()))
		println("ping interval seconds:", int64(pingInterval.Seconds()))
	}

	// 改进资源清理，确保所有 goroutine 正确退出
	defer func() {
		// 通知所有 goroutine 停止
		common.SafeSendBool(stopChan, true)

		ticker.Stop()
		if pingTicker != nil {
			pingTicker.Stop()
		}

		// 等待所有 goroutine 退出，最多等待5秒
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(relayCfg.GoroutineShutdownTimeout):
			logger.LogError(c, "timeout waiting for goroutines to exit")
		}

		// 等待超时意味着还有写 goroutine 存活;置位让它们在真正落笔前退出。
		// c.Request.Context() 兜不住这条路径——Context 被复用后读到的是下一个
		// 请求的活 context,永远不是 Done。
		detached.Store(true)

		close(stopChan)
	}()

	scanner.Buffer(make([]byte, relayCfg.StreamScannerInitialBuffer), getScannerBufferSize())
	scanner.Split(bufio.ScanLines)
	SetEventStreamHeaders(c, info)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		gopool.Go(func() {
			// wg.Done() must be the LAST thing this defer does: the cleanup
			// goroutine closes stopChan as soon as wg.Wait() returns, so
			// releasing the counter before the SafeSendBool below lets the close
			// interleave with the send. SafeSendBool recovers the resulting
			// panic, which is why this was silent in production, but it is a
			// real data race and -race fails on it.
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("ping goroutine panic: %v", r))
					common.SafeSendBool(stopChan, true)
				}
				if common.DebugEnabled {
					println("ping goroutine exited")
				}
			}()

			// 添加超时保护，防止 goroutine 无限运行
			pingTimeout := time.NewTimer(relayCfg.MaxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					// 使用超时机制防止写操作阻塞。用 SafeGo 包裹:PingData 直接写
					// 客户端连接,panic 若不 recover 会绕过外层 goroutine 的恢复直接崩进程;
					// 内层 defer Unlock 在 panic 展开时先于 recover 执行,不会泄漏 writeMutex。
					done := make(chan error, 1)
					wg.Add(1)
					common.SafeGo(func() {
						defer wg.Done()
						writeMutex.Lock()
						defer writeMutex.Unlock()
						if detached.Load() {
							return
						}
						done <- PingData(c)
					})

					select {
					case err := <-done:
						if err != nil {
							logger.LogError(c, "ping data error: "+err.Error())
							return
						}
						if common.DebugEnabled {
							println("ping data sent")
						}
					case <-time.After(relayCfg.WriteTimeout):
						logger.LogError(c, "ping data send timeout")
						return
					case <-ctx.Done():
						return
					case <-stopChan:
						return
					}
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-c.Request.Context().Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, "ping goroutine max duration reached")
					return
				}
			}
		})
	}

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		// Same ordering requirement as the ping goroutine above: the counter is
		// released only after the stopChan send, or the cleanup's
		// wg.Wait()/close(stopChan) races this send.
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("scanner goroutine panic: %v", r))
			}
			common.SafeSendBool(stopChan, true)
			if common.DebugEnabled {
				println("scanner goroutine exited")
			}
		}()

		for scanner.Scan() {
			// 检查是否需要停止
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			case <-c.Request.Context().Done():
				return
			default:
			}

			ticker.Reset(streamingTimeout)
			data := scanner.Text()
			if common.DebugEnabled {
				println(data)
			}

			if len(data) < 6 {
				continue
			}
			// 终止符可能以裸 "[DONE]" 或 "data: [DONE]" 两种形态到达,必须先判定
			// 再剥前缀:无条件剥 5 字节会把裸 "[DONE]" 削成 "]" 当负载下发,终止
			// 标记随之丢失,调用方据此把真实 usage 判为不可信并按 0 计费。
			isTerminal := strings.HasPrefix(data, "[DONE]")
			if !isTerminal {
				if !strings.HasPrefix(data, "data:") {
					continue
				}
				// 剥 5 字节再 TrimSpace,"data:x" 与 "data: x" 同样可解析。
				data = strings.TrimSpace(data[5:])
				if data == "" {
					continue
				}
				isTerminal = strings.HasPrefix(data, "[DONE]")
			}
			if !isTerminal {
				info.SetFirstResponseTime()

				// 使用超时机制防止写操作阻塞。dataHandler 是各 provider 的闭包,
				// 解析上游 SSE 负载(index-out-of-range 等 panic 的真实来源);用 SafeGo
				// 包裹避免其 panic 绕过外层 recover 崩进程,内层 defer Unlock 先于 recover
				// 执行不泄漏 writeMutex(panic 时不 send done,外层 select 由 WriteTimeout 兜底)。
				done := make(chan bool, 1)
				// wg 计入这个内层 writer:它经 c 直接写客户端,若不等它退出,
				// 本函数返回后 gin 回收 Context,晚到的写就打到别人的 socket 上。
				wg.Add(1)
				common.SafeGo(func() {
					defer wg.Done()
					writeMutex.Lock()
					defer writeMutex.Unlock()
					if detached.Load() {
						return
					}
					done <- dataHandler(data)
				})

				select {
				case success := <-done:
					if !success {
						return
					}
				case <-time.After(relayCfg.WriteTimeout):
					logger.LogError(c, "data handler timeout")
					return
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				}
			} else {
				// done, 处理完成标志，直接退出停止读取剩余数据防止出错
				setTerminalMarkerSeen()
				if common.DebugEnabled {
					println("received [DONE], stopping scanner")
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				logger.LogError(c, "scanner error: "+err.Error())
			}
		}
	})

	// 主循环等待完成或超时
	// The reason is recorded on info for the handlers: an upstream that
	// stops without its terminator must reach the caller as an error frame,
	// not as an invented normal end (openai.HandleIncompleteStream,
	// claude.HandleStreamFinalResponse).
	select {
	case <-ticker.C:
		// 超时处理逻辑
		logger.LogError(c, "streaming timeout")
		info.StreamEndReason = relaycommon.StreamEndTimeout
	case <-stopChan:
		// 正常结束
		logger.LogInfo(c, "streaming finished")
		info.StreamEndReason = streamEndReasonForStop(c.Request.Context().Err(), terminalSeen.Load())
	case <-c.Request.Context().Done():
		// 客户端断开连接
		logger.LogInfo(c, "client disconnected")
		info.StreamEndReason = relaycommon.StreamEndClientGone
	}
}
