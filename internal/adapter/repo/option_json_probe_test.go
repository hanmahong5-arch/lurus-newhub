package repo

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// TestOptionJSONProbesCoverEveryJSONKey keeps jsonOptionProbes and
// jsonOptionKinds the same set: a JSON key with a kind but no shape probe would
// be refused outright by ValidateOptionValue ("has no shape probe"), and a
// probe for a key that is no longer a JSON updater is dead weight.
func TestOptionJSONProbesCoverEveryJSONKey(t *testing.T) {
	var problems []string
	for key := range jsonOptionKinds {
		if _, ok := jsonOptionProbes[key]; !ok {
			problems = append(problems, key+" is a JSON option kind but has no shape probe")
		}
	}
	for key := range jsonOptionProbes {
		if _, ok := jsonOptionKinds[key]; !ok {
			problems = append(problems, key+" has a shape probe but is not a JSON option kind")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("jsonOptionProbes and jsonOptionKinds disagree:\n  %s", strings.Join(problems, "\n  "))
	}
	if len(jsonOptionProbes) < 10 {
		t.Fatalf("only %d JSON probes — the table shrank, not the codebase", len(jsonOptionProbes))
	}
}

// TestOptionJSONProbesRejectWrongShape drives EVERY probe with a document that
// is valid JSON but the wrong shape for its updater, and with one that is the
// right shape. Before cycle 12's hand-finish the JSON keys were validated by
// json.Valid alone, so {"a":"x"} for a map[string]float64 key was persisted
// first and refused by the updater afterwards — for five of these keys after
// the updater had already emptied the live table. Mutation that turns this
// red: put `json.Valid` back in place of the probe in ValidateOptionValue.
func TestOptionJSONProbesRejectWrongShape(t *testing.T) {
	cases := map[string]struct{ wrong, right string }{
		"Chats":                      {`{"a":"x"}`, `[{"name":"n","url":"https://x.example"}]`},
		"AutoGroups":                 {`{"a":"x"}`, `["default","vip"]`},
		"TopupGroupRatio":            {`{"a":"x"}`, `{"default":1.5}`},
		"ModelRequestRateLimitGroup": {`{"a":"x"}`, `{"default":[10,20]}`},
		"ModelRatio":                 {`{"a":"x"}`, `{"m":2}`},
		"GroupRatio":                 {`[1,2]`, `{"default":1}`},
		"GroupGroupRatio":            {`{"a":1}`, `{"g":{"default":1}}`},
		"UserUsableGroups":           {`{"a":1}`, `{"default":"default group"}`},
		"CompletionRatio":            {`{"a":"x"}`, `{"m":1.2}`},
		"ModelPrice":                 {`[1]`, `{"m":0.5}`},
		"CacheRatio":                 {`{"a":"x"}`, `{"m":0.5}`},
		"ContextLengthTiers":         {`{"a":"x"}`, `{}`},
		"ImageRatio":                 {`{"a":"x"}`, `{"m":1}`},
		"AudioRatio":                 {`{"a":"x"}`, `{"m":1}`},
		"AudioCompletionRatio":       {`{"a":"x"}`, `{"m":1}`},
	}
	for key := range jsonOptionProbes {
		if _, ok := cases[key]; !ok {
			t.Errorf("no wrong/right document for probe %s — add one so the probe is exercised", key)
		}
	}
	for key, c := range cases {
		err := ValidateOptionValue(key, c.wrong)
		if err == nil {
			t.Errorf("%s: wrong-shape document %s was accepted", key, c.wrong)
		} else if !errors.Is(err, ErrOptionValueRejected) {
			t.Errorf("%s: rejection %v does not wrap ErrOptionValueRejected", key, err)
		} else if strings.Contains(err.Error(), c.wrong) {
			t.Errorf("%s: the rejection quotes the submitted value: %q", key, err.Error())
		}
		if err := ValidateOptionValue(key, c.right); err != nil {
			t.Errorf("%s: right-shape document %s was refused: %v", key, c.right, err)
		}
	}
}

// TestOptionParse_WrongShapeJSONIsRefusedBeforeItIsPersisted is the persist
// half: the admin write path must leave the options row untouched.
func TestOptionParse_WrongShapeJSONIsRefusedBeforeItIsPersisted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	restoreOptionMapForTest(t)

	if err := UpdateOption("UserUsableGroups", `{"default":"default group"}`); err != nil {
		t.Fatalf("UpdateOption(valid): %v", err)
	}
	err := UpdateOption("UserUsableGroups", `[1,2]`)
	if err == nil {
		t.Fatal("UpdateOption accepted a wrong-shape UserUsableGroups document")
	}
	if !errors.Is(err, ErrOptionValueRejected) {
		t.Errorf("error %v does not wrap ErrOptionValueRejected", err)
	}
	stored, found, storeErr := GetOptionValue(DB, "UserUsableGroups")
	if storeErr != nil {
		t.Fatalf("read back: %v", storeErr)
	}
	if !found || stored != `{"default":"default group"}` {
		t.Errorf("stored UserUsableGroups = %q (found=%v), want the previous valid document", stored, found)
	}
}
