package pagearchive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"howett.net/plist"
)

func savedFirefox(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestFirefoxArchiveAndLocalReferences(t *testing.T) {
	payload := savedFirefox(t, map[string]string{
		"Saved/page.html":                   `<html><head><base href="https://original.example/"><link rel=stylesheet href="page_files/style.css"></head><body><img src=page_files/photo.png srcset="page_files/photo.png 1x, page_files/photo@2.png 2x"><iframe src="page_files/frame.html"></iframe></body></html>`,
		"Saved/page_files/style.css":        `@import "nested/extra.css";body{background:url("photo.png")}`,
		"Saved/page_files/nested/extra.css": `a{color:red}`,
		"Saved/page_files/photo.png":        "image",
		"Saved/page_files/photo@2.png":      "larger image",
		"Saved/page_files/frame.html":       `<img src="photo.png">`,
		"__MACOSX/._page.html":              "ignored",
	})
	archive, err := Read(bytes.NewReader(payload), int64(len(payload)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(archive.Main.URL, "/Saved/page.html") || len(archive.Resources) != 5 {
		t.Fatal(archive)
	}
	destinations := map[string]string{archive.Main.URL: "/about"}
	for _, resource := range archive.Resources {
		destinations[resource.URL] = "/p/local/" + strings.TrimPrefix(resource.URL, "https://saved-page.invalid/Saved/page_files/")
	}
	main, err := Rewrite(archive.Main, destinations)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`href="/p/local/style.css"`, `src="/p/local/photo.png"`, `/p/local/photo@2.png 2x`, `src="/p/local/frame.html"`} {
		if !strings.Contains(string(main), expected) {
			t.Fatalf("missing %s in %s", expected, main)
		}
	}
	if strings.Contains(string(main), "<base") {
		t.Fatal("remote base survived")
	}
	for _, resource := range archive.Resources {
		content, err := Rewrite(resource, destinations)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(resource.URL, "style.css") && (!strings.Contains(string(content), `"/p/local/nested/extra.css"`) || !strings.Contains(string(content), `/p/local/photo.png`)) {
			t.Fatal(string(content))
		}
		if strings.HasSuffix(resource.URL, "frame.html") && !strings.Contains(string(content), `/p/local/photo.png`) {
			t.Fatal(string(content))
		}
	}
}

func TestSafariXMLAndBinaryArchives(t *testing.T) {
	fixture := safariArchive{
		Main:      safariResource{URL: "https://example.org/source/page", MIME: "text/html", Encoding: "windows-1251", Content: []byte(`<meta charset="windows-1251"><title>` + string([]byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}) + `</title><img src="../photo.png"><iframe src="/frame"></iframe><a href="next#part">Next</a>`)},
		Resources: []safariResource{{URL: "https://example.org/photo.png", MIME: "image/png", Content: []byte("image")}},
		Frames:    []safariArchive{{Main: safariResource{URL: "https://example.org/frame", MIME: "text/html", Content: []byte(`<img src="/photo.png">`)}}},
	}
	for _, format := range []int{plist.XMLFormat, plist.BinaryFormat} {
		payload, err := plist.Marshal(fixture, format)
		if err != nil {
			t.Fatal(err)
		}
		archive, err := Read(bytes.NewReader(payload), int64(len(payload)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(archive.Resources) != 2 {
			t.Fatal("missing nested frame")
		}
		destinations := map[string]string{archive.Main.URL: "/about", "https://example.org/photo.png": "/p/photo.png", "https://example.org/frame": "/p/frame.html"}
		main, err := Rewrite(archive.Main, destinations)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"Привет", `charset="utf-8"`, `src="/p/photo.png"`, `src="/p/frame.html"`, `href="https://example.org/source/next#part"`} {
			if !strings.Contains(string(main), expected) {
				t.Fatalf("missing %s in %s", expected, main)
			}
		}
	}
}

func TestArchiveRejectsUnsafeAmbiguousAndCanceledUploads(t *testing.T) {
	for _, files := range []map[string]string{
		{"../escape.html": "<html>"}, {"/absolute.html": "<html>"}, {"C:/page.html": "<html>"}, {"page.html": "<html>", "second.html": "<html>"}, {"image.png": "image"},
	} {
		payload := savedFirefox(t, files)
		if _, err := Read(bytes.NewReader(payload), int64(len(payload)), nil); err == nil {
			t.Fatal("accepted", files)
		}
	}
	stop := make(chan struct{})
	close(stop)
	payload := savedFirefox(t, map[string]string{"page.html": "<html>"})
	if _, err := Read(bytes.NewReader(payload), int64(len(payload)), stop); err == nil {
		t.Fatal("ignored terminated consumer")
	}
	if _, err := Read(bytes.NewReader([]byte("garbage")), 7, nil); err == nil {
		t.Fatal("accepted unknown format")
	}
	for _, name := range []string{"a/../b", "a\\b", "a/./b", "a//b"} {
		if _, err := safeName(name); err == nil {
			t.Fatal("accepted", name)
		}
	}
}

func TestPlistBoundsBeforeDecoderAllocation(t *testing.T) {
	payload := append([]byte("bplist00"), make([]byte, 32)...)
	payload[8+6], payload[8+7] = 1, 1
	binary.BigEndian.PutUint64(payload[8+8:], 1<<60)
	if err := checkPlistLimits(payload, nil); err == nil {
		t.Fatal("accepted huge object table")
	}
	if err := checkPlistLimits([]byte(strings.Repeat("<array>", 129)+strings.Repeat("</array>", 129)), nil); err == nil {
		t.Fatal("accepted excessive XML nesting")
	}
}

func FuzzArchiveReader(f *testing.F) {
	f.Add([]byte("bplist00"))
	f.Add([]byte("<?xml version=\"1.0\"?><plist><dict/></plist>"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) <= 1<<20 {
			_, _ = Read(bytes.NewReader(payload), int64(len(payload)), nil)
		}
	})
}
