package backend

import "testing"

func TestIsUnwantedExtensionStartupURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
		want bool
	}{
		{name: "extension welcome", url: "chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef/welcome.html", want: true},
		{name: "extension options", url: "chrome-extension://abcdefghijklmnopqrstuvwxyzabcdef/options.html", want: true},
		{name: "extensions page", url: "chrome://extensions", want: true},
		{name: "extensions with slash", url: "chrome://extensions/", want: true},
		{name: "extensions with query", url: "chrome://extensions/?id=abc", want: true},
		{name: "extensions uppercase", url: "CHROME://EXTENSIONS", want: true},
		{name: "blank", url: "about:blank", want: false},
		{name: "https page", url: "https://www.google.com/", want: false},
		{name: "new tab", url: "chrome://newtab/", want: false},
		{name: "settings", url: "chrome://settings/", want: false},
		{name: "empty", url: "", want: false},
		{name: "extensionish host only", url: "chrome://extension", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isUnwantedExtensionStartupURL(tc.url); got != tc.want {
				t.Fatalf("isUnwantedExtensionStartupURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
