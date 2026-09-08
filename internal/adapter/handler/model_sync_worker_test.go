package handler

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/stretchr/testify/assert"
)

func TestAutoSyncChannelModelsWithContext_InvalidFrequency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test with invalid frequency (should return immediately)
	done := make(chan bool)
	go func() {
		AutoSyncChannelModelsWithContext(ctx, 0)
		done <- true
	}()

	// Should complete immediately
	select {
	case <-done:
		// Success - function returned
	case <-time.After(1 * time.Second):
		t.Fatal("Function did not return with invalid frequency")
	}
}

func TestAutoSyncChannelModelsWithContext_NegativeFrequency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test with negative frequency (should return immediately)
	done := make(chan bool)
	go func() {
		AutoSyncChannelModelsWithContext(ctx, -5)
		done <- true
	}()

	// Should complete immediately
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Function did not return with negative frequency")
	}
}

func TestAutoSyncChannelModelsWithContext_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Start worker with very long frequency
	done := make(chan bool)
	go func() {
		AutoSyncChannelModelsWithContext(ctx, 60) // 60 minutes
		done <- true
	}()

	// Cancel context immediately
	cancel()

	// Should stop within 1 second
	select {
	case <-done:
		// Success - context cancellation worked
	case <-time.After(2 * time.Second):
		t.Fatal("Worker did not stop after context cancellation")
	}
}

func TestBuildModelsURL_OpenAI(t *testing.T) {
	baseURL := "https://api.openai.com"
	result := buildModelsURL(constant.ChannelTypeOpenAI, baseURL)

	expected := "https://api.openai.com/v1/models"
	assert.Equal(t, expected, result)
}

func TestBuildModelsURL_Gemini(t *testing.T) {
	baseURL := "https://generativelanguage.googleapis.com"
	result := buildModelsURL(constant.ChannelTypeGemini, baseURL)

	expected := "https://generativelanguage.googleapis.com/v1beta/openai/models"
	assert.Equal(t, expected, result)
}

func TestBuildModelsURL_Ali(t *testing.T) {
	baseURL := "https://dashscope.aliyuncs.com"
	result := buildModelsURL(constant.ChannelTypeAli, baseURL)

	expected := "https://dashscope.aliyuncs.com/compatible-mode/v1/models"
	assert.Equal(t, expected, result)
}

func TestBuildModelsURL_Zhipu(t *testing.T) {
	baseURL := "https://open.bigmodel.cn"
	result := buildModelsURL(constant.ChannelTypeZhipu_v4, baseURL)

	expected := "https://open.bigmodel.cn/api/paas/v4/models"
	assert.Equal(t, expected, result)
}

func TestBuildModelsURL_VolcEngine(t *testing.T) {
	baseURL := "https://ark.cn-beijing.volces.com"
	result := buildModelsURL(constant.ChannelTypeVolcEngine, baseURL)

	expected := "https://ark.cn-beijing.volces.com/v1/models"
	assert.Equal(t, expected, result)
}

func TestBuildModelsURL_Moonshot(t *testing.T) {
	baseURL := "https://api.moonshot.cn"
	result := buildModelsURL(constant.ChannelTypeMoonshot, baseURL)

	expected := "https://api.moonshot.cn/v1/models"
	assert.Equal(t, expected, result)
}

