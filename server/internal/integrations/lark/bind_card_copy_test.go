package lark

import (
	"strings"
	"testing"
)

func TestBindCardCopy_RegionLanguage(t *testing.T) {
	// Lark International → English (SD's RU/UZ audience reads English, not Chinese).
	body, button := bindCardCopy(RegionLark)
	if !strings.Contains(body, "Agora") || !strings.Contains(strings.ToLower(button), "link") {
		t.Errorf("RegionLark should yield English copy, got body=%q button=%q", body, button)
	}
	if strings.ContainsAny(body, "你绑定") {
		t.Errorf("RegionLark copy must not contain Chinese: %q", body)
	}

	// Mainland Feishu → Chinese.
	zhBody, zhButton := bindCardCopy(RegionFeishu)
	if !strings.Contains(zhBody, "绑定") || zhButton != "去绑定" {
		t.Errorf("RegionFeishu should yield Chinese copy, got body=%q button=%q", zhBody, zhButton)
	}

	// Empty region defaults to Feishu (existing RegionOrDefault back-compat).
	emptyBody, _ := bindCardCopy("")
	if !strings.Contains(emptyBody, "绑定") {
		t.Errorf("empty region should default to Feishu/Chinese, got %q", emptyBody)
	}
}
