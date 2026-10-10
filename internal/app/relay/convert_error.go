package relay

import (
	"errors"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// convertRequestError maps an adaptor ConvertXxxRequest failure to the API
// error. provider.ErrNotImplemented means the channel's vendor has no such
// endpoint: that is a 501 on this channel type, not a conversion bug, and
// retrying the same request on a sibling channel of the same type cannot
// help, so it is skip-retry.
func convertRequestError(err error) *types.NewAPIError {
	if errors.Is(err, provider.ErrNotImplemented) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeAPINotImplemented, http.StatusNotImplemented, types.ErrOptionWithSkipRetry())
	}
	return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
}
