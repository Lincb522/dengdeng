package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"

	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

const (
	creationRulesHeader  = "X-DengDeng-Rules"
	creationSkillsHeader = "X-DengDeng-Skills"
	creationSettingsKey  = "dengdeng.system-settings"
	creationAppliedKey   = "dengdeng.creation-guidance-applied"
	creationUserIDKey    = "dengdeng.creation-user-id"
)

type creationWire uint8

const (
	creationWireOpenAIChat creationWire = iota
	creationWireOpenAIResponses
	creationWireAnthropic
	creationWireGemini
	creationWirePrompt
	creationWireInstructions
)

func (g *Gateway) currentSystemSettings(c *gin.Context) (service.SystemSettings, error) {
	if cached, exists := c.Get(creationSettingsKey); exists {
		if settings, ok := cached.(service.SystemSettings); ok {
			return settings, nil
		}
	}
	if g.settings == nil {
		return service.SystemSettings{}, nil
	}
	settings, err := g.settings.Get()
	if err != nil {
		return service.SystemSettings{}, err
	}
	c.Set(creationSettingsKey, settings)
	return settings, nil
}

func creationScopeMatches(entryScope, requestScope string) bool {
	return entryScope == service.CreationScopeAll || entryScope == requestScope
}

func parseCreationIDs(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	if len(value) > 2_048 {
		return nil, fmt.Errorf("selection header is too long")
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	for _, raw := range strings.Split(value, ",") {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		for _, r := range id {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
				return nil, fmt.Errorf("invalid selection %q", id)
			}
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) > 24 {
		return nil, fmt.Errorf("at most 24 entries can be selected")
	}
	return ids, nil
}

func selectCreationGuidance(library service.CreationLibrarySettings, scope string, ruleIDs, skillIDs []string) ([]string, []string, []string, error) {
	if !library.Enabled {
		if len(ruleIDs)+len(skillIDs) > 0 {
			return nil, nil, nil, fmt.Errorf("the built-in library is disabled")
		}
		return nil, nil, nil, nil
	}
	if !library.Capabilities.ScopeEnabled(scope) {
		if len(ruleIDs)+len(skillIDs) > 0 {
			return nil, nil, nil, fmt.Errorf("built-in capability %q is disabled", scope)
		}
		return nil, nil, nil, nil
	}

	selectEntries := func(kind string, entries []service.CreationLibraryEntry, requested []string) ([]string, []string, error) {
		if !library.Capabilities.TypeEnabled(kind) {
			if len(requested) > 0 {
				return nil, nil, fmt.Errorf("built-in %ss are disabled", kind)
			}
			return nil, nil, nil
		}
		requestedSet := make(map[string]struct{}, len(requested))
		for _, id := range requested {
			requestedSet[id] = struct{}{}
		}
		available := make(map[string]struct{}, len(entries))
		contents := make([]string, 0)
		applied := make([]string, 0)
		for _, entry := range entries {
			if entry.Enabled {
				available[entry.ID] = struct{}{}
			}
			_, explicitlySelected := requestedSet[entry.ID]
			if !entry.Enabled || !creationScopeMatches(entry.Scope, scope) || (!entry.AutoApply && !explicitlySelected) {
				continue
			}
			contents = append(contents, entry.Content)
			applied = append(applied, entry.ID)
		}
		for _, id := range requested {
			if _, ok := available[id]; !ok {
				return nil, nil, fmt.Errorf("%s %q is unavailable", kind, id)
			}
		}
		return contents, applied, nil
	}

	ruleContent, appliedRules, err := selectEntries("rule", library.Rules, ruleIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	skillContent, appliedSkills, err := selectEntries("skill", library.Skills, skillIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	return append(ruleContent, skillContent...), appliedRules, appliedSkills, nil
}

func mergeCreationIDs(groups ...[]string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, group := range groups {
		for _, id := range group {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

func availableStoredCreationIDs(library service.CreationLibrarySettings, kind string, requested []string) []string {
	if !library.Enabled || !library.Capabilities.TypeEnabled(kind) {
		return nil
	}
	entries := library.Rules
	if kind == "skill" {
		entries = library.Skills
	}
	available := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Enabled && !entry.AutoApply && library.Capabilities.ScopeEnabled(entry.Scope) {
			available[entry.ID] = struct{}{}
		}
	}
	result := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := available[id]; ok {
			result = append(result, id)
		}
	}
	return result
}

func appendGuidance(current string, guidance []string) string {
	parts := make([]string, 0, len(guidance)+1)
	if current = strings.TrimSpace(current); current != "" {
		parts = append(parts, current)
	}
	for _, item := range guidance {
		if item = strings.TrimSpace(item); item != "" {
			parts = append(parts, item)
		}
	}
	return strings.Join(parts, "\n\n")
}

func applyCreationGuidanceJSON(body []byte, wire creationWire, guidance []string) ([]byte, error) {
	if len(guidance) == 0 {
		return body, nil
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("invalid JSON body")
	}
	joined := appendGuidance("", guidance)
	switch wire {
	case creationWireOpenAIChat:
		messages, _ := request["messages"].([]any)
		request["messages"] = append([]any{map[string]any{"role": "system", "content": joined}}, messages...)
	case creationWireOpenAIResponses:
		current, exists := request["instructions"]
		if exists && current != nil {
			text, ok := current.(string)
			if !ok {
				return nil, fmt.Errorf("instructions must be a string when built-in guidance is used")
			}
			request["instructions"] = appendGuidance(text, guidance)
		} else {
			request["instructions"] = joined
		}
	case creationWireAnthropic:
		switch current := request["system"].(type) {
		case nil:
			request["system"] = joined
		case string:
			request["system"] = appendGuidance(current, guidance)
		case []any:
			request["system"] = append(current, map[string]any{"type": "text", "text": joined})
		default:
			return nil, fmt.Errorf("system must be text or a content block list when built-in guidance is used")
		}
	case creationWireGemini:
		instruction, _ := request["systemInstruction"].(map[string]any)
		if instruction == nil {
			instruction = map[string]any{}
		}
		parts, _ := instruction["parts"].([]any)
		instruction["parts"] = append(parts, map[string]any{"text": joined})
		request["systemInstruction"] = instruction
	case creationWirePrompt:
		prompt, _ := request["prompt"].(string)
		if strings.TrimSpace(prompt) != "" {
			request["prompt"] = appendGuidance(prompt, guidance)
		}
	case creationWireInstructions:
		instructions, _ := request["instructions"].(string)
		if strings.TrimSpace(instructions) != "" {
			request["instructions"] = appendGuidance(instructions, guidance)
		}
	}
	return json.Marshal(request)
}

func (g *Gateway) creationGuidance(c *gin.Context, scope string) ([]string, bool) {
	settings, err := g.currentSystemSettings(c)
	if err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "load built-in library failed")
		return nil, false
	}
	explicitRuleIDs, err := parseCreationIDs(c.GetHeader(creationRulesHeader))
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return nil, false
	}
	explicitSkillIDs, err := parseCreationIDs(c.GetHeader(creationSkillsHeader))
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return nil, false
	}
	stored := service.UserCreationSelection{}
	if g.settings != nil {
		if rawUserID, exists := c.Get(creationUserIDKey); exists {
			if userID, ok := rawUserID.(int64); ok && userID > 0 {
				stored, err = g.settings.UserCreationSelection(userID)
				if err != nil {
					util.Fail(c, http.StatusServiceUnavailable, "load built-in selection failed")
					return nil, false
				}
			}
		}
	}
	ruleIDs := mergeCreationIDs(availableStoredCreationIDs(settings.CreationLibrary, "rule", stored.RuleIDs), explicitRuleIDs)
	skillIDs := mergeCreationIDs(availableStoredCreationIDs(settings.CreationLibrary, "skill", stored.SkillIDs), explicitSkillIDs)
	guidance, appliedRules, appliedSkills, err := selectCreationGuidance(settings.CreationLibrary, scope, ruleIDs, skillIDs)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if len(appliedRules) > 0 {
		sort.Strings(appliedRules)
		c.Header("X-DengDeng-Applied-Rules", strings.Join(appliedRules, ","))
	}
	if len(appliedSkills) > 0 {
		sort.Strings(appliedSkills)
		c.Header("X-DengDeng-Applied-Skills", strings.Join(appliedSkills, ","))
	}
	return guidance, true
}

