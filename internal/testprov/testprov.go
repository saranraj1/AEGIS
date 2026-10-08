// Package testprov provides scripted in-process providers for tests.
// The default suite must make zero network calls; these stubs stand in
// for provider.Provider without any I/O.
package testprov

import (
	"context"
	"fmt"
	"sync"

	"github.com/aegis-verify/aegis/internal/provider"
)

type seqResp struct {
	content string
	err     error
}

// Seq returns scripted responses in call order and records every request.
// When the script runs dry, Call fails with a test-only error.
type Seq struct {
	mu        sync.Mutex
	responses []seqResp
	idx       int
	Requests  []provider.Request
}

// NewSeq scripts successive content responses.
func NewSeq(contents ...string) *Seq {
	s := &Seq{}
	for _, c := range contents {
		s.responses = append(s.responses, seqResp{content: c})
	}
	return s
}

// Then scripts one more content response (builder style).
func (s *Seq) Then(content string) *Seq {
	s.responses = append(s.responses, seqResp{content: content})
	return s
}

// ThenErr scripts an error response for the next call.
func (s *Seq) ThenErr(err error) *Seq {
	s.responses = append(s.responses, seqResp{err: err})
	return s
}

// Call records the request and returns the next scripted response.
func (s *Seq) Call(_ context.Context, req provider.Request) (provider.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, req)
	if s.idx >= len(s.responses) {
		return provider.Response{}, fmt.Errorf("testprov: no scripted response for call %d", s.idx+1)
	}
	r := s.responses[s.idx]
	s.idx++
	if r.err != nil {
		return provider.Response{}, r.err
	}
	return provider.Response{Content: r.content}, nil
}

// Calls reports how many provider calls were made so far.
func (s *Seq) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx
}
