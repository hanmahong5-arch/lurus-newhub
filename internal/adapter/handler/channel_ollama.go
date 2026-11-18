package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/ollama"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

type ollamaModelActionRequest struct {
	ChannelID int    `json:"channel_id"`
	ModelName string `json:"model_name"`
}

func bindAuthorizedOllamaModelRequest(c *gin.Context) (*repo.Channel, string, string, string, bool) {
	var req ollamaModelActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request parameters"})
		return nil, "", "", "", false
	}
	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel ID and model name are required"})
		return nil, "", "", "", false
	}

	channel, err := repo.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return nil, "", "", "", false
	}
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "This operation is only supported for Ollama channels"})
		return nil, "", "", "", false
	}
	if enforceTenantScope(c, channel.TenantId) {
		return nil, "", "", "", false
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}
	key := strings.Split(channel.Key, "\n")[0]
	return channel, baseURL, key, req.ModelName, true
}

// OllamaPullModel pulls an Ollama model synchronously onto an authorized Ollama channel.
func OllamaPullModel(c *gin.Context) {
	_, baseURL, key, modelName, ok := bindAuthorizedOllamaModelRequest(c)
	if !ok {
		return
	}
	if err := ollama.PullOllamaModel(baseURL, key, modelName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to pull model: %s", err.Error()),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Model %s pulled successfully", modelName),
	})
}

// OllamaPullModelStream streams Ollama model pull progress events via Server-Sent Events.
func OllamaPullModelStream(c *gin.Context) {
	_, baseURL, key, modelName, ok := bindAuthorizedOllamaModelRequest(c)
	if !ok {
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	progressCallback := func(progress ollama.OllamaPullResponse) {
		data, _ := json.Marshal(progress)
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(data))
		c.Writer.Flush()
	}

	if err := ollama.PullOllamaModelStream(baseURL, key, modelName, progressCallback); err != nil {
		errorData, _ := json.Marshal(gin.H{"error": err.Error()})
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(errorData))
	} else {
		successData, _ := json.Marshal(gin.H{"message": fmt.Sprintf("Model %s pulled successfully", modelName)})
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(successData))
	}
	_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

// OllamaDeleteModel deletes a model from an authorized Ollama upstream channel.
func OllamaDeleteModel(c *gin.Context) {
	_, baseURL, key, modelName, ok := bindAuthorizedOllamaModelRequest(c)
	if !ok {
		return
	}
	if err := ollama.DeleteOllamaModel(baseURL, key, modelName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to delete model: %s", err.Error()),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Model %s deleted successfully", modelName),
	})
}

// OllamaVersion queries the daemon version of an authorized Ollama channel.
func OllamaVersion(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel id"})
		return
	}
	channel, err := repo.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return
	}
	if enforceTenantScope(c, channel.TenantId) {
		return
	}
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "This operation is only supported for Ollama channels"})
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}
	key := strings.Split(channel.Key, "\n")[0]
	ver, err := ollama.FetchOllamaVersion(baseURL, key)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("获取Ollama版本失败: %s", err.Error())})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"version": ver}})
}
