package common

// task_submit.go - TaskSubmitReq / TaskInfo and their helpers, moved verbatim
// out of relay_info.go (pure move, cycle 22) so that file stays under its
// source-size ceiling.

import (
	"encoding/json"
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

type TaskSubmitReq struct {
	Prompt         string                 `json:"prompt"`
	Model          string                 `json:"model,omitempty"`
	Mode           string                 `json:"mode,omitempty"`
	Image          string                 `json:"image,omitempty"`
	Images         []string               `json:"images,omitempty"`
	Size           string                 `json:"size,omitempty"`
	Duration       int                    `json:"duration,omitempty"`
	Seconds        string                 `json:"seconds,omitempty"`
	InputReference string                 `json:"input_reference,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

func (t *TaskSubmitReq) GetPrompt() string {
	return t.Prompt
}

func (t *TaskSubmitReq) HasImage() bool {
	return len(t.Images) > 0
}

func (t *TaskSubmitReq) UnmarshalJSON(data []byte) error {
	type Alias TaskSubmitReq
	aux := &struct {
		Metadata json.RawMessage `json:"metadata,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}

	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.Metadata) > 0 {
		var metadataStr string
		if err := common.Unmarshal(aux.Metadata, &metadataStr); err == nil && metadataStr != "" {
			var metadataObj map[string]interface{}
			if err := common.Unmarshal([]byte(metadataStr), &metadataObj); err == nil {
				t.Metadata = metadataObj
				return nil
			}
		}

		var metadataObj map[string]interface{}
		if err := common.Unmarshal(aux.Metadata, &metadataObj); err == nil {
			t.Metadata = metadataObj
		}
	}

	return nil
}
func (t *TaskSubmitReq) UnmarshalMetadata(v any) error {
	metadata := t.Metadata
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata failed: %w", err)
		}
		err = json.Unmarshal(metadataBytes, v)
		if err != nil {
			return fmt.Errorf("unmarshal metadata to target failed: %w", err)
		}
	}
	return nil
}

type TaskInfo struct {
	Code             int    `json:"code"`
	TaskID           string `json:"task_id"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	Url              string `json:"url,omitempty"`
	RemoteUrl        string `json:"remote_url,omitempty"`
	Progress         string `json:"progress,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"` // 用于按倍率计费
	TotalTokens      int    `json:"total_tokens,omitempty"`      // 用于按倍率计费
}

func FailTaskInfo(reason string) *TaskInfo {
	return &TaskInfo{
		Status: "FAILURE",
		Reason: reason,
	}
}
