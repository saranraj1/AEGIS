package stagejson

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/testprov"
)

func TestCallSuccess(t *testing.T) {
	seq := testprov.NewSeq(`{"ok":true}`)
	_, warnings, err := Call(context.Background(), seq, provider.Request{Stage: StageTest}, func(c string) error {
		if !strings.Contains(c, "ok") {
			t.Errorf("unexpected content %q", c)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if seq.Calls() != 1 {
		t.Errorf("calls = %d, want 1", seq.Calls())
	}
}

func TestCallRepairSucceeds(t *testing.T) {
	seq := testprov.NewSeq("not json", `[{"text":"fixed"}]`)
	var got string
	_, warnings, err := Call(context.Background(), seq, provider.Request{Stage: StageTest}, func(c string) error {
		if !strings.HasPrefix(c, "[") {
			return errors.New("not an array")
		}
		got = c
		return nil
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != `[{"text":"fixed"}]` {
		t.Errorf("final content = %q", got)
	}
	if seq.Calls() != 2 {
		t.Errorf("calls = %d, want 2 (original + one repair)", seq.Calls())
	}
	// The repair call must carry the previous bad output.
	repairUser := seq.Requests[1].User
	if !strings.Contains(repairUser, "not json") {
		t.Errorf("repair prompt should include the invalid response: %q", repairUser)
	}
	if !strings.Contains(repairUser, seq.Requests[0].User) {
		t.Errorf("repair prompt should include the original prompt")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "repair") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the repair retry: %v", warnings)
	}
}

func TestCallRepairFails(t *testing.T) {
	seq := testprov.NewSeq("bad", "still bad")
	_, _, err := Call(context.Background(), seq, provider.Request{Stage: StageTest}, func(c string) error {
		return errors.New("always invalid")
	})
	if err == nil {
		t.Fatal("expected hard error after repair")
	}
	if seq.Calls() != 2 {
		t.Errorf("calls = %d, want exactly 2 (no second repair)", seq.Calls())
	}
}

func TestCallProviderErrorPassesThrough(t *testing.T) {
	perr := &provider.Error{Kind: provider.KindTimeout, Msg: "boom"}
	seq := testprov.NewSeq("bad").ThenErr(perr)
	_, _, err := Call(context.Background(), seq, provider.Request{Stage: StageTest}, func(c string) error {
		return errors.New("invalid")
	})
	var got *provider.Error
	if !errors.As(err, &got) || got.Kind != provider.KindTimeout {
		t.Fatalf("got %v, want provider timeout passthrough", err)
	}
}

// StageTest exists so tests do not couple to a real stage name.
const StageTest provider.Stage = "test"
