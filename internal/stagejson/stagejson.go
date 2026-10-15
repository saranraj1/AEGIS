// Package stagejson implements the shared "structured LLM call" rule used
// by every JSON-returning stage: one call, and if the response is not
// valid JSON for the stage schema, exactly one "repair this JSON" call,
// then a hard error (spec: errors and retries).
package stagejson

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aegis-verify/aegis/internal/provider"
)

// ParseFunc validates raw assistant content. A nil return means the
// content is acceptable; a non-nil error triggers the repair path.
type ParseFunc func(content string) error

// DecodeArray parses a stage response whose logical shape is an array of
// records (claims, ground results, reasoning steps). Because
// response_format json_object guarantees a JSON OBJECT at top level, the
// canonical live shape is the wrapper {"<wrapperKey>": [...]}; a bare JSON
// array is also accepted (fake fixtures and pre-json_object transcripts
// use it). dst must point at a slice type matching the records. An object
// without the wrapper key is an error (it triggers the repair path).
func DecodeArray(content, wrapperKey string, dst any) error {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return json.Unmarshal([]byte(trimmed), dst)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &top); err != nil {
		return err
	}
	raw, ok := top[wrapperKey]
	if !ok {
		keys := make([]string, 0, len(top))
		for k := range top {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("object response missing %q key (top-level keys: %s)", wrapperKey, strings.Join(keys, ", "))
	}
	return json.Unmarshal(raw, dst)
}

const repairInstruction = `Your previous response was not valid JSON for the required schema. Here is your previous response:

%s

Return ONLY the corrected JSON matching the required schema, with no other text.`

// Call performs the stage call. Provider errors (timeouts, 4xx, retries
// exhausted) pass through untouched — only JSON-shape failures trigger the
// repair retry. Warnings from both calls are concatenated in order, plus a
// note when the repair path was used.
func Call(ctx context.Context, prov provider.Provider, req provider.Request, parse ParseFunc) (string, []string, error) {
	resp, err := prov.Call(ctx, req)
	if err != nil {
		return "", nil, err
	}
	if parseErr := parse(resp.Content); parseErr == nil {
		return resp.Content, resp.Warnings, nil
	}

	repairReq := req
	repairReq.User = req.User + "\n\n" + fmt.Sprintf(repairInstruction, resp.Content)
	resp2, err2 := prov.Call(ctx, repairReq)
	if err2 != nil {
		return "", resp.Warnings, err2
	}
	warnings := append(append([]string{}, resp.Warnings...), resp2.Warnings...)
	warnings = append(warnings, fmt.Sprintf("stage %s: json repair retry used", req.Stage))
	if parseErr := parse(resp2.Content); parseErr != nil {
		// Structured error so the CLI's exit-2 line stays kind-first
		// (D2: "error: provider-bad-response: ..."), like all other
		// stage failures.
		return "", warnings, &provider.Error{
			Kind: provider.KindBadResponse,
			Msg:  fmt.Sprintf("stage %s: response still not valid JSON after one repair retry: %v", req.Stage, parseErr),
		}
	}
	return resp2.Content, warnings, nil
}
