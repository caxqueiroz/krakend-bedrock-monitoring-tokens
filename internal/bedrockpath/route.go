package bedrockpath

import "strings"

type APISurface string

const (
	InvokeModel                   APISurface = "InvokeModel"
	InvokeModelWithResponseStream APISurface = "InvokeModelWithResponseStream"
	Converse                      APISurface = "Converse"
	ConverseStream                APISurface = "ConverseStream"
)

type Route struct {
	Surface   APISurface
	ModelID   string
	Streaming bool
}

func Classify(path string) (Route, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != "model" {
			continue
		}

		modelID := parts[i+1]
		action := parts[i+2]
		if modelID == "" || action == "" || i+3 != len(parts) {
			return Route{}, false
		}

		switch action {
		case "invoke":
			return Route{Surface: InvokeModel, ModelID: modelID}, true
		case "invoke-with-response-stream":
			return Route{Surface: InvokeModelWithResponseStream, ModelID: modelID, Streaming: true}, true
		case "converse":
			return Route{Surface: Converse, ModelID: modelID}, true
		case "converse-stream":
			return Route{Surface: ConverseStream, ModelID: modelID, Streaming: true}, true
		default:
			return Route{}, false
		}
	}
	return Route{}, false
}
