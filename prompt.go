package instassist

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type optionEntry struct {
	Value               string `json:"value"`
	Description         string `json:"description"`
	RecommendationOrder int    `json:"recommendation_order"`
}

type optionResponse struct {
	Options []optionEntry `json:"options"`
}

func buildPrompt(userPrompt string) string {
	base := "Give me one or more concise options with short descriptions for the following: "
	schema := `Respond ONLY with JSON shaped like {"options":[{"value":"...","description":"...","recommendation_order":1}]}. No extra text.`
	return base + userPrompt + "\n" + schema
}

func parseOptions(raw string) ([]optionEntry, error) {
	var lastOpts []optionEntry
	search := raw
	for {
		idx := strings.Index(search, `{"options"`)
		if idx < 0 {
			break
		}
		segment := search[idx:]
		var resp optionResponse
		decoder := json.NewDecoder(strings.NewReader(segment))
		if err := decoder.Decode(&resp); err == nil && len(resp.Options) > 0 {
			opts := resp.Options
			sort.SliceStable(opts, func(i, j int) bool {
				oi := opts[i].RecommendationOrder
				oj := opts[j].RecommendationOrder
				if oi > 0 && oj > 0 && oi != oj {
					return oi < oj
				}
				if oi > 0 && oj <= 0 {
					return true
				}
				if oi <= 0 && oj > 0 {
					return false
				}
				return i < j
			})
			lastOpts = opts
		}
		search = search[idx+len(`{"options`):]
	}
	if len(lastOpts) > 0 {
		return lastOpts, nil
	}
	return nil, fmt.Errorf("failed to parse options JSON")
}

func extractOptions(raw string) ([]optionEntry, error) {
	if opts, err := parseOptions(raw); err == nil {
		return opts, nil
	}

	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var data any
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			continue
		}
		if opts := findOptionsInValue(data); len(opts) > 0 {
			return opts, nil
		}
	}

	return nil, fmt.Errorf("failed to parse options JSON")
}

func findOptionsInValue(v any) []optionEntry {
	switch val := v.(type) {
	case map[string]any:
		if optsVal, ok := val["options"]; ok {
			if opts := decodeOptionsFromInterface(optsVal); len(opts) > 0 {
				return opts
			}
		}
		for _, nested := range val {
			if opts := findOptionsInValue(nested); len(opts) > 0 {
				return opts
			}
		}
	case []any:
		for _, item := range val {
			if opts := findOptionsInValue(item); len(opts) > 0 {
				return opts
			}
		}
	case string:
		if opts, err := parseOptions(val); err == nil {
			return opts
		}
	}
	return nil
}

func decodeOptionsFromInterface(v any) []optionEntry {
	payload := map[string]any{"options": v}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	opts, err := parseOptions(string(b))
	if err != nil {
		return nil
	}
	return opts
}

var (
	uuidPattern      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	opencodePattern  = regexp.MustCompile(`ses_[A-Za-z0-9]+`)
	sessionWordRegex = regexp.MustCompile(`(?i)(session|conversation|thread)[\s:=]+([A-Za-z0-9_-]{6,})`)
)

func extractSessionID(raw string) string {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if id := sessionIDFromLine(line); id != "" {
			return id
		}
	}

	return pickSessionFromString(raw)
}

func sessionIDFromLine(line string) string {
	var data any
	if err := json.Unmarshal([]byte(line), &data); err == nil {
		if id := findSessionInValue(data); id != "" {
			return id
		}
	}
	return pickSessionFromString(line)
}

func findSessionInValue(v any) string {
	switch val := v.(type) {
	case map[string]any:
		for k, nested := range val {
			if looksLikeSessionKey(k) {
				if s := pickSessionFromValue(nested); s != "" {
					return s
				}
			}
		}
		for _, nested := range val {
			if s := findSessionInValue(nested); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range val {
			if s := findSessionInValue(item); s != "" {
				return s
			}
		}
	case string:
		if s := pickSessionFromString(val); s != "" {
			return s
		}
	}
	return ""
}

func looksLikeSessionKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "session") || strings.Contains(key, "thread") || strings.Contains(key, "conversation")
}

func pickSessionFromValue(v any) string {
	switch t := v.(type) {
	case string:
		return pickSessionFromString(t)
	case fmt.Stringer:
		return pickSessionFromString(t.String())
	}
	return ""
}

func pickSessionFromString(s string) string {
	if match := opencodePattern.FindString(s); match != "" {
		return match
	}
	if match := uuidPattern.FindString(s); match != "" {
		return match
	}
	if groups := sessionWordRegex.FindStringSubmatch(s); len(groups) > 2 {
		return groups[2]
	}
	return ""
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func schemaSources() (string, string, error) {
	tryPaths := []string{}

	if exe, err := os.Executable(); err == nil {
		tryPaths = append(tryPaths, filepath.Join(filepath.Dir(exe), "options.schema.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		tryPaths = append(tryPaths, filepath.Join(cwd, "options.schema.json"))
	}
	tryPaths = append(tryPaths, "/usr/local/share/insta-assist/options.schema.json")

	for _, p := range tryPaths {
		if data, err := os.ReadFile(p); err == nil {
			return p, string(data), nil
		}
	}

	// Fallback to embedded schema if available by writing to a temp file
	if len(embeddedSchema) > 0 {
		tmp, err := os.CreateTemp("", "insta-options-schema-*.json")
		if err != nil {
			return "", "", fmt.Errorf("failed to create temp schema file: %w", err)
		}
		if _, err := tmp.Write(embeddedSchema); err != nil {
			tmp.Close()
			return "", "", fmt.Errorf("failed to write temp schema file: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return "", "", fmt.Errorf("failed to close temp schema file: %w", err)
		}
		return tmp.Name(), string(embeddedSchema), nil
	}

	return "", "", fmt.Errorf("options.schema.json not found in executable directory, working directory, or /usr/local/share/insta-assist")
}
