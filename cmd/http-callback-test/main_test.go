package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallbackReceivesAndPrintsJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      int
	}{
		{"batch", `[{"id":"one","after":{"status":"paid","port":"上海"}},{"id":"two"}]`, 2},
		{"object", `{"id":"probe"}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := newHandler(log.New(&output, "", 0))
			req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer do-not-print-this")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var result struct {
				OK       bool `json:"ok"`
				Received int  `json:"received"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.OK || result.Received != tc.count {
				t.Fatalf("result=%+v", result)
			}
			if !strings.Contains(output.String(), "收到 HTTP 回调") || !strings.Contains(output.String(), `"id"`) {
				t.Fatalf("missing callback output: %s", output.String())
			}
			if tc.name == "batch" && !strings.Contains(output.String(), "上海") {
				t.Fatal("中文内容未打印")
			}
			if strings.Contains(output.String(), "do-not-print-this") {
				t.Fatal("不应打印认证请求头")
			}
		})
	}
}

func TestCallbackRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"invalid JSON", `not-json`, http.StatusBadRequest},
		{"scalar JSON", `true`, http.StatusBadRequest},
		{"too large", strings.Repeat("x", 10*1024*1024+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			response := httptest.NewRecorder()
			newHandler(log.New(&output, "", 0)).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(tc.body)))
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d", response.Code, tc.status)
			}
			if strings.Contains(output.String(), "收到 HTTP 回调") {
				t.Fatal("拒绝的请求不应记录为接收成功")
			}
		})
	}
}

func TestHealthAndMethodRouting(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/health", http.StatusOK},
		{http.MethodGet, "/callback", http.StatusMethodNotAllowed},
		{http.MethodPost, "/unknown", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		newHandler(log.New(&bytes.Buffer{}, "", 0)).ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s %s: status=%d want=%d", tc.method, tc.path, response.Code, tc.status)
		}
		if tc.path == "/health" && !strings.Contains(response.Body.String(), `"ok":true`) {
			t.Fatalf("health body=%s", response.Body.String())
		}
	}
}
