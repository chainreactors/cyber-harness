package curl

import (
	"net/url"
	"testing"
)

func TestNormalizeCurlURLPathRFCExamples(t *testing.T) {
	cases := map[string]string{
		"/a/b/c/./../../g":   "/a/g",
		"/a/b/c/./../../g/":  "/a/g/",
		"/a/b/c/../..":       "/a/",
		"/a/b/c/../../..":    "/",
		"/a/b/c/../../../g":  "/g",
		"/a/b/c/./../../g/.": "/a/g/",
	}
	for input, want := range cases {
		u, err := url.Parse("http://example.test" + input)
		if err != nil {
			t.Fatal(err)
		}
		normalizeCurlURLPath(u)
		if got := u.EscapedPath(); got != want {
			t.Errorf("%s => %s, want %s", input, got, want)
		}
	}
}

func TestNormalizeCurlURLPathRepeatedSlashExamples(t *testing.T) {
	cases := map[string]string{
		"//a///b":    "/a///b",
		"/a//../b":   "/a/b",
		"/a/./b":     "/a/b",
		"/../x":      "/x",
		"/../../x":   "/x",
		"/a//b/../c": "/a//c",
	}
	for input, want := range cases {
		u, _ := url.Parse("http://example.test" + input)
		normalizeCurlURLPath(u)
		if got := u.EscapedPath(); got != want {
			t.Errorf("%s => %s, want %s", input, got, want)
		}
	}
}
