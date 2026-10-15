package stagejson

import (
	"errors"
	"strings"
	"testing"
)

type decodeRec struct {
	Text   string `json:"text"`
	Stance string `json:"stance"`
}

func TestDecodeArrayAcceptsWrapperAndArray(t *testing.T) {
	cases := []struct {
		name, content string
		want          int
	}{
		{"wrapper object", `{"claims": [{"text": "a", "stance": "asserted"}, {"text": "b", "stance": "hedged"}]}`, 2},
		{"wrapper with whitespace", " \n {\"claims\": [{\"text\": \"a\", \"stance\": \"asserted\"}]} \n", 1},
		{"bare array (fixtures)", `[{"text": "a", "stance": "asserted"}]`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raws []decodeRec
			if err := DecodeArray(tc.content, "claims", &raws); err != nil {
				t.Fatalf("DecodeArray: %v", err)
			}
			if len(raws) != tc.want {
				t.Fatalf("got %d records, want %d", len(raws), tc.want)
			}
		})
	}
}

func TestDecodeArrayObjectWithoutKeyErrors(t *testing.T) {
	var raws []decodeRec
	err := DecodeArray(`{"models": [{"text": "a"}]}`, "claims", &raws)
	if err == nil {
		t.Fatal("expected error for object without the wrapper key")
	}
	if !strings.Contains(err.Error(), `"claims"`) {
		t.Fatalf("error should name the missing wrapper key: %v", err)
	}
}

func TestDecodeArrayBareObjectIsAnError(t *testing.T) {
	// A single record object (not wrapped, not an array) must NOT decode:
	// that is the live failure shape the wrapper keys were introduced to
	// prevent, and silently salvaging it would violate the no-salvage rule.
	var raws []decodeRec
	err := DecodeArray(`{"text":"a","stance":"asserted"}`, "claims", &raws)
	if err == nil {
		t.Fatal("expected error for a bare record object")
	}
}

func TestDecodeArrayGarbageErrors(t *testing.T) {
	var raws []decodeRec
	err := DecodeArray("not json", "claims", &raws)
	if err == nil || !errors.Is(err, err) {
		_ = err
	}
	// The exact assertion: a non-JSON payload is an error either way.
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}
