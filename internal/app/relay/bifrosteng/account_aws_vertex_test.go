package bifrosteng

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestBedrockKeyConfig_AKSK(t *testing.T) {
	cfg, err := bedrockKeyConfig(&channelSnap{
		APIKey: "AKIAxxx|SECRETyyy|us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessKey.GetValue() != "AKIAxxx" || cfg.SecretKey.GetValue() != "SECRETyyy" {
		t.Fatalf("ak/sk = %q/%q", cfg.AccessKey.GetValue(), cfg.SecretKey.GetValue())
	}
	if cfg.Region == nil || cfg.Region.GetValue() != "us-east-1" {
		t.Fatalf("region = %#v", cfg.Region)
	}
}

func TestBedrockKeyConfig_APIKey(t *testing.T) {
	cfg, err := bedrockKeyConfig(&channelSnap{
		APIKey: "bedrock-api-key|us-west-2",
		Other:  dto.ChannelOtherSettings{AwsKeyType: dto.AwsKeyTypeApiKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region == nil || cfg.Region.GetValue() != "us-west-2" {
		t.Fatalf("region = %#v", cfg.Region)
	}
}

func TestVertexKeyConfig_JSON(t *testing.T) {
	raw := `{"project_id":"my-proj","client_email":"sa@x","private_key":"k"}`
	cfg, err := vertexKeyConfig(&channelSnap{
		APIKey:     raw,
		ApiVersion: "europe-west1",
		Other:      dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeJSON},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID.GetValue() != "my-proj" {
		t.Fatalf("project=%q", cfg.ProjectID.GetValue())
	}
	if cfg.Region.GetValue() != "europe-west1" {
		t.Fatalf("region=%q", cfg.Region.GetValue())
	}
	if cfg.AuthCredentials.GetValue() != raw {
		t.Fatal("auth credentials should be full SA JSON")
	}
}

func TestCoverageAwsVertexOwned(t *testing.T) {
	if !IsMapped(constant.APITypeAws) || CoverageOf(constant.APITypeAws) != CoverageOwned {
		t.Fatal("APITypeAws should be bifrost-owned after Wave 2")
	}
	if !IsMapped(constant.APITypeVertexAi) || CoverageOf(constant.APITypeVertexAi) != CoverageOwned {
		t.Fatal("APITypeVertexAi should be bifrost-owned after Wave 2")
	}
	if CoverageOf(constant.APITypeBaidu) != CoverageDeferred {
		t.Fatal("Baidu stays deferred")
	}
}
