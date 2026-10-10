package repo

// ability_modality_test.go - abilities.modality is written whenever a channel's
// abilities are (re)built (migration 053), honouring the administrator
// override in models.modality, and a merely-guessed chat modality is stored as
// "" (unknown) so the router keeps treating it as compatible.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func modalityOf(t *testing.T, channelID int) map[string]string {
	t.Helper()
	var rows []Ability
	if err := DB.Where("channel_id = ?", channelID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Model] = r.Modality
	}
	return out
}

func TestAbilities_WriteInferredModality(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := &Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default",
		Models: "text-embedding-model-a,model-b,tts-model-c,rerank-model-d"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := ch.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	got := modalityOf(t, ch.Id)
	want := map[string]string{
		"text-embedding-model-a": "embedding",
		"model-b":                "", // a defaulted chat guess is stored as unknown
		"tts-model-c":            "audio",
		"rerank-model-d":         "rerank",
	}
	for model, w := range want {
		if got[model] != w {
			t.Errorf("modality[%s] = %q, want %q", model, got[model], w)
		}
	}
}

func TestAbilities_DecisionChannelTypeWinsOverName(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := &Channel{Type: constant.ChannelTypeTypeSafe, Status: common.ChannelStatusEnabled, Group: "default", Models: "embed-model-a"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := ch.UpdateAbilities(nil); err != nil {
		t.Fatal(err)
	}
	if got := modalityOf(t, ch.Id)["embed-model-a"]; got != "decision" {
		t.Fatalf("modality = %q, want decision", got)
	}
}

func TestAbilities_AdminOverrideFromModels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DB.Create(&Model{ModelName: "custom-vectors", Modality: "embedding"}).Error; err != nil {
		t.Fatal(err)
	}
	// A soft-deleted override must not apply.
	if err := DB.Create(&Model{ModelName: "gone-model", Modality: "rerank"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Where("model_name = ?", "gone-model").Delete(&Model{}).Error; err != nil {
		t.Fatal(err)
	}
	ch := &Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default", Models: "custom-vectors,gone-model"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := ch.UpdateAbilities(nil); err != nil {
		t.Fatal(err)
	}
	got := modalityOf(t, ch.Id)
	if got["custom-vectors"] != "embedding" {
		t.Errorf("override not applied: %q", got["custom-vectors"])
	}
	if got["gone-model"] != "" {
		t.Errorf("soft-deleted override applied: %q", got["gone-model"])
	}
}

func TestModalityOverrideCache_RefreshedByChannelCache(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	defer func() { common.MemoryCacheEnabled = prev }()
	SetModalityOverridesForTest(nil)
	defer SetModalityOverridesForTest(nil)

	if err := DB.Create(&Model{ModelName: "custom-vectors", Modality: "rerank"}).Error; err != nil {
		t.Fatal(err)
	}
	if got := ModalityOverride("custom-vectors"); got != "" {
		t.Fatalf("override visible before the cache rebuild: %q", got)
	}
	InitChannelCache()
	if got := ModalityOverride("custom-vectors"); got != "rerank" {
		t.Fatalf("override = %q after rebuild, want rerank", got)
	}
}
