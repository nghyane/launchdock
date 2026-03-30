package launchdock

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed model_capabilities.json
var modelCapabilitiesSnapshot []byte

type LaunchModelMetadata struct {
	Provider    string         `json:"provider"`
	Name        string         `json:"name"`
	Attachment  bool           `json:"attachment"`
	Reasoning   bool           `json:"reasoning"`
	ToolCall    bool           `json:"tool_call"`
	Temperature bool           `json:"temperature"`
	Limit       map[string]any `json:"limit"`
}

var (
	launchModelMetadataOnce sync.Once
	launchModelMetadata     map[string]LaunchModelMetadata
)

func lookupLaunchModelMetadata(id string) (LaunchModelMetadata, bool) {
	launchModelMetadataOnce.Do(func() {
		_ = json.Unmarshal(modelCapabilitiesSnapshot, &launchModelMetadata)
		if launchModelMetadata == nil {
			launchModelMetadata = map[string]LaunchModelMetadata{}
		}
	})
	md, ok := launchModelMetadata[id]
	return md, ok
}
