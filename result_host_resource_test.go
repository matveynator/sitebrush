package main

import (
	"net/url"
	"testing"
)

func TestResultHostResourceURLPreserved(t *testing.T) {
	resultURL, err := url.Parse("https://kavtrans.sitebrush.ru/")
	if err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{
		"https://kavtrans.sitebrush.ru/files/map.pdf",
		"https://KAVTRANS.SITEBRUSH.RU/files/archive.zip",
	} {
		if !isResultHostResourceURL(rawURL, resultURL) {
			t.Fatalf("result-host resource was not preserved: %s", rawURL)
		}
	}
	if isResultHostResourceURL("https://source.example/files/map.pdf", resultURL) {
		t.Fatal("external resource was incorrectly treated as a result-host resource")
	}
	if isResultHostResourceURL("://bad-url", resultURL) {
		t.Fatal("invalid URL was treated as a result-host resource")
	}
	if isResultHostResourceURL("https://kavtrans.sitebrush.ru/files/map.pdf", nil) {
		t.Fatal("resource matched without a result URL")
	}
}
