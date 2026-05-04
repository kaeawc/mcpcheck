package rules

import "testing"

func TestFindSecretMention(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain mention", "Returns the user's password.", "password"},
		{"api_key snake", "Set the api_key header.", "api_key"},
		{"api-key kebab", "Set the api-key header.", "api-key"},
		{"apikey unspaced", "Provide an apikey.", "apikey"},
		// Space-separated compound forms fall through to the bare-token match,
		// since the compound alternative requires "_", "-", or no separator.
		{"access token spaced", "Returns the access token.", "token"},
		{"access_token", "Returns the access_token.", "access_token"},
		{"bearer", "Pass a bearer in the Authorization header.", "bearer"},
		{"client secret", "Configure the client_secret.", "client_secret"},

		// Word-boundary negatives: shouldn't false-positive on lookalikes.
		{"tokenizer", "Tokenize input using a fast tokenizer.", ""},
		{"secretly", "The system secretly batches calls.", ""},
		{"api_keys plural", "Lists configured api_keys.", ""},
		{"benign", "Search the web for a query.", ""},

		// Substring inside a larger word should not match.
		{"password inside id", "passwordless flow", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findSecretMention(tc.in); got != tc.want {
				t.Fatalf("findSecretMention(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
