package ws

import "encoding/json"

// InboundMessage is the generic envelope for client->server messages (Section 4.2).
// Identity is never trusted from the payload — the hub always resolves the acting
// user from the authenticated connection, per the spec's "Critical rule".
type InboundMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func encodeEnvelope(msgType string, payload map[string]any) ([]byte, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["type"] = msgType
	return json.Marshal(payload)
}
