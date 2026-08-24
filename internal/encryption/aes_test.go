package encryption

import "testing"

// These vectors pin this package to the exact
// bytes Flexcube expects - if a change here breaks them, the proxy would be
// speaking a dialect the upstream does not.
func TestWireFormatCompatibility(t *testing.T) {
	cases := []struct {
		plain string
		pass  string
		want  string
	}{
		{
			plain: `{"Data":{"AccountNumber":"0075010012345678"}}`,
			pass:  "secrets",
			want:  "v/p75uwFuVoKOX53uMAg9KQJ4FbSw/axBv7QrgXii5ckOzAcfnN+X0gM42VNIWth.AAAAAAAAAAAAAAAAAAAAAA==",
		},
		{
			plain: "hello world",
			pass:  "secrets",
			want:  "dlwzJ0vkSHLIDnwpY+im+Q==.AAAAAAAAAAAAAAAAAAAAAA==",
		},
	}

	for _, tc := range cases {
		got, err := EncryptAES(tc.plain, tc.pass)
		if err != nil {
			t.Fatalf("EncryptAES(%q): %v", tc.plain, err)
		}
		if got != tc.want {
			t.Errorf("EncryptAES(%q) drifted from the expected wire format\n got: %s\nwant: %s", tc.plain, got, tc.want)
		}

		back, err := DecryptAES(tc.want, tc.pass)
		if err != nil {
			t.Fatalf("DecryptAES(%q): %v", tc.want, err)
		}
		if back != tc.plain {
			t.Errorf("DecryptAES round trip = %q, want %q", back, tc.plain)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, plain := range []string{"", "a", "exactly-16-bytes", `{"nested":{"x":[1,2,3]}}`} {
		encrypted, err := EncryptAES(plain, "secrets")
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		decrypted, err := DecryptAES(encrypted, "secrets")
		if err != nil {
			t.Fatalf("decrypt %q: %v", plain, err)
		}
		if decrypted != plain {
			t.Errorf("round trip = %q, want %q", decrypted, plain)
		}
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	cases := map[string]string{
		"no separator":     "notbase64atall",
		"bad base64":       "!!!!.AAAAAAAAAAAAAAAAAAAAAA==",
		"short iv":         "dlwzJ0vkSHLIDnwpY+im+Q==.AAAA",
		"wrong block size": "dlwzJ0vkSHLIDnwpY+im.AAAAAAAAAAAAAAAAAAAAAA==",
	}
	for name, input := range cases {
		if _, err := DecryptAES(input, "secrets"); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// A wrong key must fail rather than return junk, so a misconfigured secret is
// visible instead of silently producing garbage.
func TestWrongPasswordFails(t *testing.T) {
	encrypted, err := EncryptAES(`{"a":1}`, "secrets")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := DecryptAES(encrypted, "not-the-secret"); err == nil {
		t.Errorf("decrypted with the wrong key: %q", out)
	}
}
