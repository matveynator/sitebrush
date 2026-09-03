package main

import "testing"

func TestResultHostResourceURLPreserved(t *testing.T) {
	spider := &pageSpider{domain: "kavtrans.sitebrush.ru"}
	for _, rawURL := range []string{
		"https://kavtrans.sitebrush.ru/files/map.pdf",
		"https://KAVTRANS.SITEBRUSH.RU/files/archive.zip",
		"https://kavtrans.sitebrush.ru:443/files/manual.docx",
	} {
		if !spider.isResultHostResourceURL(rawURL) {
			t.Fatalf("result-host resource was not preserved: %s", rawURL)
		}
	}
	if spider.isResultHostResourceURL("https://source.example/files/map.pdf") {
		t.Fatal("external resource was incorrectly treated as a result-host resource")
	}
	if spider.isResultHostResourceURL("://bad-url") {
		t.Fatal("invalid URL was treated as a result-host resource")
	}
	if (&pageSpider{}).isResultHostResourceURL("https://kavtrans.sitebrush.ru/files/map.pdf") {
		t.Fatal("resource matched without a result domain")
	}
}
