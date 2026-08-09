package search

import "testing"

// TestConvertDocumentToLog_KeepsGovernanceFields guards the reverse mapping of
// the governance fields (ChannelType/RelayMode/UpstreamModel/TotalLatencyMs):
// they were indexed by ConvertLogToDocument but never copied back, so every
// search hit surfaced them as zero values to the console API.
func TestConvertDocumentToLog_KeepsGovernanceFields(t *testing.T) {
	doc := &LogDocument{
		ID:               7,
		CreatedAt:        1753400000,
		Type:             2,
		UserID:           11,
		Username:         "alice",
		TokenID:          22,
		TokenName:        "tok",
		ModelName:        "gpt-4o",
		Content:          "content",
		Quota:            96,
		PromptTokens:     10,
		CompletionTokens: 20,
		UseTime:          3,
		IsStream:         true,
		ChannelID:        33,
		ChannelName:      "chan",
		Group:            "default",
		IP:               "10.0.0.1",
		ChannelType:      14,
		RelayMode:        5,
		UpstreamModel:    "gpt-4o-2024-08-06",
		TotalLatencyMs:   1234,
	}

	got := ConvertDocumentToLog(doc)

	if got.ChannelType != doc.ChannelType {
		t.Errorf("ChannelType = %d, want %d", got.ChannelType, doc.ChannelType)
	}
	if got.RelayMode != doc.RelayMode {
		t.Errorf("RelayMode = %d, want %d", got.RelayMode, doc.RelayMode)
	}
	if got.UpstreamModel != doc.UpstreamModel {
		t.Errorf("UpstreamModel = %q, want %q", got.UpstreamModel, doc.UpstreamModel)
	}
	if got.TotalLatencyMs != doc.TotalLatencyMs {
		t.Errorf("TotalLatencyMs = %d, want %d", got.TotalLatencyMs, doc.TotalLatencyMs)
	}
}

// TestConvertLogToDocumentRoundTrip_LosslessForIndexedFields checks the full
// round trip Log -> LogDocument -> Log for every field the index carries.
// Log.Other has no counterpart in LogDocument, so it is intentionally excluded.
func TestConvertLogToDocumentRoundTrip_LosslessForIndexedFields(t *testing.T) {
	original := &Log{
		Id:               7,
		CreatedAt:        1753400000,
		Type:             2,
		UserId:           11,
		Username:         "alice",
		TokenId:          22,
		TokenName:        "tok",
		ModelName:        "gpt-4o",
		Content:          "content",
		Quota:            96,
		PromptTokens:     10,
		CompletionTokens: 20,
		UseTime:          3,
		IsStream:         true,
		ChannelId:        33,
		ChannelName:      "chan",
		Group:            "default",
		Ip:               "10.0.0.1",
		ChannelType:      14,
		RelayMode:        5,
		UpstreamModel:    "gpt-4o-2024-08-06",
		TotalLatencyMs:   1234,
	}

	got := ConvertDocumentToLog(ConvertLogToDocument(original))

	want := *original
	want.Other = "" // not carried by LogDocument
	if *got != want {
		t.Errorf("round trip lost data:\n got  %+v\n want %+v", *got, want)
	}
}
