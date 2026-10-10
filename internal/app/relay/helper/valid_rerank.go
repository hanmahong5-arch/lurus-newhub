package helper

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// MaxRerankDocuments is the per-request document ceiling.
const MaxRerankDocuments = 2048

// ValidateRerankFields enforces the rerank contract: 1..2048 non-empty
// documents and, when given, 1 <= top_n <= len(documents). top_n larger than
// the document count used to reach the vendor, which each answers differently
// (error, clamp, or silently short list).
func ValidateRerankFields(req *dto.RerankRequest) *types.NewAPIError {
	bad := func(format string, args ...any) *types.NewAPIError {
		return types.NewErrorWithStatusCode(fmt.Errorf(format, args...), types.ErrorCodeInvalidRequest, 400, types.ErrOptionWithSkipRetry())
	}
	n := len(req.Documents)
	if n == 0 {
		return bad("documents is empty")
	}
	if n > MaxRerankDocuments {
		return bad("documents has %d items, the maximum is %d", n, MaxRerankDocuments)
	}
	for i, d := range req.Documents {
		switch v := d.(type) {
		case nil:
			return bad("documents[%d] is empty", i)
		case string:
			if v == "" {
				return bad("documents[%d] is empty", i)
			}
		}
	}
	if req.TopN < 0 || req.TopN > n {
		return bad("top_n must be between 1 and the number of documents (%d)", n)
	}
	return nil
}
