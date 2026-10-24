package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Gateway capabilities describe a provider's serving model, independently of
// models.dev's model-wide capabilities and of a user's routing requirements.
type GatewayProvider struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Has          []string `json:"has"`
	Quantization string   `json:"quantization,omitempty"`
	Context      int      `json:"context,omitempty"`
	Output       int      `json:"output,omitempty"`
}

type GatewayCatalog struct {
	Model     string            `json:"model"`
	Source    string            `json:"source"`
	FetchedAt int64             `json:"fetchedAt"`
	Providers []GatewayProvider `json:"providers"`
}

type RoutingCheck struct {
	Profile   string   `json:"profile"`
	AccountID string   `json:"accountId"`
	Account   string   `json:"account"`
	Has       []string `json:"has"`
	Targets   []string `json:"targets"`
	Actual    []string `json:"actual"`
	Matched   bool     `json:"matched"`
	Rejected  bool     `json:"rejected"`
	OK        bool     `json:"ok"`
	Current   bool     `json:"current"`
	Error     string   `json:"error,omitempty"`
	Code      string   `json:"code,omitempty"`
	CheckedAt int64    `json:"checkedAt"`
	MS        int64    `json:"ms"`
}

// DefaultRouteProbe is observed without client pins or capability requirements.
// Profiles contain credential digests, never the credential itself.
type DefaultRouteProbe struct {
	Profile   string   `json:"profile"`
	AccountID string   `json:"accountId"`
	Account   string   `json:"account"`
	Pipeline  string   `json:"pipeline"`
	Canonical string   `json:"canonical"`
	Providers []string `json:"providers"`
	Private   bool     `json:"private"`
	Pinnable  bool     `json:"pinnable"`
	Reason    string   `json:"reason,omitempty"`
	CheckedAt int64    `json:"checkedAt"`
	Current   bool     `json:"current"`
}

type RoutingDiscovery struct {
	Profile      string         `json:"profile"`
	SourceConfig string         `json:"sourceConfig"`
	AccountID    string         `json:"accountId"`
	Account      string         `json:"account"`
	Status       string         `json:"status"`
	Reason       string         `json:"reason,omitempty"`
	Config       PerModelConfig `json:"config"`
	CheckProfile string         `json:"checkProfile,omitempty"`
	CheckedAt    int64          `json:"checkedAt"`
	Current      bool           `json:"current"`
	Cached       bool           `json:"cached,omitempty"`
}

var quantizationCondition = regexp.MustCompile(`^!?quantization:[a-z0-9][a-z0-9._-]{0,31}$`)

func NormalizeGatewayHas(values []string) []string {
	var result []string
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func ValidateGatewayHas(values []string) error {
	if len(values) > 16 {
		return fmt.Errorf("能力过滤最多允许 16 项")
	}
	var format string
	for _, value := range values {
		switch value {
		case "free", "vision", "implicit-caching", "reasoning", "tool-use", "structured-output":
		default:
			if !quantizationCondition.MatchString(value) {
				return fmt.Errorf("不支持的能力过滤条件：%s", value)
			}
			if strings.HasPrefix(value, "quantization:") {
				if format != "" && format != value {
					return fmt.Errorf("不能同时要求多种权重量化格式")
				}
				format = value
			}
		}
		if strings.HasPrefix(value, "quantization:") && slices.Contains(values, "!"+value) {
			return fmt.Errorf("量化条件互相冲突：%s", value)
		}
	}
	return nil
}

func (p GatewayProvider) Matches(required []string) bool {
	for _, condition := range required {
		switch {
		case strings.HasPrefix(condition, "!quantization:"):
			if p.Quantization == strings.TrimPrefix(condition, "!quantization:") {
				return false
			}
		case strings.HasPrefix(condition, "quantization:"):
			if p.Quantization != strings.TrimPrefix(condition, "quantization:") {
				return false
			}
		default:
			if !slices.Contains(p.Has, condition) {
				return false
			}
		}
	}
	return true
}

// RoutingProfile binds evidence to the credential and complete configuration.
// Only a digest is persisted; rotating a key invalidates its old evidence.
func RoutingProfile(modelID, base string, account Account, cfg PerModelConfig) string {
	cfg.Upstream = ""
	cfg.GatewayHas = NormalizeGatewayHas(cfg.GatewayHas)
	if cfg.Upstreams == nil {
		cfg.Upstreams = []string{}
	}
	cfg.Exclude = slices.Clone(cfg.Exclude)
	if cfg.Exclude == nil {
		cfg.Exclude = []string{}
	}
	slices.Sort(cfg.Exclude)
	raw, _ := json.Marshal(struct {
		Model, Base, Account, Key string
		Config                    PerModelConfig
	}{modelID, base, account.ID, account.Key, cfg})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
