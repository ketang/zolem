package main

import (
	"net/http"
	"testing"
)

// TestE2E_Gemini_PartTypes verifies request-side schema acceptance for every
// real Gemini Part data variant against a real zolem process, on both the v1
// and v1beta routes (each has its own vendored schema).
func TestE2E_Gemini_PartTypes(t *testing.T) {
	svc := startFixedService(t, "gemini")
	headers := []string{"x-goog-api-key: k", "Content-Type: application/json"}

	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "function_response_round_trip",
			body: `{"contents":[` +
				`{"role":"user","parts":[{"text":"x"}]},` +
				`{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},` +
				`{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"temp":20}}}]}]}`,
			want: http.StatusOK,
		},
		{
			name: "file_data",
			body: `{"contents":[{"role":"user","parts":[` +
				`{"fileData":{"mimeType":"application/pdf","fileUri":"https://example/f"}},{"text":"summarize"}]}]}`,
			want: http.StatusOK,
		},
		{
			name: "executable_code",
			body: `{"contents":[{"role":"model","parts":[` +
				`{"executableCode":{"language":"PYTHON","code":"print(1)"}},` +
				`{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1"}}]},` +
				`{"role":"user","parts":[{"text":"ok"}]}]}`,
			want: http.StatusOK,
		},
		{
			name: "video_metadata_with_file_data",
			body: `{"contents":[{"role":"user","parts":[` +
				`{"fileData":{"mimeType":"video/mp4","fileUri":"https://example/v"},"videoMetadata":{"startOffset":"1s"}}]}]}`,
			want: http.StatusOK,
		},
		{
			name: "thought_with_text_accepted",
			body: `{"contents":[{"role":"user","parts":[{"text":"t","thought":true}]}]}`,
			want: http.StatusOK,
		},
		{
			name: "empty_part_rejected",
			body: `{"contents":[{"role":"user","parts":[{}]}]}`,
			want: http.StatusBadRequest,
		},
		{
			name: "metadata_only_rejected",
			body: `{"contents":[{"role":"user","parts":[{"thought":true}]}]}`,
			want: http.StatusBadRequest,
		},
		{
			name: "thought_signature_only_rejected",
			body: `{"contents":[{"role":"user","parts":[{"thoughtSignature":"x"}]}]}`,
			want: http.StatusBadRequest,
		},
		{
			name: "function_response_missing_name_rejected",
			body: `{"contents":[{"role":"user","parts":[{"functionResponse":{"response":{}}}]}]}`,
			want: http.StatusBadRequest,
		},
		{
			name: "file_data_missing_uri_rejected",
			body: `{"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"a/b"}}]}]}`,
			want: http.StatusBadRequest,
		},
	}

	for _, version := range []string{"v1", "v1beta"} {
		path := "/" + version + "/models/gemini-2.0-flash:generateContent"
		for _, tc := range cases {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				resp, body := doRequest(t, svc.baseURL, http.MethodPost, path, tc.body, headers...)
				defer resp.Body.Close()
				if resp.StatusCode != tc.want {
					t.Fatalf("status: got %d, want %d; body: %s", resp.StatusCode, tc.want, body)
				}
			})
		}
	}
}
