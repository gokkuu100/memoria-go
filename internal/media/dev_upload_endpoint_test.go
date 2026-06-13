package media

import "testing"

func TestAllowedDevUploadEndpoint(t *testing.T) {
	allowed := []string{
		"http://localhost:9000",
		"http://127.0.0.1:9000",
		"http://10.0.2.2:9000",
		"http://192.168.0.101:9000",
	}
	for _, u := range allowed {
		if !AllowedDevMediaEndpoint(u) {
			t.Fatalf("expected allowed: %s", u)
		}
	}

	denied := []string{
		"https://192.168.0.101:9000",
		"http://evil.example.com:9000",
		"ftp://192.168.0.1:9000",
		"not-a-url",
	}
	for _, u := range denied {
		if AllowedDevMediaEndpoint(u) {
			t.Fatalf("expected denied: %s", u)
		}
	}
}
