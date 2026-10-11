package helper

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// Embedding request bounds. The 2048 ceiling matches the batch limit that the
// large hosted embedding vendors enforce; rejecting here turns an opaque
// upstream 400 (billed to nobody, retried across channels) into one local,
// non-retryable 400.
const (
	MaxEmbeddingInputs     = 2048
	MinEmbeddingDimensions = 1
	MaxEmbeddingDimensions = 65536
)

func embeddingBadRequest(format string, args ...any) *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf(format, args...), types.ErrorCodeInvalidRequest, 400, types.ErrOptionWithSkipRetry())
}

// embeddingInputCount returns how many logical inputs `input` holds and
// whether any of them is empty. Accepted shapes follow the OpenAI contract:
// string, []string, []int (one token array) and [][]int.
func embeddingInputCount(input any) (n int, hasEmpty bool, ok bool) {
	switch v := input.(type) {
	case string:
		return 1, v == "", true
	case []string:
		for _, s := range v {
			if s == "" {
				hasEmpty = true
			}
		}
		return len(v), hasEmpty, true
	case []any:
		if len(v) == 0 {
			return 0, false, true
		}
		if _, isNum := v[0].(float64); isNum {
			// A flat list of numbers is ONE pre-tokenised input.
			return 1, false, true
		}
		for _, item := range v {
			switch it := item.(type) {
			case string:
				if it == "" {
					hasEmpty = true
				}
			case []any:
				if len(it) == 0 {
					hasEmpty = true
				}
			default:
				return 0, false, false
			}
		}
		return len(v), hasEmpty, true
	}
	return 0, false, false
}

// ValidateEmbeddingFields enforces the embeddings contract. dimensionsSet says
// whether the client sent the field at all: Dimensions is a plain int with
// omitempty, so an explicit 0 is otherwise indistinguishable from "absent".
// Moderations reuse the embedding DTO, so only input-presence rules apply to
// them (checkLimits=false).
func ValidateEmbeddingFields(req *dto.EmbeddingRequest, dimensionsSet bool, checkLimits bool) *types.NewAPIError {
	if !checkLimits {
		return nil
	}
	n, hasEmpty, ok := embeddingInputCount(req.Input)
	if !ok {
		return embeddingBadRequest("input must be a string, an array of strings, or token arrays")
	}
	if n == 0 {
		return embeddingBadRequest("input must contain at least 1 item")
	}
	if n > MaxEmbeddingInputs {
		return embeddingBadRequest("input has %d items, the maximum is %d", n, MaxEmbeddingInputs)
	}
	if hasEmpty {
		return embeddingBadRequest("input items must not be empty")
	}
	if dimensionsSet || req.Dimensions != 0 {
		if req.Dimensions < MinEmbeddingDimensions || req.Dimensions > MaxEmbeddingDimensions {
			return embeddingBadRequest("dimensions must be between %d and %d", MinEmbeddingDimensions, MaxEmbeddingDimensions)
		}
	}
	switch req.EncodingFormat {
	case "", "float", "base64":
	default:
		return embeddingBadRequest("encoding_format must be one of float, base64")
	}
	if req.InputType != "" && req.Task != "" {
		return embeddingBadRequest("input_type and task are mutually exclusive")
	}
	return nil
}

// embeddingInputTypeChannels / embeddingTaskChannels list the channel types
// whose adaptors actually forward input_type / task upstream. On any other
// channel the converted request would silently drop the field, so the caller
// would be billed for an embedding computed without it.
var (
	embeddingInputTypeChannels = map[int]bool{constant.ChannelTypeVoyage: true}
	embeddingTaskChannels      = map[int]bool{constant.ChannelTypeJina: true}
)

// CheckEmbeddingUnsupportedParams returns 400 unsupported_parameter when the
// request sets input_type/task and the selected channel cannot forward it.
// Callers skip this on pass-through paths, where the raw body is forwarded.
func CheckEmbeddingUnsupportedParams(req *dto.EmbeddingRequest, channelType int) *types.NewAPIError {
	if req.InputType != "" && !embeddingInputTypeChannels[channelType] {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("input_type is not supported by this channel"),
			types.ErrorCodeUnsupportedParameter, 400, types.ErrOptionWithSkipRetry())
	}
	if req.Task != "" && !embeddingTaskChannels[channelType] {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("task is not supported by this channel"),
			types.ErrorCodeUnsupportedParameter, 400, types.ErrOptionWithSkipRetry())
	}
	return nil
}
