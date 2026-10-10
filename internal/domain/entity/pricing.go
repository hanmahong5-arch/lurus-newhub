package entity

import "github.com/LurusTech/lurus-hub/internal/pkg/constant"

type Pricing struct {
	ModelName              string                  `json:"model_name"`
	Description            string                  `json:"description,omitempty"`
	Icon                   string                  `json:"icon,omitempty"`
	Tags                   string                  `json:"tags,omitempty"`
	VendorID               int                     `json:"vendor_id,omitempty"`
	QuotaType              int                     `json:"quota_type"`
	ModelRatio             float64                 `json:"model_ratio"`
	ModelPrice             float64                 `json:"model_price"`
	OwnerBy                string                  `json:"owner_by"`
	CompletionRatio        float64                 `json:"completion_ratio"`
	EnableGroup            []string                `json:"enable_groups"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
	// Modality is the administrator override, else the most common modality
	// recorded on the model's abilities rows ("" = unknown).
	Modality string `json:"modality,omitempty"`
	// UsageUnit is "search_unit" when a SearchUnitPrice entry exists for the
	// model (even 0), otherwise "token". Filled per request by
	// repo.GetPricingForTenant from the live option, never cached.
	UsageUnit string `json:"usage_unit,omitempty"`
	// SearchUnitPrice is nil (omitted) when unconfigured and a pointer to 0
	// when explicitly free: the two must stay distinguishable.
	SearchUnitPrice *float64 `json:"search_unit_price,omitempty"`
}

type PricingVendor struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}
