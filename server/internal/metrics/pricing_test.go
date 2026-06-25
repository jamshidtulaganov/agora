package metrics

import "testing"

func TestPriceForModelAliasAnthropicFableAndOpus48(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "claude-fable-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50},
		},
		{
			model: "anthropic/claude-fable-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50},
		},
		{
			model: "claude-opus-4-8",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-4.8", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 25},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}
}

// TestPriceForModelAliasAgoraFreeGLM guards that the Agora free GLM-4-Flash
// base — bare and provider-prefixed, across z.ai gateway spellings —
// resolves to a $0 price instead of the unmapped diagnostic.
func TestPriceForModelAliasAgoraFreeGLM(t *testing.T) {
	for _, model := range []string{
		"glm-4-flash",
		"glm-4.7-flash",
		"zhipuai/glm-4.7-flash",
		"z-ai/glm-4.5-flash",
	} {
		got, ok := PriceForModelAlias(model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", model)
		}
		if got.InputPerM != 0 || got.OutputPerM != 0 || got.CacheReadPerM != 0 || got.CacheWritePerM != 0 {
			t.Errorf("PriceForModelAlias(%q) = %+v, want all-zero free price", model, got)
		}
	}
}

// TestPriceForModelAliasGLMFlashxNotFree guards the \b boundary in the GLM
// flash rule: the paid `glm-4.7-flashx` SKU must NOT be swept into the free
// tier. It has no server-side price row, so it should stay unresolved.
func TestPriceForModelAliasGLMFlashxNotFree(t *testing.T) {
	if _, ok := PriceForModelAlias("glm-4.7-flashx"); ok {
		t.Error("glm-4.7-flashx (paid) wrongly resolved via the free GLM-flash rule")
	}
}
