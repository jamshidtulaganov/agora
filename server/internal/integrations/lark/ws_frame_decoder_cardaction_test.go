package lark

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDecodeCardAction(t *testing.T) {
	d := NewLarkJSONFrameDecoder()
	var inst db.LarkInstallation

	payload := []byte(`{
		"schema": "2.0",
		"header": {"event_id": "ev1", "event_type": "card.action.trigger", "app_id": "cli_app"},
		"event": {
			"operator": {"open_id": "ou_operator"},
			"token": "tok123",
			"action": {"tag": "button", "value": {"action": "set_status", "issue_id": "abc", "status": "in_review"}},
			"context": {"open_message_id": "om_card", "open_chat_id": "oc_chat"}
		}
	}`)

	got, ok, err := d.DecodeCardAction(payload, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for a card.action.trigger frame")
	}
	if got.OperatorOpenID != "ou_operator" {
		t.Errorf("OperatorOpenID = %q, want ou_operator", got.OperatorOpenID)
	}
	if got.MessageID != "om_card" {
		t.Errorf("MessageID = %q, want om_card", got.MessageID)
	}
	if got.ChatID != "oc_chat" {
		t.Errorf("ChatID = %q, want oc_chat", got.ChatID)
	}
	if got.Value["action"] != "set_status" || got.Value["issue_id"] != "abc" || got.Value["status"] != "in_review" {
		t.Errorf("Value mismatch: %#v", got.Value)
	}
}

func TestDecodeCardAction_IgnoresMessageEvent(t *testing.T) {
	d := NewLarkJSONFrameDecoder()
	var inst db.LarkInstallation
	// An im.message.receive_v1 frame must be declined by the card-action decoder
	// (the connector routes it to Decode instead).
	payload := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{}}`)
	_, ok, err := d.DecodeCardAction(payload, inst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("card-action decoder must decline a message event")
	}
}

func TestDecodeCardAction_EmptyAndHeartbeat(t *testing.T) {
	d := NewLarkJSONFrameDecoder()
	var inst db.LarkInstallation
	for _, p := range [][]byte{nil, []byte(``), []byte(`{"ping":1}`)} {
		if _, ok, err := d.DecodeCardAction(p, inst); ok || err != nil {
			t.Errorf("payload %q: want (false,nil), got (%v,%v)", p, ok, err)
		}
	}
}
