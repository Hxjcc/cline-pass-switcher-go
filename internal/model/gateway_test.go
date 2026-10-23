package model

import (
	"slices"
	"testing"
)

func TestGatewayCapabilityConditions(t *testing.T) {
	if !slices.Equal(NormalizeGatewayHas([]string{" Vision ", "vision", "implicit-caching"}), []string{"implicit-caching", "vision"}) {
		t.Fatal("capabilities were not normalized")
	}
	for _, values := range [][]string{{"open-weights"}, {"unknown"}, {"quantization:fp8", "!quantization:fp8"}, {"quantization:fp8", "quantization:int4"}} {
		if ValidateGatewayHas(values) == nil {
			t.Fatalf("invalid filter accepted: %v", values)
		}
	}
	provider := GatewayProvider{Has: []string{"vision", "implicit-caching"}}
	if !provider.Matches([]string{"vision", "!quantization:fp8"}) || provider.Matches([]string{"quantization:fp8"}) || provider.Matches([]string{"free"}) {
		t.Fatal("unknown quantization or absent capability was guessed")
	}
	a := PerModelConfig{Upstreams: []string{"deepseek"}, GatewayHas: []string{"vision", "implicit-caching"}}
	b := PerModelConfig{Upstream: "deepseek", Upstreams: []string{"deepseek"}, Exclude: []string{}, GatewayHas: []string{"implicit-caching", "vision"}}
	account := Account{ID: "one", Key: "secret"}
	if RoutingProfile("m", "base", account, a) != RoutingProfile("m", "base", account, b) {
		t.Fatal("equivalent policies produced different evidence keys")
	}
	account.Key = "rotated"
	if RoutingProfile("m", "base", account, a) == RoutingProfile("m", "base", Account{ID: "one", Key: "secret"}, a) {
		t.Fatal("rotated key reused evidence")
	}
}
