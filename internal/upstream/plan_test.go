package upstream

import (
	"slices"
	"testing"
)

func TestParsePlannedProviders(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want []string
	}{
		{
			name: "execution order wins over the system credential list",
			plan: "System credentials planned for: deepseek, alibaba, baseten, fireworks. Total execution order: deepseek(system) → alibaba(system) → baseten(system) → fireworks(system)",
			want: []string{"deepseek", "alibaba", "baseten", "fireworks"},
		},
		{
			name: "vmc single provider",
			plan: "Routed via VMC 'deepseek-v4-flash-contributor-fallbacks' → private/deepseek-v4-flash-contributor. System credentials planned for: openai-compatible-private. Total execution order: openai-compatible-private(system)",
			want: []string{"openai-compatible-private"},
		},
		{
			name: "system credential list fallback",
			plan: "System credentials planned for: alpha, beta.",
			want: []string{"alpha", "beta"},
		},
		{
			name: "unrecognised plan",
			plan: "Z.AI won tier 0 over atlas-cloud.",
			want: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parsePlannedProviders(test.plan)
			if !slices.Equal(got, test.want) {
				t.Fatalf("parsePlannedProviders() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func boolPtr(value bool) *bool {
	return &value
}
