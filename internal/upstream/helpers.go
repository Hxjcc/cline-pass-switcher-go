package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/apierr"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
)

const maxResponseBytes = 64 << 20

type Routing struct {
	Content           string
	Pipeline          string
	CanonicalSlug     string
	FinalProvider     string
	FinalProviderName string
	Fallbacks         []string
	Plan              string
}

type Attempt struct {
	Upstream    string
	OrderRest   []string
	ExcludeList []string
	Strict      bool
	Sort        string
}

type AttemptResult struct {
	Status  int
	Out     map[string]any
	Routing Routing
	NetErr  string
	Account model.Account
	// Fatal marks a failure no other channel or account can fix: a missing
	// account or a routing configuration that excludes every channel.
	Fatal bool
}

func getMap(value map[string]any, key string) map[string]any {
	if value == nil {
		return nil
	}
	return jsonx.Map(value[key])
}

func getSlice(value map[string]any, key string) []any {
	if value == nil {
		return nil
	}
	return jsonx.Slice(value[key])
}

func getString(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	result, _ := value[key].(string)
	return result
}

func getStringSlice(value map[string]any, key string) []string {
	raw := getSlice(value, key)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func getBool(value map[string]any, key string) bool {
	if value == nil {
		return false
	}
	result, _ := value[key].(bool)
	return result
}

func parseModelCapability(value map[string]any, updatedAt int64) model.ModelMeta {
	result := model.ModelMeta{
		DisplayName:         getString(value, "name"),
		Description:         getString(value, "description"),
		Family:              getString(value, "family"),
		CapabilitiesKnown:   true,
		Reasoning:           getBool(value, "reasoning"),
		Attachment:          getBool(value, "attachment"),
		ToolCall:            getBool(value, "tool_call"),
		StructuredOutput:    getBool(value, "structured_output"),
		Temperature:         getBool(value, "temperature"),
		CapabilityUpdatedAt: updatedAt,
	}
	for _, raw := range getSlice(value, "reasoning_options") {
		option := jsonx.Map(raw)
		if getString(option, "type") == "effort" {
			result.ReasoningEfforts = strx.Unique(append(result.ReasoningEfforts, getStringSlice(option, "values")...))
		}
	}
	modalities := getMap(value, "modalities")
	result.InputModalities = strx.Unique(getStringSlice(modalities, "input"))
	result.OutputModalities = strx.Unique(getStringSlice(modalities, "output"))
	limits := getMap(value, "limit")
	result.ContextWindow = formatInt(limits["context"])
	result.OutputLimit = formatInt(limits["output"])
	return result
}

func normalizeModelCapability(modelID string, capability model.ModelMeta) model.ModelMeta {
	// Some generated Cline Pass rows currently expose a generic OpenAI effort
	// list instead of the model's distinct wire-level tiers. Compatibility
	// aliases are deliberately omitted here: the UI should describe actual
	// behavior, not several names that collapse to the same effort.
	switch modelID {
	case "cline-pass/deepseek-v4-flash", "cline-pass/deepseek-v4.1-flash", "cline-pass/deepseek-v4-pro":
		capability.Reasoning = true
		capability.ReasoningEfforts = []string{"none", "low", "high", "max"}
	case "cline-pass/glm-5.2":
		capability.Reasoning = true
		capability.ReasoningEfforts = []string{"none", "high", "max"}
	case "cline-pass/glm-5.3", "cline-pass/glm-5.3-flash":
		capability.Reasoning = true
		capability.ReasoningEfforts = []string{"low", "high", "max"}
	case "cline-pass/kimi-k3":
		capability.Reasoning = true
		capability.ReasoningEfforts = []string{"low", "high", "max"}
	case "cline-pass/qwen3.8-max":
		capability.Reasoning = true
		capability.ReasoningEfforts = []string{"none", "low", "medium", "xhigh"}
	case "cline-pass/kimi-k2.6", "cline-pass/minimax-m3", "cline-pass/mimo-v2.5", "cline-pass/mimo-v2.5-pro":
		// These models currently document a thinking toggle rather than
		// multiple independent effort levels. none/high represents off/on.
		capability.Reasoning = true
		capability.ReasoningEfforts = []string{"none", "high"}
	}
	return capability
}

func mergeModelCapability(current model.ModelMeta, capability model.ModelMeta) model.ModelMeta {
	if capability.DisplayName != "" {
		current.DisplayName = capability.DisplayName
	}
	if capability.Description != "" {
		current.Description = capability.Description
	}
	if capability.Family != "" {
		current.Family = capability.Family
	}
	if capability.CapabilitiesKnown {
		current.CapabilitiesKnown = true
		current.Reasoning = capability.Reasoning
		current.ReasoningEfforts = capability.ReasoningEfforts
		current.InputModalities = capability.InputModalities
		current.OutputModalities = capability.OutputModalities
		current.Attachment = capability.Attachment
		current.ToolCall = capability.ToolCall
		current.StructuredOutput = capability.StructuredOutput
		current.Temperature = capability.Temperature
		current.ContextWindow = capability.ContextWindow
		current.OutputLimit = capability.OutputLimit
		current.CapabilityUpdatedAt = capability.CapabilityUpdatedAt
	}
	return current
}

func errorText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(raw)
	}
}

