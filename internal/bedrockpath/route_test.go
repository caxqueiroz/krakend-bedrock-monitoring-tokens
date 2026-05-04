package bedrockpath

import "testing"

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		wantOK     bool
		wantModel  string
		wantAPI    APISurface
		wantStream bool
	}{
		{
			name:      "invoke model",
			path:      "/model/anthropic.claude-3-5-sonnet-20241022-v2:0/invoke",
			wantOK:    true,
			wantModel: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			wantAPI:   InvokeModel,
		},
		{
			name:       "invoke model stream",
			path:       "/model/anthropic.claude-3-5-sonnet-20241022-v2:0/invoke-with-response-stream",
			wantOK:     true,
			wantModel:  "anthropic.claude-3-5-sonnet-20241022-v2:0",
			wantAPI:    InvokeModelWithResponseStream,
			wantStream: true,
		},
		{
			name:      "converse",
			path:      "/model/meta.llama3-1-70b-instruct-v1:0/converse",
			wantOK:    true,
			wantModel: "meta.llama3-1-70b-instruct-v1:0",
			wantAPI:   Converse,
		},
		{
			name:       "converse stream",
			path:       "/bedrock/model/amazon.titan-text-premier-v1:0/converse-stream",
			wantOK:     true,
			wantModel:  "amazon.titan-text-premier-v1:0",
			wantAPI:    ConverseStream,
			wantStream: true,
		},
		{
			name:   "non bedrock path",
			path:   "/v1/chat/completions",
			wantOK: false,
		},
		{
			name:   "missing model",
			path:   "/model//invoke",
			wantOK: false,
		},
		{
			name:   "unknown action",
			path:   "/model/anthropic.claude/invoke-async",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := Classify(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.ModelID != tt.wantModel {
				t.Fatalf("ModelID = %q, want %q", got.ModelID, tt.wantModel)
			}
			if got.Surface != tt.wantAPI {
				t.Fatalf("Surface = %q, want %q", got.Surface, tt.wantAPI)
			}
			if got.Streaming != tt.wantStream {
				t.Fatalf("Streaming = %v, want %v", got.Streaming, tt.wantStream)
			}
		})
	}
}
