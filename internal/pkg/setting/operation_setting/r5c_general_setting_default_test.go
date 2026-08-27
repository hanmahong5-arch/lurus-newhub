package operation_setting

// r5c_general_setting_default_test.go — locks G4d in the 2026-08-26 live UAT
// gap report: this codebase serves a commercial production domain
// (hub.lurus.cn), and the package-level default for DocsLink used to be the
// upstream open-source project's own docs site ("https://docs.newapi.pro"),
// leaked into GetStatus()'s public `docs_link` field. That is a white-label
// leak with no operator opt-out short of editing Go source, since the
// options table has zero rows overriding it in production (verified via the
// live UAT report referenced above). The default must stay empty so the
// frontend hides the Docs entry (see general_setting.go's comment on
// generalSetting for the exact frontend files that guard on emptiness)
// instead of pointing customers at a third party's site.

import "testing"

func TestR5CGeneralSettingDefault_DocsLinkIsEmpty(t *testing.T) {
	got := GetGeneralSetting().DocsLink
	if got != "" {
		t.Errorf("GetGeneralSetting().DocsLink = %q, want empty string (white-label leak: must not default to the upstream project's docs site)", got)
	}
}
