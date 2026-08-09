package helper

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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

	setTerminalMarkerSeen := func() {
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
					common.SafeGo(func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
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
			if data[:5] != "data:" && data[:6] != "[DONE]" {
				continue
			}
			data = data[5:]
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}
			if !strings.HasPrefix(data, "[DONE]") {
				info.SetFirstResponseTime()

				// 使用超时机制防止写操作阻塞。dataHandler 是各 provider 的闭包,
				// 解析上游 SSE 负载(index-out-of-range 等 panic 的真实来源);用 SafeGo
				// 包裹避免其 panic 绕过外层 recover 崩进程,内层 defer Unlock 先于 recover
				// 执行不泄漏 writeMutex(panic 时不 send done,外层 select 由 WriteTimeout 兜底)。
				done := make(chan bool, 1)
				common.SafeGo(func() {
					writeMutex.Lock()
					defer writeMutex.Unlock()
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
	select {
	case <-ticker.C:
		// 超时处理逻辑
		logger.LogError(c, "streaming timeout")
	case <-stopChan:
		// 正常结束
		logger.LogInfo(c, "streaming finished")
	case <-c.Request.Context().Done():
		// 客户端断开连接
		logger.LogInfo(c, "client disconnected")
	}
}
