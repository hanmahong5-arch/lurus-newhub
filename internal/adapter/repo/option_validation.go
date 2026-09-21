package repo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
)

// option_validation.go — validate an option value BEFORE the row is written.
// Split out of option.go by the cycle-13 wiring pass as a pure move (every
// function here is byte-identical to the one that stood in option.go);
// internal/pkg/gates' source-size ratchet holds option.go at its measured
// line count, so this cycle's additions to that file are paid for by a move
// rather than by raising the ceiling.
//
// What stays in option.go on purpose: updateOptionMap (its dispatch is what
// option_validation_gate_test.go derives the numeric/JSON tables from) and
// optionInt/optionFloat (the same gate reads option.go for strconv results
// whose error is thrown away, and those two are where it is handled).

// jsonShape is the generic probe: decode into a throwaway T and report the
// decoder's verdict. It allocates nothing the caller keeps.
func jsonShape[T any](value string) error {
	var probe T
	return json.Unmarshal([]byte(value), &probe)
}

// ValidateOptionValue reports whether value can be applied to key, changing
// nothing at all. UpdateOption calls it before it writes the row.
//
// A key it does not recognise is not an error: most options are free-form
// strings and booleans (`value == "true"`), which cannot fail to parse.
func ValidateOptionValue(key, value string) error {
	if canonical, retired := retiredOptionKeys[key]; retired {
		return fmt.Errorf("%w: %s; write %s instead", errOptionKeyRetired, key, canonical)
	}

	if kind, ok := numericOptionKinds[key]; ok {
		var err error
		var parsedFloat float64
		switch kind {
		case optionKindInteger:
			_, err = strconv.Atoi(value)
		default:
			parsedFloat, err = strconv.ParseFloat(value, 64)
		}
		if err != nil {
			reportOptionParseFailure(key, kind)
			return optionKindError(key, kind)
		}
		if positiveRangeOptionKinds[key] && (parsedFloat <= 0 || parsedFloat >= optionPositiveRangeMax) {
			reportOptionRangeFailure(key)
			return optionRangeError(key)
		}
		return nil
	}

	if kind, ok := jsonOptionKinds[key]; ok {
		probe, known := jsonOptionProbes[key]
		if !known {
			// TestOptionJSONProbesCoverEveryJSONKey keeps the two tables equal;
			// a key that slipped through is refused rather than persisted blind.
			return fmt.Errorf("%w: %s has no shape probe", ErrOptionValueRejected, key)
		}
		if err := probe(value); err != nil {
			reportOptionParseFailure(key, kind)
			return optionKindError(key, kind)
		}
		return nil
	}

	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	cfg := config.GlobalConfig.Get(parts[0])
	if cfg == nil {
		return nil
	}
	if err := config.ValidateConfigValue(cfg, parts[1], value); err != nil {
		// config's error names the expected type and nothing else
		// (config.parseKindError), so it is safe to log and to return.
		metrics.RecordOptionParseRejected(key)
		common.SysError(fmt.Sprintf("option %s rejected: %v; the previous value is kept", key, err))
		return fmt.Errorf("%w: %s %w", ErrOptionValueRejected, key, err)
	}
	return nil
}

// optionKindError is the single shape of a rejection message.
func optionKindError(key string, kind optionValueKind) error {
	return fmt.Errorf("%w: %s must be a valid %s", ErrOptionValueRejected, key, kind)
}

// optionRangeError is optionKindError's counterpart for positiveRangeOptionKinds:
// the value parsed fine but is out of the range that key must stay in. Like
// optionKindError it never quotes the submitted value — this dispatch is
// shared with secret-bearing keys (see ErrOptionValueRejected's comment).
func optionRangeError(key string) error {
	return fmt.Errorf("%w: %s must be greater than 0 and less than %g", ErrOptionValueRejected, key, optionPositiveRangeMax)
}
