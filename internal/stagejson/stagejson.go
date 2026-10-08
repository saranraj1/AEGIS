// Package stagejson implements the shared "structured LLM call" rule used
// by every JSON-returning stage: one call, and if the response is not
// valid JSON for the stage schema, exactly one "repair this JSON" call,
// then a hard error (spec: errors and retries).
package stagejson

import (
	"context"
	"fmt"

	"github.com/aegis-verify/aegis/internal/provider"
)

// ParseFunc validates raw assistant content. A nil return means the
// content is acceptable; a non-nil error triggers the repair path.
type ParseFunc func(content string) error

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
		return "", warnings, fmt.Errorf("stage %s: response still not valid JSON after one repair retry: %w", req.Stage, parseErr)
	}
	return resp2.Content, warnings, nil
}
