package main

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

type MockHTTPClient struct {
	mu        sync.Mutex
	Requests  []*http.Request
	Responses []*http.Response
	Errors    []error
	CallCount int
	OnRequest func(req *http.Request)
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Requests = append(m.Requests, req)
	if m.OnRequest != nil {
		m.OnRequest(req)
	}

	var resp *http.Response
	var err error

	if m.CallCount < len(m.Responses) {
		resp = m.Responses[m.CallCount]
	} else {
		resp = &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
		}
	}

	if m.CallCount < len(m.Errors) {
		err = m.Errors[m.CallCount]
	}

	m.CallCount++
	return resp, err
}

func (m *MockHTTPClient) GetCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CallCount
}

func (m *MockHTTPClient) GetRequests() []*http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*http.Request, len(m.Requests))
	copy(cp, m.Requests)
	return cp
}
