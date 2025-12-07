package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func optionsSchemaPath() (string, error) {
	tryPaths := []string{}

	if exe, err := os.Executable(); err == nil {
		tryPaths = append(tryPaths, filepath.Join(filepath.Dir(exe), "options.schema.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		tryPaths = append(tryPaths, filepath.Join(cwd, "options.schema.json"))
	}
	tryPaths = append(tryPaths, "/usr/local/share/insta-assist/options.schema.json")

	for _, p := range tryPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("options.schema.json not found in executable directory, working directory, or /usr/local/share/insta-assist")
}
