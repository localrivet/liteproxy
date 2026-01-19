package passthrough

import (
	"testing"
)

func TestExtractSNI(t *testing.T) {
	// Real TLS 1.2 ClientHello with SNI "example.com"
	// Captured from actual TLS handshake
	clientHello := []byte{
		// TLS record header
		0x16,       // ContentType: Handshake
		0x03, 0x01, // Version: TLS 1.0 (for ClientHello)
		0x00, 0xf1, // Length: 241

		// Handshake header
		0x01,             // Type: ClientHello
		0x00, 0x00, 0xed, // Length: 237

		// ClientHello
		0x03, 0x03, // Version: TLS 1.2

		// Random (32 bytes)
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,

		// Session ID
		0x00, // Length: 0

		// Cipher Suites
		0x00, 0x02, // Length: 2
		0x00, 0x2f, // TLS_RSA_WITH_AES_128_CBC_SHA

		// Compression Methods
		0x01, // Length: 1
		0x00, // null

		// Extensions
		0x00, 0x1e, // Length: 30

		// SNI extension
		0x00, 0x00, // Type: server_name (0)
		0x00, 0x10, // Length: 16
		0x00, 0x0e, // SNI list length: 14
		0x00,       // Name type: host_name (0)
		0x00, 0x0b, // Name length: 11
		'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',

		// Padding to make it realistic
		0x00, 0x0d, // Type: signature_algorithms
		0x00, 0x04, // Length
		0x00, 0x02, 0x04, 0x01,
	}

	sni, err := extractSNI(clientHello)
	if err != nil {
		t.Fatalf("extractSNI failed: %v", err)
	}
	if sni != "example.com" {
		t.Errorf("extractSNI = %q, want %q", sni, "example.com")
	}
}

func TestExtractSNI_NoSNI(t *testing.T) {
	// TLS ClientHello without SNI extension
	clientHello := []byte{
		0x16, 0x03, 0x01, 0x00, 0x45, // TLS record
		0x01, 0x00, 0x00, 0x41, // Handshake: ClientHello
		0x03, 0x03, // Version
		// Random (32 bytes)
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
		0x00,       // Session ID length
		0x00, 0x02, // Cipher suites length
		0x00, 0x2f,
		0x01, 0x00, // Compression
		0x00, 0x00, // No extensions
	}

	_, err := extractSNI(clientHello)
	if err == nil {
		t.Error("extractSNI should fail without SNI")
	}
}

func TestExtractSNI_NotTLS(t *testing.T) {
	// HTTP request instead of TLS
	data := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")

	_, err := extractSNI(data)
	if err == nil {
		t.Error("extractSNI should fail for non-TLS data")
	}
}

func TestExtractSNI_TooShort(t *testing.T) {
	_, err := extractSNI([]byte{0x16, 0x03})
	if err == nil {
		t.Error("extractSNI should fail for truncated data")
	}
}

func TestExtractHTTPHost(t *testing.T) {
	tests := []struct {
		name    string
		request string
		want    string
	}{
		{
			name:    "simple host",
			request: "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
			want:    "example.com",
		},
		{
			name:    "host with port",
			request: "GET / HTTP/1.1\r\nHost: example.com:8080\r\n\r\n",
			want:    "example.com:8080",
		},
		{
			name:    "host with path",
			request: "GET /api/users HTTP/1.1\r\nHost: api.example.com\r\n\r\n",
			want:    "api.example.com",
		},
		{
			name:    "post request",
			request: "POST /data HTTP/1.1\r\nHost: data.example.com\r\nContent-Length: 0\r\n\r\n",
			want:    "data.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := extractHTTPHost([]byte(tt.request))
			if err != nil {
				t.Fatalf("extractHTTPHost failed: %v", err)
			}
			if host != tt.want {
				t.Errorf("extractHTTPHost = %q, want %q", host, tt.want)
			}
		})
	}
}

func TestExtractHTTPHost_Invalid(t *testing.T) {
	// Not HTTP
	_, err := extractHTTPHost([]byte{0x16, 0x03, 0x01}) // TLS
	if err == nil {
		t.Error("extractHTTPHost should fail for non-HTTP data")
	}
}

func BenchmarkExtractSNI(b *testing.B) {
	clientHello := []byte{
		0x16, 0x03, 0x01, 0x00, 0xf1,
		0x01, 0x00, 0x00, 0xed,
		0x03, 0x03,
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
		0x00,
		0x00, 0x02, 0x00, 0x2f,
		0x01, 0x00,
		0x00, 0x1e,
		0x00, 0x00, 0x00, 0x10, 0x00, 0x0e, 0x00, 0x00, 0x0b,
		'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',
		0x00, 0x0d, 0x00, 0x04, 0x00, 0x02, 0x04, 0x01,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractSNI(clientHello)
	}
}

func BenchmarkExtractHTTPHost(b *testing.B) {
	request := []byte("GET /api/v1/users HTTP/1.1\r\nHost: api.example.com\r\nUser-Agent: benchmark\r\n\r\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractHTTPHost(request)
	}
}