func TestBuildModelsURL_AllChannelTypes(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		baseURL     string
		expected    string
	}{
		{
			name:        "OpenAI",
			channelType: constant.ChannelTypeOpenAI,
			baseURL:     "https://api.openai.com",
			expected:    "https://api.openai.com/v1/models",
		},
		{
			name:        "Azure",
			channelType: constant.ChannelTypeAzure,
			baseURL:     "https://api.azure.com",
			expected:    "https://api.azure.com/v1/models",
		},
		{
			name:        "Anthropic",
			channelType: constant.ChannelTypeAnthropic,
			baseURL:     "https://api.anthropic.com",
			expected:    "https://api.anthropic.com/v1/models",
		},
		{
			name:        "Gemini",
			channelType: constant.ChannelTypeGemini,
			baseURL:     "https://generativelanguage.googleapis.com",
			expected:    "https://generativelanguage.googleapis.com/v1beta/openai/models",
		},
		{
			name:        "Ali",
			channelType: constant.ChannelTypeAli,
			baseURL:     "https://dashscope.aliyuncs.com",
			expected:    "https://dashscope.aliyuncs.com/compatible-mode/v1/models",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildModelsURL(tt.channelType, tt.baseURL)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Renamed off the TestFetchAndMergeModels_ prefix: that group used to mix
// two real assertions with three t.Skip-only pseudo-tests (never executed,
// TI-1). The pseudo-tests are gone; these two keep their coverage.
func TestModelSyncFetch_NoBaseURL(t *testing.T) {
	// Use a valid channel type but clear its base URL
	channel := &repo.Channel{
		Type: constant.ChannelTypeOpenAI,
	}

	// Save original base URL
	originalURL := constant.ChannelBaseURLs[constant.ChannelTypeOpenAI]
	// Clear base URL to trigger error
	constant.ChannelBaseURLs[constant.ChannelTypeOpenAI] = ""

	newModels, err := fetchAndMergeModels(channel)

	assert.Nil(t, newModels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no base URL")

	// Restore original URL
	constant.ChannelBaseURLs[constant.ChannelTypeOpenAI] = originalURL
}

func TestModelSyncFetch_NoAvailableKey(t *testing.T) {
	// Save and restore original base URL
	originalURL := constant.ChannelBaseURLs[constant.ChannelTypeOpenAI]
	defer func() {
		constant.ChannelBaseURLs[constant.ChannelTypeOpenAI] = originalURL
	}()
	constant.ChannelBaseURLs[constant.ChannelTypeOpenAI] = "https://api.openai.com"

	channel := &repo.Channel{
		Type: constant.ChannelTypeOpenAI,
	}
	// Enable multi-key mode with no keys to trigger "no available key" error
	channel.ChannelInfo.IsMultiKey = true
	channel.Key = ""

	newModels, err := fetchAndMergeModels(channel)

	// Should fail because no key available
	assert.Nil(t, newModels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no available key")
}

// Benchmark test for concurrent channel sync
func BenchmarkSyncAllChannelModels(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping benchmark in short mode")
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		syncAllChannelModels(ctx)
	}
}

// Test the worker lifecycle
func TestModelSyncWorker_Lifecycle(t *testing.T) {
	tests := []struct {
		name      string
		frequency int
		testTime  time.Duration
		wantTicks int
	}{
		{
			name:      "invalid_frequency",
			frequency: 0,
			testTime:  100 * time.Millisecond,
			wantTicks: 0, // Should exit immediately
		},
		{
			name:      "negative_frequency",
			frequency: -1,
			testTime:  100 * time.Millisecond,
			wantTicks: 0, // Should exit immediately
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.testTime)
			defer cancel()

			done := make(chan bool)
			go func() {
				AutoSyncChannelModelsWithContext(ctx, tt.frequency)
				done <- true
			}()

			// Wait for completion or timeout
			select {
			case <-done:
				// Success
			case <-time.After(tt.testTime + 100*time.Millisecond):
				if tt.wantTicks > 0 {
					t.Fatal("Worker did not complete in expected time")
				}
			}
		})
	}
}

// Test for proper resource cleanup
func TestModelSyncWorker_ResourceCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Start worker
	done := make(chan bool)
	go func() {
		AutoSyncChannelModelsWithContext(ctx, 60)
		done <- true
	}()

	// Cancel immediately
	cancel()

	// Wait for cleanup
	select {
	case <-done:
		// Success - resources cleaned up
	case <-time.After(2 * time.Second):
		t.Fatal("Worker did not clean up resources")
	}
}
