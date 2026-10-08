package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aegis-verify/aegis/internal/report"
)

// Fake is the in-process provider: it serves canned JSON fixtures from a
// directory instead of making HTTP calls. It never reads USER_LLM_*.
//
// Fixture files are named "{job-id}.{stage}.json" where stage is one of
// wrap | claims | ground | reason | critique. A missing fixture for a
// required stage is a hard error. The file bytes are returned verbatim as
// the response content, exactly as a live provider would return the
// assistant message.
type Fake struct {
	Dir string
}

// NewFake returns a fake provider rooted at dir.
func NewFake(dir string) *Fake { return &Fake{Dir: dir} }

// Call loads the fixture for req.JobID and req.Stage.
func (f *Fake) Call(_ context.Context, req Request) (Response, error) {
	if !req.Stage.Valid() {
		return Response{}, &Error{Kind: KindBadRequest, Msg: fmt.Sprintf("invalid stage %q", req.Stage)}
	}
	if req.JobID == "" {
		return Response{}, &Error{Kind: KindBadRequest, Msg: "fake provider requires a job id to locate fixtures"}
	}
	path := filepath.Join(f.Dir, string(req.JobID)+"."+string(req.Stage)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Response{}, &Error{
				Kind: KindFakeMissing,
				Msg:  fmt.Sprintf("no fake fixture for job %q stage %q: %s", req.JobID, req.Stage, path),
			}
		}
		return Response{}, &Error{
			Kind: KindFakeUnreadable,
			Msg:  fmt.Sprintf("fake fixture %s unreadable: %v", path, err),
		}
	}
	return Response{Content: string(data)}, nil
}

// GroundRecord is one entry of a fake ground fixture, mirroring the spec
// shape {claim_id, grounding, quote}. ClaimID may be empty, in which case
// the record binds to the next unassigned claim in order (A11). Quote is
// required for supported and contradicted groundings and optional for
// not-in-context.
type GroundRecord struct {
	ClaimID   string `json:"claim_id,omitempty"`
	Grounding string `json:"grounding"`
	Quote     string `json:"quote,omitempty"`
}

// DecodeGround parses and validates the contents of a ground fixture.
// Unknown JSON fields are rejected, grounding values must come from the
// report vocabulary, and the quote-required rule is enforced here so a
// malformed fixture fails fast before stage logic runs.
func DecodeGround(data []byte) ([]GroundRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var recs []GroundRecord
	if err := dec.Decode(&recs); err != nil {
		return nil, &Error{Kind: KindBadResponse, Msg: "ground fixture is not a valid grounding array: " + err.Error()}
	}
	for i, r := range recs {
		switch r.Grounding {
		case report.Grounded, report.Contradicted:
			if strings.TrimSpace(r.Quote) == "" {
				return nil, &Error{
					Kind: KindBadResponse,
					Msg: fmt.Sprintf("ground record %d (claim %q): grounding %q requires a quote",
						i, r.ClaimID, r.Grounding),
				}
			}
		case report.NotInContext:
			// Quote optional.
		default:
			return nil, &Error{
				Kind: KindBadResponse,
				Msg: fmt.Sprintf("ground record %d (claim %q): unknown grounding value %q (accepted: %s, %s, %s)",
					i, r.ClaimID, r.Grounding, report.Grounded, report.Contradicted, report.NotInContext),
			}
		}
	}
	return recs, nil
}