func (g *Gateway) applyCreationGuidance(c *gin.Context, body []byte, scope string, wire creationWire) ([]byte, bool) {
	if applied, _ := c.Get(creationAppliedKey); applied == true {
		return body, true
	}
	guidance, ok := g.creationGuidance(c, scope)
	if !ok {
		return nil, false
	}
	patched, err := applyCreationGuidanceJSON(body, wire, guidance)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return patched, true
}

func applyCreationGuidanceMultipart(contentType string, body []byte, guidance []string) ([]byte, string, error) {
	if len(guidance) == 0 {
		return body, contentType, nil
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return nil, "", fmt.Errorf("image edits require multipart/form-data")
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	foundPrompt := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("invalid multipart body")
		}
		outPart, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, "", err
		}
		if part.FormName() == "prompt" {
			value, readErr := io.ReadAll(part)
			if readErr != nil {
				return nil, "", readErr
			}
			foundPrompt = strings.TrimSpace(string(value)) != ""
			if foundPrompt {
				_, err = io.WriteString(outPart, appendGuidance(string(value), guidance))
			}
		} else {
			_, err = io.Copy(outPart, part)
		}
		if err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	if !foundPrompt {
		return body, contentType, nil
	}
	return out.Bytes(), writer.FormDataContentType(), nil
}

func (g *Gateway) handleCreationLibrary(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	if g.settings == nil {
		settings, err := g.currentSystemSettings(c)
		if err != nil {
			util.Fail(c, http.StatusServiceUnavailable, "load built-in library failed")
			return
		}
		util.OK(c, service.UserCreationLibrary{CreationLibrarySettings: service.PublicCreationLibrary(settings.CreationLibrary)})
		return
	}
	library, err := g.settings.UserCreationLibrary(ak.User.ID)
	if err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "load built-in library failed")
		return
	}
	util.OK(c, library)
}