func extractError(root map[string]any) string {
	if root == nil {
		return ""
	}
	return errorText(root["error"])
}

func hasChoices(root map[string]any) bool {
	if root == nil {
		return false
	}
	if len(getSlice(root, "choices")) > 0 {
		return true
	}
	data := getMap(root, "data")
	return len(getSlice(data, "choices")) > 0
}

func reasoningEffortDisabled(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

func reasoningTokensDisabled(body map[string]any) bool {
	if include, ok := body["include_reasoning"].(bool); ok && !include {
		return true
	}
	if effort, ok := body["reasoning_effort"].(string); ok && reasoningEffortDisabled(effort) {
		return true
	}
	if reasoning := jsonx.Map(body["reasoning"]); reasoning != nil {
		if exclude, ok := reasoning["exclude"].(bool); ok && exclude {
			return true
		}
		if reasoningEffortDisabled(getString(reasoning, "effort")) {
			return true
		}
	}
	return false
}

func ensureIncludeReasoning(body map[string]any, meta model.ModelMeta) {
	if body == nil {
		return
	}
	if _, found := body["include_reasoning"]; found || reasoningTokensDisabled(body) {
		return
	}
	if body["reasoning_effort"] != nil || jsonx.Map(body["reasoning"]) != nil || meta.Reasoning {
		body["include_reasoning"] = true
	}
}

func responseBody(root map[string]any) map[string]any {
	if root == nil {
		return nil
	}
	data := getMap(root, "data")
	if data != nil && len(getSlice(data, "choices")) > 0 {
		return data
	}
	return root
}

func ParseRouting(root map[string]any) Routing {
	data := responseBody(root)
	if data == nil {
		return Routing{}
	}
	var message map[string]any
	choices := getSlice(data, "choices")
	if len(choices) > 0 {
		message = getMap(jsonx.Map(choices[0]), "message")
	}
	messageMetadata := getMap(message, "provider_metadata")
	rootMetadata := getMap(data, "provider_metadata")
	routing := getMap(getMap(messageMetadata, "gateway"), "routing")
	if routing == nil {
		routing = getMap(getMap(rootMetadata, "gateway"), "routing")
	}
	direct, _ := data["provider"].(string)
	result := Routing{
		Content:           getString(message, "content"),
		CanonicalSlug:     getString(routing, "canonicalSlug"),
		FinalProvider:     getString(routing, "finalProvider"),
		FinalProviderName: getString(routing, "finalProvider"),
		Fallbacks:         getStringSlice(routing, "fallbacksAvailable"),
		Plan:              getString(routing, "planningReasoning"),
	}
	if result.FinalProvider != "" {
		result.Pipeline = "planner"
	} else if direct != "" {
		result.Pipeline = "direct"
		result.FinalProvider = slugify(direct)
		result.FinalProviderName = direct
	}
	if result.CanonicalSlug == "" {
		if modelID, ok := data["model"].(string); ok && strings.Contains(modelID, "/") {
			result.CanonicalSlug = modelID
		}
	}
	return result
}

// slugify approximates OpenRouter's provider slug convention: lower case, any
// run of non-alphanumerics becomes a single dash ("Z.AI" -> "z-ai",
// "Inference.net" -> "inference-net", "Atlas Cloud" -> "atlas-cloud").
func slugify(value string) string {
	var out strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			dash = false
			continue
		}
		if out.Len() > 0 && !dash {
			out.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(out.String(), "-")
}

// providerKey folds a slug or display name down to its letters and digits so
// "z.ai", "z-ai" and "Z.AI" compare equal.
func providerKey(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// CanonicalProvider maps the provider a gateway names in a response to the
// slug the model's channel list (and therefore the pin configuration) uses.
// OpenRouter reports display names ("Z.AI", "AtlasCloud") while pins are
// slugs ("z-ai", "atlas-cloud"); without this the UI cannot tell that a hit
// on the pinned channel was in fact a hit.
func CanonicalProvider(meta model.ModelMeta, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	key := providerKey(raw)
	if key == "" {
		return slugify(raw)
	}
	for _, slug := range meta.Upstreams {
		if providerKey(slug) == key {
			return slug
		}
	}
	for slug, detail := range meta.UpstreamDetail {
		if providerKey(slug) == key || providerKey(detail.Name) == key {
			return slug
		}
	}
	return slugify(raw)
}

func (s *Service) CanonicalProvider(modelID, raw string) string {
	return CanonicalProvider(s.store.ModelMeta(modelID), raw)
}

// RoutingFor parses the routing metadata of a completion and canonicalizes
// the provider against the model's known channels.
func (s *Service) RoutingFor(modelID string, root map[string]any) Routing {
	routing := ParseRouting(root)
	routing.FinalProvider = s.CanonicalProvider(modelID, routing.FinalProvider)
	return routing
}

func classifyUpstreamError(message string) string {
	switch {
	case regexp.MustCompile(`(?i)empty response content`).MatchString(message):
		return "ok"
	}
	switch apierr.InferType(0, message, nil) {
	case "rate_limit_error":
		return "limited"
	case "invalid_request_error", "context_length_exceeded", "content_filter", "not_found_error":
		return "bad"
	case "authentication_error", "permission_error":
		return "auth"
	default:
		if regexp.MustCompile(`(?i)modelid|no allowed providers|no available providers|unsupported`).MatchString(message) {
			return "bad"
		}
		return "unknown"
	}
}

// pinProbeSlug is deliberately syntactically valid: some gateways drop
// underscore-shaped probe names before routing, which would make the probe
// look like "the preference is ignored" even when it is still honoured.
const pinProbeSlug = "zzz-not-a-provider"

var (
	availableProvidersRE = regexp.MustCompile(`(?i)Available providers are:\s*([^.]+)`)
	slugTokenRE          = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// Provider names may carry dots ("Z.AI"), so the winner token cannot be
	// limited to word characters and dashes.
	tier0RE = regexp.MustCompile(`([\w.-]+) won tier 0 over ([^."]+)`)
	// A routing rejection proves the gateway read the client's provider
	// preference. The phrases cover both the planner and OpenRouter shapes.
	routingRejectionRE  = regexp.MustCompile(`(?i)no (?:allowed|available) providers|available providers are|provider\.only|requested_providers|no provider matches`)
	planOrderPrefix     = "Total execution order:"
	planProvidersPrefix = "System credentials planned for:"
)

func parseAvailableProviders(message string) []string {
	match := availableProvidersRE.FindStringSubmatch(message)
	if len(match) < 2 {
		return nil
	}
	var result []string
	for _, token := range strings.Split(match[1], ",") {
		token = strings.ToLower(strings.TrimSpace(token))
		if slugTokenRE.MatchString(token) {
			result = append(result, token)
		}
	}
	return strx.Unique(result)
}

func parseTier0(plan string) []string {
	match := tier0RE.FindStringSubmatch(plan)
	if len(match) < 3 {
		return nil
	}
	result := []string{match[1]}
	replacer := strings.NewReplacer(" and ", ",", " and ", ",")
	rest := replacer.Replace(match[2])
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return strx.Unique(result)
}

// parsePlannedProviders extracts the channel order Cline's planner reports in
// provider_metadata.gateway.routing.planningReasoning. It is the most reliable
// channel source when the gateway stops honouring providerOptions:
// fallbacksAvailable deliberately omits the chain head.
func parsePlannedProviders(plan string) []string {
	if plan == "" {
		return nil
	}
	if order := planSegment(plan, planOrderPrefix); order != "" {
		order = strings.ReplaceAll(order, "->", "→")
		if providers := parsePlanTokens(order, "→"); len(providers) > 0 {
			return providers
		}
	}
	if planned := planSegment(plan, planProvidersPrefix); planned != "" {
		// The sentence continues after the list. Planner tokens are slugs, so
		// the first period safely ends the provider list.
		if dot := strings.Index(planned, "."); dot >= 0 {
			planned = planned[:dot]
		}
		if providers := parsePlanTokens(planned, ","); len(providers) > 0 {
			return providers
		}
	}
	return nil
}

func planSegment(plan, prefix string) string {
	index := strings.Index(plan, prefix)
	if index < 0 {
		index = strings.Index(strings.ToLower(plan), strings.ToLower(prefix))
		if index < 0 {
			return ""
		}
	}
	segment := plan[index+len(prefix):]
	if newline := strings.IndexAny(segment, "\r\n"); newline >= 0 {
		segment = segment[:newline]
	}
	return strings.TrimSpace(segment)
}

func parsePlanTokens(value, separator string) []string {
	var result []string
	for _, part := range strings.Split(value, separator) {
		if token := normalisePlanToken(part); token != "" {
			result = append(result, token)
		}
	}
	return strx.Unique(result)
}

func normalisePlanToken(value string) string {
	token := strings.ToLower(strings.TrimSpace(value))
	if index := strings.Index(token, "("); index >= 0 {
		token = token[:index]
	}
	token = strings.Trim(token, " .,;:'\"")
	if !slugTokenRE.MatchString(token) {
		return ""
	}
	return token
}

func (s *Service) fetchJSON(ctx context.Context, method, endpoint string, headers map[string]string, body any, timeout time.Duration) (int, any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "cline-pass-switcher-go/1.0")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return response.StatusCode, nil, err
	}
	var decoded any
	if len(bytes.TrimSpace(raw)) == 0 {
		decoded = map[string]any{}
	} else if err := json.Unmarshal(raw, &decoded); err != nil {
		decoded = map[string]any{"raw": string(raw)}
	}
	return response.StatusCode, decoded, nil
}

func (s *Service) fetchText(ctx context.Context, endpoint string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "cline-pass-switcher-go/1.0")
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func formatInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	case int:
		return typed
	case int64:
		return int(typed)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	default:
		return 0
	}
}

func formatInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	case int:
		return int64(typed)
	case int64:
		return typed
	case string:
		result, _ := strconv.ParseInt(typed, 10, 64)
		return result
	default:
		return 0
	}
}

func normalizeSlug(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

var errNoAccount = errors.New("尚未配置可用的 Cline Pass 账号")
