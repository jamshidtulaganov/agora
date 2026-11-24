package connectauth

import (
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

func TestEnvelopes(t *testing.T) {
	box, _ := secretbox.New(make([]byte, 32))
	other, _ := secretbox.New([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Now()
	req := Request{ClientID: "agora-claude", ClientName: "Claude", Return: "https://mcp.example/authorize/callback?state=x", Binding: "b"}

	sealed, err := SealRequest(box, req, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenRequest(box, sealed, now)
	if err != nil || got.ClientName != "Claude" || got.Return != req.Return {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := OpenRequest(box, sealed, now.Add(RequestLifetime+time.Second)); err == nil {
		t.Fatal("expired request accepted")
	}
	if _, err := OpenRequest(other, sealed, now); err == nil {
		t.Fatal("request sealed with another key accepted")
	}
	if _, err := OpenReply(box, sealed, now); err == nil {
		t.Fatal("request accepted as a reply")
	}
	insecure := req
	insecure.Return = "http://mcp.example/cb"
	if s, _ := SealRequest(box, insecure, now); s != "" {
		if _, err := OpenRequest(box, s, now); err == nil {
			t.Fatal("non-HTTPS return accepted")
		}
	}

	reply, err := SealReply(box, "mul_x", "b", now)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := OpenReply(box, reply, now); err != nil || r.Token != "mul_x" || r.Binding != "b" {
		t.Fatalf("reply round trip: %+v %v", r, err)
	}
	if _, err := OpenReply(box, reply, now.Add(ReplyLifetime+time.Second)); err == nil {
		t.Fatal("expired reply accepted")
	}
	if _, err := OpenRequest(box, reply, now); err == nil {
		t.Fatal("reply accepted as a request")
	}
	for _, bad := range []string{"", "!!!", "AAAA"} {
		if _, err := OpenReply(box, bad, now); err == nil {
			t.Fatalf("garbage %q accepted", bad)
		}
	}
}
