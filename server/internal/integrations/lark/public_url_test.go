package lark

import "testing"

func TestPublicURLReachable(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://sd-agora-web.fly.dev", true},
		{"https://agora.example.com", true},
		{"http://app.internal.example.com", true}, // public DNS name, http allowed
		{"", false},
		{"http://localhost:3000", false},
		{"http://127.0.0.1:3000", false},
		{"https://[::1]:3000", false},
		{"http://192.168.1.10:3000", false}, // private
		{"http://10.0.0.5", false},          // private
		{"http://169.254.1.1", false},       // link-local
		{"http://0.0.0.0:8080", false},      // unspecified
		{"http://my-mac.local:3000", false}, // mDNS
		{"not a url at all", false},
	}
	for _, c := range cases {
		if got := publicURLReachable(c.raw); got != c.want {
			t.Errorf("publicURLReachable(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}
