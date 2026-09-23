package httpapi

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

// Where an effective value came from. The three sources are resolved in this
// order at startup, which is also the order the console shows them in.
const (
	sourceEnv     = "env"
	sourceConfig  = "config"
	sourceDefault = "default"
	sourceBuiltin = "builtin"
)

// effectiveSettingView is one runtime knob as the console sees it: the value
// that is actually in force plus the layer that supplied it. Values are
// rendered as strings so the whole list shares one shape.
type effectiveSettingView struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Source string `json:"source"`
	// Secret marks a row whose value is only reported as set/unset.
	Secret bool `json:"secret,omitempty"`
}

// effectiveSettings describes the settings an operator has to reason about when
// a deployment "does not pick up" a change: compaction budgets, the shell
// compatibility switches and the web-tool mappings. Each row states the value
// in force and whether it came from the environment, config.json or the
// built-in default, which is exactly the question the console could not answer
// before.
func (s *Server) effectiveSettings() []effectiveSettingView {
	cfg := s.store.Config()
	fileKeys := s.store.ConfigFileKeys()
	rows := make([]effectiveSettingView, 0, 9)

	add := func(key, label, configKey, envName, value string) {
		rows = append(rows, effectiveSettingView{
			Key:    key,
			Label:  label,
			Value:  value,
			Source: settingSource(envName, configKey, fileKeys, value),
		})
	}

	add("compactionRecentTokens", "压缩逐字尾部（估算 token）", "compactionRecentTokens",
		"COMPACTION_RECENT_TOKENS", strconv.Itoa(cfg.CompactionRecentTokens))
	add("compactionReasoningEffort", "压缩推理档位", "compactionReasoningEffort",
		"COMPACTION_REASONING_EFFORT", cfg.CompactionReasoningEffort)
	add("compactionMinOutputTokens", "压缩首轮输出预算下限", "compactionMinOutputTokens",
		"COMPACTION_MIN_OUTPUT_TOKENS", strconv.Itoa(cfg.CompactionMinOutputTokens))
	rows = append(rows, effectiveSettingView{
		Key:    "compactionEscalatedCeilingTokens",
		Label:  "压缩重试预算上限（代码常量）",
		Value:  strconv.Itoa(responsesbridge.CompactionEscalatedCeilingTokens),
		Source: sourceBuiltin,
	})
	add("shellCompat", "工具 shell 兼容值", "shellCompat",
		"SHELL_COMPAT", firstNonEmpty(cfg.ShellCompat, "关闭"))
	add("shellCompatEnforce", "强制改写工具 shell 参数", "shellCompatEnforce",
		"SHELL_COMPAT_ENFORCE", yesNo(cfg.ShellCompatEnforce))
	add("webSearchUpstream", "搜索工具映射", "webSearchUpstream",
		"WEB_SEARCH_UPSTREAM", firstNonEmpty(cfg.WebSearchUpstream, "关闭"))
	add("webFetchUpstream", "网页抓取工具映射", "webFetchUpstream",
		"WEB_FETCH_UPSTREAM", firstNonEmpty(cfg.WebFetchUpstream, "关闭"))

	// Credentials are reported as presence only: the console shows which layer
	// supplied them without ever echoing the secret.
	rows = append(rows,
		effectiveSettingView{
			Key: "proxyKey", Label: "代理主密钥（客户端）", Secret: true,
			Value:  presence(cfg.ProxyKey),
			Source: settingSource("PROXY_KEY", "proxyKey", fileKeys, cfg.ProxyKey),
		},
		effectiveSettingView{
			Key: "adminKey", Label: "管理密钥（控制台）", Secret: true,
			Value:  presence(effectiveAdminKey(cfg)),
			Source: settingSource("ADMIN_KEY", "adminKey", fileKeys, cfg.AdminKey),
		},
	)
	return rows
}

// settingSource names the layer that supplied a value: a matching environment
// variable wins, then an explicit key in config.json, otherwise the built-in
// default.
func settingSource(envName, configKey string, fileKeys map[string]bool, effective string) string {
	if raw := strings.TrimSpace(os.Getenv(envName)); raw != "" && environmentMatches(envName, raw, effective) {
		return sourceEnv
	}
	if fileKeys[configKey] {
		return sourceConfig
	}
	return sourceDefault
}

// environmentMatches re-applies the environment's own conversion so an
// unparseable value is not credited for a setting it never produced.
func environmentMatches(envName, raw, effective string) bool {
	switch envName {
	case "SHELL_COMPAT_ENFORCE":
		parsed, err := strconv.ParseBool(raw)
		return err == nil && yesNo(parsed) == effective
	default:
		return strings.EqualFold(strings.TrimSpace(raw), strings.TrimSpace(effective))
	}
}

func effectiveAdminKey(cfg model.Config) string {
	if cfg.AdminKey != "" {
		return cfg.AdminKey
	}
	return cfg.ProxyKey
}

func presence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "未设置"
	}
	return "已设置"
}

func yesNo(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// handleSettings serves the same list the console renders, so a deployment can
// be checked without opening the page.
func (s *Server) handleSettings(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"settings": s.effectiveSettings()})
}
