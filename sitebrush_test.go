package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/matveynator/netchan"
	"github.com/matveynator/sitebrush/v2/pkg/channelacme"
	"github.com/matveynator/sitebrush/v2/pkg/crawler"
	"github.com/matveynator/sitebrush/v2/pkg/demo"
	"github.com/matveynator/sitebrush/v2/pkg/diagnosticlog"
	"github.com/matveynator/sitebrush/v2/pkg/dirprotect"
	"github.com/matveynator/sitebrush/v2/pkg/diskusage"
	"github.com/matveynator/sitebrush/v2/pkg/expenses"
	"github.com/matveynator/sitebrush/v2/pkg/hostingandsupport"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
	"github.com/matveynator/sitebrush/v2/pkg/sitebrushtemplate"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/text/encoding/charmap"
)

type automaticSSLIssuerFunc func(context.Context, string) channelacme.IssueResult

func (issue automaticSSLIssuerFunc) Issue(ctx context.Context, domain string) channelacme.IssueResult {
	return issue(ctx, domain)
}

func openServerControlDatabaseForTest(ctx context.Context, application *App) (*sql.DB, error) {
	databasePath, err := application.writablePathInsideStorage(application.serverControlDBPath())
	if err != nil {
		return nil, err
	}
	if err := ensureParentDir(databasePath); err != nil {
		return nil, err
	}
	database, err := openServerControlDatabaseHandle(databasePath, true)
	if err != nil {
		return nil, err
	}
	if err := hostingandsupport.Migrate(ctx, database); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

type fakeGrabTransport struct {
	responses map[string]fakeGrabResponse
}

type fakeGrabResponse struct {
	statusCode  int
	location    string
	contentType string
	body        string
}

func TestVersionCommandPrintsOnlyVersionWithoutCreatingStorage(t *testing.T) {
	binaryName := "sitebrush-version-test"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	buildCommand := exec.Command("go", "build", "-o", binaryPath, "-ldflags=-X main.CompileVersion=version-test", ".")
	if buildOutput, buildErr := buildCommand.CombinedOutput(); buildErr != nil {
		t.Fatalf("build version test binary: %v\n%s", buildErr, buildOutput)
	}

	for _, versionFlag := range []string{"--version", "-version", "-v"} {
		t.Run(versionFlag, func(t *testing.T) {
			homePath := t.TempDir()
			command := exec.Command(binaryPath, versionFlag)
			command.Env = append(os.Environ(),
				"HOME="+homePath,
				"XDG_CONFIG_HOME="+filepath.Join(homePath, "config"),
				"XDG_DATA_HOME="+filepath.Join(homePath, "data"),
			)
			var standardOutput bytes.Buffer
			var standardError bytes.Buffer
			command.Stdout = &standardOutput
			command.Stderr = &standardError
			if commandErr := command.Run(); commandErr != nil {
				t.Fatalf("run %s: %v", versionFlag, commandErr)
			}
			if standardOutput.String() != "version-test\n" {
				t.Fatalf("stdout = %q", standardOutput.String())
			}
			if standardError.Len() != 0 {
				t.Fatalf("stderr = %q", standardError.String())
			}
			homeEntries, readErr := os.ReadDir(homePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(homeEntries) != 0 {
				t.Fatalf("version command created files in HOME: %#v", homeEntries)
			}
		})
	}
}

func TestEffectiveGrabResourceContentTypeTrustsJavaScriptExtension(t *testing.T) {
	contentType := crawler.EffectiveResourceContentType("http://oldkmv.uprof.info/js/CurrentTime.js", "text/html; charset=windows-1251")
	if contentType != "application/javascript" {
		t.Fatalf("content type = %q", contentType)
	}
}

func TestDecodeImportedResourceBytesConvertsJavaScriptToUTF8(t *testing.T) {
	sourceText := `var title = "Текущее время";`
	sourceBytes, encodeErr := charmap.Windows1251.NewEncoder().Bytes([]byte(sourceText))
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}

	decodedBytes := decodeImportedResourceBytes("http://oldkmv.uprof.info/js/CurrentTime.js", sourceBytes, "text/html; charset=windows-1251", "application/javascript")
	if string(decodedBytes) != sourceText {
		t.Fatalf("decoded javascript = %q", string(decodedBytes))
	}
}

func TestDecodeImportedResourceBytesRewritesCSSCharset(t *testing.T) {
	sourceText := `@charset "windows-1251";` + "\n" + `.title{content:"Привет";}`
	sourceBytes, encodeErr := charmap.Windows1251.NewEncoder().Bytes([]byte(sourceText))
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}

	decodedBytes := decodeImportedResourceBytes("http://example.test/style.css", sourceBytes, "text/css; charset=windows-1251", "text/css")
	decodedText := string(decodedBytes)
	if !strings.Contains(decodedText, `@charset "utf-8";`) || !strings.Contains(decodedText, `Привет`) {
		t.Fatalf("decoded css = %q", decodedText)
	}
}

func TestPublicOutboundIPAllowedRejectsPrivateNetworks(t *testing.T) {
	blockedIPs := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.0.1",
		"169.254.1.1",
		"100.64.0.1",
		"198.51.100.1",
		"::1",
		"fc00::1",
		"fe80::1",
	}
	for _, ipText := range blockedIPs {
		if publicOutboundIPAllowed(net.ParseIP(ipText)) {
			t.Fatalf("private or special IP %s was allowed", ipText)
		}
	}
	allowedIPs := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, ipText := range allowedIPs {
		if !publicOutboundIPAllowed(net.ParseIP(ipText)) {
			t.Fatalf("public IP %s was rejected", ipText)
		}
	}
}

func TestRequirePublicOutboundURLRejectsCredentialsAndPrivateIP(t *testing.T) {
	privateURL, _ := url.Parse("https://127.0.0.1/")
	if err := requirePublicOutboundURL(privateURL); err == nil {
		t.Fatal("private IP URL was allowed")
	}
	credentialURL, _ := url.Parse("https://user:pass@example.com/")
	if err := requirePublicOutboundURL(credentialURL); err == nil {
		t.Fatal("credential URL was allowed")
	}
	publicURL, _ := url.Parse("https://example.com/")
	if err := requirePublicOutboundURL(publicURL); err != nil {
		t.Fatalf("public URL was rejected: %v", err)
	}
}

func TestReadRequestBodyWithLimitRejectsOversizedBody(t *testing.T) {
	if _, err := readRequestBodyWithLimit(strings.NewReader("abcdef"), 5); !errors.Is(err, errReadLimitExceeded) {
		t.Fatalf("oversized body error = %v", err)
	}
	body, err := readRequestBodyWithLimit(strings.NewReader("abcde"), 5)
	if err != nil {
		t.Fatalf("body at limit rejected: %v", err)
	}
	if string(body) != "abcde" {
		t.Fatalf("body = %q", string(body))
	}
}

func TestServiceMailRelayEndpointRejectsOversizedBody(t *testing.T) {
	application := &App{}
	requestBody := strings.NewReader(strings.Repeat("x", int(serviceMailRelayBodyLimitBytes)+1))
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?service_mail_relay", requestBody)
	response := httptest.NewRecorder()
	application.serviceMailRelayEndpoint(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func mustDNSNameForTest(name string) dnsmessage.Name {
	dnsName, err := dnsmessage.NewName(name)
	if err != nil {
		panic(err)
	}
	return dnsName
}

func writeCachedAutoCertForTest(t *testing.T, application *App, domain string, notBefore time.Time, notAfter time.Time) {
	t.Helper()
	certificatePEM := cachedAutoCertPEMForTest(t, domain, notBefore, notAfter)
	certificateCacheDir := filepath.Join(application.storageRootDir(), "letsencrypt")
	if err := os.MkdirAll(certificateCacheDir, 0o700); err != nil {
		t.Fatalf("create certificate cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(certificateCacheDir, domain), certificatePEM, 0o600); err != nil {
		t.Fatalf("write cached certificate: %v", err)
	}
}

func cachedAutoCertPEMForTest(t *testing.T, domain string, notBefore time.Time, notAfter time.Time) []byte {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate certificate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		DNSNames:              []string{domain},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal certificate key: %v", err)
	}
	var certificatePEM bytes.Buffer
	if err := pem.Encode(&certificatePEM, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER}); err != nil {
		t.Fatalf("encode private key: %v", err)
	}
	if err := pem.Encode(&certificatePEM, &pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}); err != nil {
		t.Fatalf("encode certificate: %v", err)
	}
	return certificatePEM.Bytes()
}

func newTestApplication(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	storagePath := t.TempDir()
	storageRealRoot, err := prepareStorageJailRoot(storagePath)
	if err != nil {
		t.Fatalf("prepare storage root: %v", err)
	}
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{
		db:                        rawDB,
		storagePath:               storagePath,
		storageRealRoot:           storageRealRoot,
		grabTracker:               newGrabProgressTracker(),
		grabCancels:               newGrabCancelTracker(),
		trialPreviews:             newPublicTrialPreviewStore(),
		publishTracker:            newPublishProgressTracker(),
		analyticsEvents:           make(chan siteAnalyticsEvent, 1024),
		authIPFailureCache:        startAuthIPFailureCacheWorker(context.Background()),
		registrationConfirmations: startEmailConfirmationMemoryWorker(context.Background()),
		emailDelivery:             make(chan mailout.DeliveryJob, mailout.DeliveryQueueSize),
	}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return application, rawDB
}

func newPagePasswordTestCookie(rule PagePasswordRule, request *http.Request, issuedAt time.Time) *http.Cookie {
	rule.Domain = normalizeDomainName(rule.Domain)
	return &http.Cookie{
		Name:  dirprotect.CookieName(rule.Domain, rule.Path),
		Value: dirprotect.BoundSessionToken(rule, clientIPAddress(request), request.UserAgent(), issuedAt),
	}
}

func TestStoragePathFallsBackToUserDirectoryWhenSystemDirectoryIsUnavailable(t *testing.T) {
	temporaryRoot := t.TempDir()
	unavailableSystemPath := filepath.Join(temporaryRoot, "system-storage")
	if err := os.WriteFile(unavailableSystemPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create unavailable system path: %v", err)
	}
	userStoragePath := filepath.Join(temporaryRoot, "user", ".sitebrush")

	preparedStoragePath, usedFallback, err := prepareStoragePathWithFallback(unavailableSystemPath, userStoragePath, true)
	if err != nil {
		t.Fatalf("prepare storage with fallback: %v", err)
	}
	if !usedFallback {
		t.Fatal("user storage fallback was not used")
	}
	if !sameCleanPath(preparedStoragePath, userStoragePath) {
		t.Fatalf("prepared storage path = %q, want %q", preparedStoragePath, userStoragePath)
	}
	if fileInfo, statErr := os.Stat(userStoragePath); statErr != nil || !fileInfo.IsDir() {
		t.Fatalf("user storage directory was not created: info=%#v err=%v", fileInfo, statErr)
	}
}

func TestExplicitStoragePathDoesNotFallBack(t *testing.T) {
	temporaryRoot := t.TempDir()
	unavailableStoragePath := filepath.Join(temporaryRoot, "configured-storage")
	if err := os.WriteFile(unavailableStoragePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create unavailable configured path: %v", err)
	}

	_, usedFallback, err := prepareStoragePathWithFallback(unavailableStoragePath, filepath.Join(temporaryRoot, "fallback"), false)
	if err == nil {
		t.Fatal("unavailable explicit storage path was accepted")
	}
	if usedFallback {
		t.Fatal("explicit storage path unexpectedly used fallback")
	}
}

func TestUserStoragePathUsesHiddenDirectoryInHome(t *testing.T) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(homeDirectory, ".sitebrush")
	if got := userAppStoragePath(); !sameCleanPath(got, want) {
		t.Fatalf("user storage path = %q, want %q", got, want)
	}
}

func TestLinuxDefaultStorageUsesUserDirectoryWithoutAdministrativeRights(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific default storage policy")
	}
	got := defaultAppStoragePath()
	if sameCleanPath(got, "/var/lib/sitebrush") {
		t.Skip("process has administrative Linux storage privileges")
	}
	if want := userAppStoragePath(); !sameCleanPath(got, want) {
		t.Fatalf("default storage path = %q, want user path %q", got, want)
	}
}

func TestStorageJailResolvesSymlinkRootAndRejectsEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	realStorageRoot := t.TempDir()
	linkParent := t.TempDir()
	linkStorageRoot := filepath.Join(linkParent, "sitebrush-storage")
	if err := os.Symlink(realStorageRoot, linkStorageRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	application := &App{storagePath: linkStorageRoot}
	resolvedRoot, err := application.storageJailRootPath()
	if err != nil {
		t.Fatalf("storageJailRootPath: %v", err)
	}
	expectedStorageRoot, err := filepath.EvalSymlinks(realStorageRoot)
	if err != nil {
		t.Fatalf("resolve expected storage root: %v", err)
	}
	if !sameCleanPath(resolvedRoot, expectedStorageRoot) {
		t.Fatalf("resolved storage root = %q, want %q", resolvedRoot, expectedStorageRoot)
	}

	outsideFilePath := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outsideFilePath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if _, err := application.existingPathInsideStorage(outsideFilePath); err == nil {
		t.Fatalf("outside file was accepted inside storage")
	}

	siteFilesRoot := application.domainFilesDirForDomain("site.example")
	if err := application.mkdirAllInsideStorage(siteFilesRoot, 0o755); err != nil {
		t.Fatalf("create site files root: %v", err)
	}
	leakPath := filepath.Join(siteFilesRoot, "leak.txt")
	if err := os.Symlink(outsideFilePath, leakPath); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	if _, err := application.existingPathInsideStorageSubtree(siteFilesRoot, leakPath); err == nil {
		t.Fatalf("symlink escape from site files root was accepted")
	}

	outsideDirectoryPath := t.TempDir()
	escapeDirectoryPath := filepath.Join(siteFilesRoot, "escape-dir")
	if err := os.Symlink(outsideDirectoryPath, escapeDirectoryPath); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if _, err := application.writablePathInsideStorageSubtree(siteFilesRoot, filepath.Join(escapeDirectoryPath, "created.txt")); err == nil {
		t.Fatalf("write through symlinked parent was accepted")
	}
}

func TestPublicAssetRejectsSymlinkToNeighborSite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	application := &App{storagePath: t.TempDir()}
	siteDomain := "a.example"
	neighborDomain := "b.example"
	siteFilesRoot := application.domainFilesDirForDomain(siteDomain)
	neighborFilesRoot := application.domainFilesDirForDomain(neighborDomain)
	if err := application.mkdirAllInsideStorage(siteFilesRoot, 0o755); err != nil {
		t.Fatalf("create site files root: %v", err)
	}
	if err := application.mkdirAllInsideStorage(neighborFilesRoot, 0o755); err != nil {
		t.Fatalf("create neighbor files root: %v", err)
	}
	neighborSecretPath := filepath.Join(neighborFilesRoot, "secret.txt")
	if err := application.writeFileInsideStorage(neighborSecretPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write neighbor secret: %v", err)
	}
	if err := os.Symlink(neighborSecretPath, filepath.Join(siteFilesRoot, "leak.txt")); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	if application.publicAssetExists(domainStorageName(siteDomain), "leak.txt") {
		t.Fatalf("public asset lookup accepted symlink into neighbor site")
	}
}

func TestPublicAssetRejectsSymlinkedSiteRootToNeighborSite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	application := &App{storagePath: t.TempDir()}
	siteDomain := "a.example"
	neighborDomain := "b.example"
	siteFilesRoot := application.domainFilesDirForDomain(siteDomain)
	neighborFilesRoot := application.domainFilesDirForDomain(neighborDomain)
	if err := application.mkdirAllInsideStorage(filepath.Dir(siteFilesRoot), 0o755); err != nil {
		t.Fatalf("create files root: %v", err)
	}
	if err := application.mkdirAllInsideStorage(neighborFilesRoot, 0o755); err != nil {
		t.Fatalf("create neighbor files root: %v", err)
	}
	neighborSecretPath := filepath.Join(neighborFilesRoot, "secret.txt")
	if err := application.writeFileInsideStorage(neighborSecretPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write neighbor secret: %v", err)
	}
	if err := os.Symlink(neighborFilesRoot, siteFilesRoot); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if application.publicAssetExists(domainStorageName(siteDomain), "secret.txt") {
		t.Fatalf("public asset lookup accepted symlinked site root into neighbor site")
	}
}

func TestPublishedStaticRejectsSymlinkToNeighborSite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on Windows")
	}
	application := &App{storagePath: t.TempDir()}
	siteDomain := "a.example"
	neighborDomain := "b.example"
	siteStaticRoot := application.domainStaticDir(siteDomain)
	neighborStaticRoot := application.domainStaticDir(neighborDomain)
	if err := application.mkdirAllInsideStorage(siteStaticRoot, 0o755); err != nil {
		t.Fatalf("create site static root: %v", err)
	}
	if err := application.mkdirAllInsideStorage(neighborStaticRoot, 0o755); err != nil {
		t.Fatalf("create neighbor static root: %v", err)
	}
	neighborIndexPath := filepath.Join(neighborStaticRoot, "index.html")
	if err := application.writeFileInsideStorage(neighborIndexPath, []byte("<!doctype html><p>secret</p>"), 0o644); err != nil {
		t.Fatalf("write neighbor static file: %v", err)
	}
	if err := os.Symlink(neighborIndexPath, filepath.Join(siteStaticRoot, "index.html")); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	if application.servePublishedStaticFileFromDisk(response, request, siteDomain, "/", false) {
		t.Fatalf("published static serving accepted symlink into neighbor site")
	}
}

func TestOpenServerControlDatabaseRejectsPathOutsideStorage(t *testing.T) {
	application := &App{storagePath: t.TempDir(), dbPath: filepath.Join(t.TempDir(), "outside.db")}
	if err := application.withServerControlDatabaseWrite(context.Background(), "test-outside-path", func(*sql.DB) error { return nil }); err == nil {
		t.Fatalf("openServerControlDatabase accepted db path outside storage")
	}
}

func TestServerControlDatabaseDispatcherLimitsAccessAndCancelsReply(t *testing.T) {
	dispatcher, err := startServerControlDatabaseDispatcher(filepath.Join(t.TempDir(), "control.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()

	firstWriterStarted := make(chan struct{})
	releaseFirstWriter := make(chan struct{})
	firstWriterResult := make(chan error, 1)
	go func() {
		firstWriterResult <- dispatcher.execute(context.Background(), serverControlDatabaseWrite, "first-writer", func(*sql.DB) error {
			close(firstWriterStarted)
			<-releaseFirstWriter
			return nil
		})
	}()
	<-firstWriterStarted
	secondWriterRan := make(chan struct{})
	secondWriterResult := make(chan error, 1)
	go func() {
		secondWriterResult <- dispatcher.execute(context.Background(), serverControlDatabaseWrite, "second-writer", func(*sql.DB) error {
			close(secondWriterRan)
			return nil
		})
	}()
	select {
	case <-secondWriterRan:
		t.Fatal("second writer ran while the first writer was active")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirstWriter)
	if err := <-firstWriterResult; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondWriterResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second writer did not run after the first writer released access")
	}

	releaseReaders := make(chan struct{})
	readerResults := make(chan error, serverControlDatabaseReaderCount)
	readerStarted := make(chan struct{}, serverControlDatabaseReaderCount)
	for range serverControlDatabaseReaderCount {
		go func() {
			readerResults <- dispatcher.execute(context.Background(), serverControlDatabaseRead, "held-reader", func(*sql.DB) error {
				readerStarted <- struct{}{}
				<-releaseReaders
				return nil
			})
		}()
	}
	for range serverControlDatabaseReaderCount {
		<-readerStarted
	}
	sixthReaderRan := make(chan struct{})
	sixthReaderResult := make(chan error, 1)
	go func() {
		sixthReaderResult <- dispatcher.execute(context.Background(), serverControlDatabaseRead, "sixth-reader", func(*sql.DB) error {
			close(sixthReaderRan)
			return nil
		})
	}()
	select {
	case <-sixthReaderRan:
		t.Fatal("sixth reader ran while five readers were active")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseReaders)
	select {
	case err := <-sixthReaderResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sixth reader did not run after one reader released access")
	}
	for range serverControlDatabaseReaderCount {
		if err := <-readerResults; err != nil {
			t.Fatal(err)
		}
	}

	blockingWriterStarted := make(chan struct{})
	releaseBlockingWriter := make(chan struct{})
	go func() {
		_ = dispatcher.execute(context.Background(), serverControlDatabaseWrite, "blocking-writer", func(*sql.DB) error {
			close(blockingWriterStarted)
			<-releaseBlockingWriter
			return nil
		})
	}()
	<-blockingWriterStarted
	timeoutContext, cancelTimeout := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelTimeout()
	if timeoutErr := dispatcher.execute(timeoutContext, serverControlDatabaseWrite, "timed-out-writer", func(*sql.DB) error { return nil }); !errors.Is(timeoutErr, context.DeadlineExceeded) {
		t.Fatalf("timed out writer error = %v, want context deadline exceeded", timeoutErr)
	}
	close(releaseBlockingWriter)
	dispatcher.Close()
}

func TestServerControlDatabaseDispatcherSerializesWritesAndKeepsWALReadersAvailable(t *testing.T) {
	dispatcher, err := startServerControlDatabaseDispatcher(filepath.Join(t.TempDir(), "control.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()

	if err := dispatcher.execute(context.Background(), serverControlDatabaseWrite, "create-test-table", func(database *sql.DB) error {
		_, err := database.ExecContext(context.Background(), `CREATE TABLE dispatcher_test(id INTEGER PRIMARY KEY,value TEXT)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	writerReady := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerResult := make(chan error, 1)
	go func() {
		writerResult <- dispatcher.execute(context.Background(), serverControlDatabaseWrite, "uncommitted-writer", func(database *sql.DB) error {
			transaction, err := database.BeginTx(context.Background(), nil)
			if err != nil {
				return err
			}
			if _, err := transaction.ExecContext(context.Background(), `INSERT INTO dispatcher_test(value) VALUES('pending')`); err != nil {
				_ = transaction.Rollback()
				return err
			}
			close(writerReady)
			<-releaseWriter
			return transaction.Rollback()
		})
	}()
	<-writerReady
	var visibleRows int
	if err := dispatcher.execute(context.Background(), serverControlDatabaseRead, "wal-reader", func(database *sql.DB) error {
		return database.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM dispatcher_test`).Scan(&visibleRows)
	}); err != nil {
		t.Fatal(err)
	}
	if visibleRows != 0 {
		t.Fatalf("reader saw uncommitted writer rows = %d", visibleRows)
	}
	close(releaseWriter)
	if err := <-writerResult; err != nil {
		t.Fatal(err)
	}

	const writeCount = 20
	writeResults := make(chan error, writeCount)
	for writeIndex := range writeCount {
		go func(index int) {
			writeResults <- dispatcher.execute(context.Background(), serverControlDatabaseWrite, "serialized-write", func(database *sql.DB) error {
				_, writeErr := database.ExecContext(context.Background(), `INSERT INTO dispatcher_test(value) VALUES(?)`, strconv.Itoa(index))
				return writeErr
			})
		}(writeIndex)
	}
	for range writeCount {
		if writeErr := <-writeResults; writeErr != nil {
			t.Fatalf("serialized write failed: %v", writeErr)
		}
	}
	if err := dispatcher.execute(context.Background(), serverControlDatabaseRead, "count-serialized-writes", func(database *sql.DB) error {
		return database.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM dispatcher_test`).Scan(&visibleRows)
	}); err != nil {
		t.Fatal(err)
	}
	if visibleRows != writeCount {
		t.Fatalf("visible rows = %d, want %d", visibleRows, writeCount)
	}
}

func TestSlowPublicTrialDownloadDoesNotBlockDemoSessionWrite(t *testing.T) {
	storagePath := t.TempDir()
	databasePath := filepath.Join(storagePath, "storage", "db", "sitebrush.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := startServerControlDatabaseDispatcher(databasePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()
	application := &App{
		storagePath:     storagePath,
		dbPath:          databasePath,
		controlDatabase: dispatcher,
		grabTracker:     newGrabProgressTracker(),
		trialPreviews:   newPublicTrialPreviewStore(),
	}

	requestStarted := make(chan struct{})
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			select {
			case <-requestStarted:
			default:
				close(requestStarted)
			}
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
	}
	t.Cleanup(func() { newGrabHTTPClient = previousGrabHTTPClient })

	cancelPreview := make(chan struct{})
	previewDone := make(chan struct{})
	go func() {
		application.runPublicTrialSitePreview("slow-preview", "https://mitsue.it/", "", nil, cancelPreview, false, grabSourceOptions{})
		close(previewDone)
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		close(cancelPreview)
		t.Fatal("public trial download did not start")
	}

	writeContext, cancelWrite := context.WithTimeout(context.Background(), time.Second)
	defer cancelWrite()
	event := demoSessionEvent{
		kind:         "create",
		domain:       "demo.sitebrush.com",
		sessionToken: "slow-preview-session",
		userEmail:    "demo@sitebrush.com",
		resetAfter:   time.Now().Add(time.Hour),
	}
	if err := application.persistDemoSessionEvent(writeContext, event); err != nil {
		close(cancelPreview)
		t.Fatalf("demo session write was blocked by public trial download: %v", err)
	}
	var status string
	if err := application.withServerControlDatabaseRead(writeContext, "verify-demo-session-write", func(database *sql.DB) error {
		return database.QueryRowContext(writeContext, `SELECT status FROM demo_site_sessions WHERE session_token=?`, event.sessionToken).Scan(&status)
	}); err != nil {
		close(cancelPreview)
		t.Fatal(err)
	}
	if status != "active" {
		close(cancelPreview)
		t.Fatalf("demo session status = %q, want active", status)
	}
	close(cancelPreview)
	select {
	case <-previewDone:
	case <-time.After(time.Second):
		t.Fatal("canceled public trial download did not stop")
	}
}

func TestGuestPublishedStaticRouteDoesNotWaitForControlDatabaseWriter(t *testing.T) {
	application := newRouterTestApplication(t)
	application.writePublishedStaticHTML("localhost", "/", "<html><body>ready</body></html>")

	if err := os.MkdirAll(filepath.Dir(application.serverControlDBPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := startServerControlDatabaseDispatcher(application.serverControlDBPath(), false)
	if err != nil {
		t.Fatal(err)
	}
	application.controlDatabase = dispatcher
	stopDemoRuntime := make(chan struct{})
	application.demoSiteRuntime = application.startDemoSiteRuntimeProcess(stopDemoRuntime)
	t.Cleanup(func() {
		close(stopDemoRuntime)
		dispatcher.Close()
	})

	writerStarted := make(chan struct{})
	releaseWriter := make(chan struct{})
	go func() {
		_ = dispatcher.execute(context.Background(), serverControlDatabaseWrite, "test-blocking-writer", func(*sql.DB) error {
			close(writerStarted)
			<-releaseWriter
			return nil
		})
	}()
	<-writerStarted
	defer close(releaseWriter)

	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	request = request.WithContext(contextWithDomain(request.Context(), "localhost"))
	response := httptest.NewRecorder()
	startedAt := time.Now()
	application.route(response, request)
	duration := time.Since(startedAt)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ready") {
		t.Fatalf("static response = %d %q", response.Code, response.Body.String())
	}
	if duration >= time.Second {
		t.Fatalf("static route waited for control database writer: %s", duration)
	}
}

func TestPreparedDemoSiteStartsAndLogsOutWhileControlDatabaseWriterIsBusy(t *testing.T) {
	application := newRouterTestApplication(t)
	controlDatabase := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	settings := demo.Settings{Domain: "demo-fast.example", Enabled: true}
	if err := (demo.Store{DB: controlDatabase}).SaveSettings(context.Background(), settings.Domain, "", false, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), settings, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := controlDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	dispatcher, err := startServerControlDatabaseDispatcher(application.serverControlDBPath(), false)
	if err != nil {
		t.Fatal(err)
	}
	application.controlDatabase = dispatcher
	stop := make(chan struct{})
	application.demoSessionEvents = application.startDemoSessionEventProcess(stop)
	application.demoSiteRuntime = application.startDemoSiteRuntimeProcess(stop)
	t.Cleanup(func() {
		close(stop)
		dispatcher.Close()
	})
	waitForDemoSiteRuntimeStatus(t, application, "ready")

	writerStarted := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		_ = dispatcher.execute(context.Background(), serverControlDatabaseWrite, "test-blocking-demo-writer", func(*sql.DB) error {
			close(writerStarted)
			<-releaseWriter
			return nil
		})
		close(writerDone)
	}()
	<-writerStarted
	request := httptest.NewRequest(http.MethodGet, "https://demo-fast.example/", nil)
	request = request.WithContext(contextWithDomain(request.Context(), settings.Domain))
	response := httptest.NewRecorder()
	startedAt := time.Now()
	application.route(response, request)
	if duration := time.Since(startedAt); duration >= time.Second {
		t.Fatalf("prepared demo start waited for control database: %s", duration)
	}
	if response.Code != http.StatusFound {
		t.Fatalf("prepared demo start status = %d, body=%q", response.Code, response.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, responseCookie := range response.Result().Cookies() {
		if responseCookie.Name == "sitebrush_session" {
			sessionCookie = responseCookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("prepared demo response did not set a session cookie")
	}
	close(releaseWriter)
	<-writerDone
	waitForDemoSessionStatus(t, application, sessionCookie.Value, "active")

	writerStarted = make(chan struct{})
	releaseWriter = make(chan struct{})
	writerDone = make(chan struct{})
	go func() {
		_ = dispatcher.execute(context.Background(), serverControlDatabaseWrite, "test-blocking-demo-logout-writer", func(*sql.DB) error {
			close(writerStarted)
			<-releaseWriter
			return nil
		})
		close(writerDone)
	}()
	<-writerStarted
	logoutRequest := httptest.NewRequest(http.MethodGet, "https://demo-fast.example/?logout", nil)
	logoutRequest = logoutRequest.WithContext(contextWithDomain(logoutRequest.Context(), settings.Domain))
	logoutRequest.AddCookie(sessionCookie)
	logoutResponse := httptest.NewRecorder()
	startedAt = time.Now()
	application.route(logoutResponse, logoutRequest)
	if duration := time.Since(startedAt); duration >= time.Second {
		t.Fatalf("prepared demo logout waited for control database: %s", duration)
	}
	if logoutResponse.Code != http.StatusFound {
		t.Fatalf("prepared demo logout status = %d, body=%q", logoutResponse.Code, logoutResponse.Body.String())
	}
	close(releaseWriter)
	<-writerDone
	waitForDemoSessionStatus(t, application, sessionCookie.Value, "deleting")
}

func waitForDemoSiteRuntimeStatus(t *testing.T, application *App, expectedStatus string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, found := application.runtimeDemoSiteState(context.Background())
		if found && state.Status == expectedStatus {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("demo runtime status = %q error=%q, want %q", state.Status, state.Error, expectedStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForDemoSessionStatus(t *testing.T, application *App, sessionToken, expectedStatus string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var status string
		queryErr := application.withServerControlDatabaseRead(context.Background(), "wait-demo-session-status", func(database *sql.DB) error {
			return database.QueryRowContext(context.Background(), `SELECT status FROM demo_site_sessions WHERE session_token=?`, sessionToken).Scan(&status)
		})
		if queryErr == nil && status == expectedStatus {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("demo session status = %q error=%v, want %q", status, queryErr, expectedStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHostingClientSiteViewsUsePreparedDNSResult(t *testing.T) {
	previousIPLookup := lookupIPRecords
	lookupIPRecords = func(string) ([]net.IP, error) {
		t.Fatal("prepared hosting view performed a live DNS lookup")
		return nil, nil
	}
	t.Cleanup(func() {
		lookupIPRecords = previousIPLookup
	})

	views := hostingAndSupportClientSiteViews(map[string]hostingAndSupportClientSiteSource{
		"site.example": {
			ip:                "203.0.113.10",
			dnsMatchesServer:  true,
			dnsCheckAvailable: true,
		},
	}, map[string]struct{}{})
	if len(views) != 1 || !views[0].RealInstallation {
		t.Fatalf("prepared DNS site view = %#v", views)
	}
}

func TestHostingAndSupportSnapshotUsesExistingWriterSession(t *testing.T) {
	storagePath := t.TempDir()
	storageRealRoot, err := prepareStorageJailRoot(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(storageRealRoot, "storage", "db", "sitebrush.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := startServerControlDatabaseDispatcher(databasePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()
	application := &App{
		storagePath:     storageRealRoot,
		storageRealRoot: storageRealRoot,
		dbPath:          databasePath,
		controlDatabase: dispatcher,
	}
	snapshotContext, cancelSnapshot := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSnapshot()
	if _, err := application.collectHostingAndSupportPanelSnapshot(snapshotContext, nil); err != nil {
		t.Fatalf("collect snapshot through dispatcher: %v", err)
	}
}

func captureImmediateProfileEmail(t *testing.T, application *App) {
	t.Helper()
	application.sendEmail = func(ctx context.Context, message mailout.Message) error {
		select {
		case application.emailDelivery <- mailout.DeliveryJob{Message: message}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestMigrateWithSingleSQLiteConnectionRebuildsPagePasswordPrefixFiles(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	rawDB.SetMaxOpenConns(1)
	rawDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO page_password_rules(domain,path,password_hash,created_at,updated_at) VALUES(?,?,?,?,?)`, "localhost", "/", "hash", time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert page password rule: %v", err)
	}

	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := application.migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if duration := time.Since(startedAt); duration > time.Second {
		t.Fatalf("second migrate took %s, want under 1s", duration)
	}
}

func withEmailSPFAllowed(t *testing.T) {
	t.Helper()
	t.Setenv("SITEBRUSH_SMTP_FROM", "SiteBrush <sitebrush@example.com>")
	previousTXTLookup := lookupTXTRecords
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	lookupTXTRecords = func(domain string) ([]string, error) {
		if domain != "example.com" {
			t.Fatalf("unexpected SPF lookup domain %q", domain)
		}
		return []string{"v=spf1 a mx ip4:203.0.113.10 ~all"}, nil
	}
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain != "example.com" {
			t.Fatalf("unexpected IP lookup domain %q", domain)
		}
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}
	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		return nil, nil
	}
	t.Cleanup(func() {
		lookupTXTRecords = previousTXTLookup
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	})
}

func withRegistrationDNS(t *testing.T, domain string, pointsToServer bool) {
	t.Helper()
	previousTXTLookup := lookupTXTRecords
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	lookupTXTRecords = func(lookupDomain string) ([]string, error) {
		if lookupDomain == "sender.example" {
			return []string{"v=spf1 a mx ip4:203.0.113.10 ~all"}, nil
		}
		if lookupDomain != domain {
			t.Fatalf("unexpected SPF lookup domain %q", lookupDomain)
		}
		if pointsToServer {
			return []string{"v=spf1 a mx ip4:203.0.113.10 ~all"}, nil
		}
		return []string{"v=spf1 a mx ip4:198.51.100.20 ~all"}, nil
	}
	lookupIPRecords = func(lookupDomain string) ([]net.IP, error) {
		if lookupDomain == "sender.example" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		if lookupDomain != domain {
			t.Fatalf("unexpected IP lookup domain %q", lookupDomain)
		}
		if pointsToServer {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return []net.IP{net.ParseIP("198.51.100.20")}, nil
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}
	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		return nil, nil
	}
	t.Cleanup(func() {
		lookupTXTRecords = previousTXTLookup
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	})
}

func confirmationTokenFromBody(t *testing.T, body string) string {
	t.Helper()
	marker := "email_confirm="
	startIndex := strings.Index(body, marker)
	if startIndex < 0 {
		t.Fatalf("confirmation body has no token: %q", body)
	}
	tokenStart := startIndex + len(marker)
	tokenEnd := tokenStart
	for tokenEnd < len(body) {
		switch body[tokenEnd] {
		case ' ', '\n', '\r', '\t', '&', '<', '"', '\'':
			rawToken := body[tokenStart:tokenEnd]
			token, err := url.QueryUnescape(rawToken)
			if err != nil {
				t.Fatalf("decode confirmation token %q: %v", rawToken, err)
			}
			return token
		}
		tokenEnd++
	}
	rawToken := body[tokenStart:tokenEnd]
	token, err := url.QueryUnescape(rawToken)
	if err != nil {
		t.Fatalf("decode confirmation token %q: %v", rawToken, err)
	}
	return token
}

func waitSiteDBRouterStartup(t *testing.T, router *perSiteDBRouter) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := router.MigrationStatus(context.Background(), "localhost")
		if status.startupFinished {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("site database router startup did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testStaticFiles(t *testing.T) fs.FS {
	t.Helper()
	staticFiles, err := fs.Sub(embeddedWebFiles, "web/static")
	if err != nil {
		t.Fatalf("static subfs: %v", err)
	}
	return staticFiles
}

type panicSQLExecutor struct {
	t *testing.T
}

func (executor panicSQLExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	executor.t.Fatalf("unexpected database exec on static fast path: %s", query)
	return nil, nil
}

func (executor panicSQLExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	executor.t.Fatalf("unexpected database query on static fast path: %s", query)
	return nil, nil
}

func (executor panicSQLExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	executor.t.Fatalf("unexpected database query-row on static fast path: %s", query)
	return nil
}

func newAdminSessionCookie(t *testing.T, application *App, email string) *http.Cookie {
	t.Helper()
	sessionRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	sessionResponse := httptest.NewRecorder()
	application.createSession(sessionResponse, sessionRequest, email)
	sessionCookies := sessionResponse.Result().Cookies()
	if len(sessionCookies) == 0 {
		t.Fatal("createSession did not set a cookie")
	}
	return sessionCookies[0]
}

type fakeHijackResponseWriter struct {
	header http.Header
	conn   *fakeHijackConn
}

type fakeHijackConn struct {
	bytes.Buffer
	closed bool
}

type fakeAddr string

func (writer *fakeHijackResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *fakeHijackResponseWriter) Write(payload []byte) (int, error) {
	return writer.conn.Write(payload)
}

func (writer *fakeHijackResponseWriter) WriteHeader(int) {}

func (writer *fakeHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return writer.conn, bufio.NewReadWriter(bufio.NewReader(writer.conn), bufio.NewWriter(writer.conn)), nil
}

func (conn *fakeHijackConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (conn *fakeHijackConn) Close() error {
	conn.closed = true
	return nil
}

func (conn *fakeHijackConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (conn *fakeHijackConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (conn *fakeHijackConn) SetDeadline(time.Time) error {
	return nil
}

func (conn *fakeHijackConn) SetReadDeadline(time.Time) error {
	return nil
}

func (conn *fakeHijackConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (addr fakeAddr) Network() string {
	return string(addr)
}

func (addr fakeAddr) String() string {
	return string(addr)
}

func (transport fakeGrabTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, found := transport.responses[request.URL.String()]
	if !found {
		return fakeGrabResponse{statusCode: http.StatusNotFound, body: "not found"}.httpResponse(request), nil
	}
	return response.httpResponse(request), nil
}

func (response fakeGrabResponse) httpResponse(request *http.Request) *http.Response {
	header := make(http.Header)
	if response.contentType != "" {
		header.Set("Content-Type", response.contentType)
	}
	if response.location != "" {
		header.Set("Location", response.location)
	}
	statusCode := response.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	statusText := http.StatusText(statusCode)
	if statusText == "" {
		statusText = "status"
	}
	return &http.Response{
		StatusCode:    statusCode,
		Status:        strconv.Itoa(statusCode) + " " + statusText,
		Body:          io.NopCloser(strings.NewReader(response.body)),
		ContentLength: int64(len(response.body)),
		Header:        header,
		Request:       request,
	}
}

func (roundTripper roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestValidateServiceMailSecretRejectsArbitraryText(t *testing.T) {
	if err := validateServiceMailSecret("login_code", "123456"); err != nil {
		t.Fatalf("valid login code rejected: %v", err)
	}
	if err := validateServiceMailSecret("login_code", "please send anything"); err == nil {
		t.Fatal("arbitrary login-code text was accepted")
	}
	if err := validateServiceMailSecret("email_confirm", "https://example.com/?email_confirm=token"); err != nil {
		t.Fatalf("valid confirmation link rejected: %v", err)
	}
	if err := validateServiceMailSecret("email_confirm", "custom text without link"); err == nil {
		t.Fatal("arbitrary confirmation text was accepted")
	}
}

func TestServiceMailRateLimitedIncludesSourceSubnet(t *testing.T) {
	application, _ := newTestApplication(t)
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDatabase.Close()
	store := hostingandsupport.Store{DB: controlDatabase}
	for eventIndex := 0; eventIndex < serviceMailPerSubnetHourLimit; eventIndex++ {
		if err := store.LogServiceMailEvent(context.Background(), hostingandsupport.ServiceMailEvent{
			InstallationID:  "installation-" + strconv.Itoa(eventIndex),
			SourceIP:        fmt.Sprintf("10.20.30.%d", eventIndex%200+1),
			Recipient:       fmt.Sprintf("user-%d@example.net", eventIndex),
			RecipientDomain: "example.net",
			CodeKind:        "login_code",
			Status:          "sent",
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, limited := serviceMailRateLimited(context.Background(), store, serviceMailRequest{
		InstallationID: "new-installation",
		Recipient:      "fresh@example.org",
		CodeKind:       "login_code",
	}, "10.20.30.250", "example.org")
	if !limited {
		t.Fatal("subnet limit did not block a new IP in the same /24")
	}
}

func TestServiceMailRelayRejectsLoginCodeForUnverifiedRecipient(t *testing.T) {
	application, _ := newTestApplication(t)
	request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      1,
		SourceDomain: "customer.example",
		Recipient:    "victim@example.net",
		CodeKind:     "login_code",
		SecretValue:  "123456",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	registerServiceMailInstallationForTest(t, application, request)
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: controlDatabase}
	if err := store.UpsertServiceMailRecipient(context.Background(), request.InstallationID, "owner@example.net", "verified", "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err := controlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "https://sitebrush.com/?service_mail_relay", nil)
	status, statusCode := application.handleServiceMailRelayRequest(context.Background(), httpRequest, request, "203.0.113.10")
	if statusCode != http.StatusForbidden {
		t.Fatalf("status = %d %q, want forbidden", statusCode, status)
	}
	if !strings.Contains(status, "not verified") {
		t.Fatalf("status = %q, want recipient verification error", status)
	}
}

func TestServiceMailRelayRejectsUnregisteredInstallation(t *testing.T) {
	application, _ := newTestApplication(t)
	request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      1,
		SourceDomain: "customer.example",
		Recipient:    "owner@example.net",
		CodeKind:     "login_code",
		SecretValue:  "123456",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	httpRequest := httptest.NewRequest(http.MethodPost, "https://sitebrush.com/?service_mail_relay", nil)
	status, statusCode := application.handleServiceMailRelayRequest(context.Background(), httpRequest, request, "203.0.113.10")
	if statusCode != http.StatusForbidden {
		t.Fatalf("status = %d %q, want forbidden", statusCode, status)
	}
	if !strings.Contains(status, "not registered") {
		t.Fatalf("status = %q, want registration error", status)
	}
}

func TestServiceMailRelayAllowsLoginCodeForVerifiedRecipient(t *testing.T) {
	application, _ := newTestApplication(t)
	request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      1,
		SourceDomain: "customer.example",
		Recipient:    "owner@example.net",
		CodeKind:     "login_code",
		SecretValue:  "123456",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: controlDatabase}
	if err := store.UpsertServiceMailRecipient(context.Background(), request.InstallationID, request.Recipient, "verified", "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertServiceMailInstallation(context.Background(), request.InstallationID, request.PublicKey, "203.0.113.10", request.SourceDomain); err != nil {
		t.Fatal(err)
	}
	if err := controlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	var sentMessage mailout.Message
	application.sendEmail = func(ctx context.Context, message mailout.Message) error {
		sentMessage = message
		return nil
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "https://sitebrush.com/?service_mail_relay", nil)
	status, statusCode := application.handleServiceMailRelayRequest(context.Background(), httpRequest, request, "203.0.113.10")
	if statusCode != http.StatusOK {
		t.Fatalf("status = %d %q, want ok", statusCode, status)
	}
	if sentMessage.To != "owner@example.net" {
		t.Fatalf("to = %q", sentMessage.To)
	}
	if sentMessage.From != "SiteBrush <sitebrush@sitebrush.com>" {
		t.Fatalf("from = %q", sentMessage.From)
	}
}

func TestServiceMailRelayAllowsPasswordChangeCodeForCurrentAdminBootstrap(t *testing.T) {
	application, _ := newTestApplication(t)
	request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      1,
		SourceDomain: "customer.example",
		Recipient:    "owner@example.net",
		CodeKind:     "password_change_code",
		SecretValue:  "123456",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	registerServiceMailInstallationForTest(t, application, request)
	var sentMessage mailout.Message
	application.sendEmail = func(ctx context.Context, message mailout.Message) error {
		sentMessage = message
		return nil
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "https://sitebrush.com/?service_mail_relay", nil)
	status, statusCode := application.handleServiceMailRelayRequest(context.Background(), httpRequest, request, "203.0.113.10")
	if statusCode != http.StatusOK {
		t.Fatalf("status = %d %q, want ok", statusCode, status)
	}
	if sentMessage.To != "owner@example.net" {
		t.Fatalf("to = %q", sentMessage.To)
	}
}

func TestServiceMailEncryptedRelayRequestRoundTrip(t *testing.T) {
	relayPrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SITEBRUSH_SERVICE_MAIL_RELAY_PUBLIC_KEY_SITEBRUSH_COM", base64.StdEncoding.EncodeToString(relayPrivateKey.PublicKey().Bytes()))
	t.Setenv("SITEBRUSH_SERVICE_MAIL_RELAY_PRIVATE_KEY_SITEBRUSH_COM", base64.StdEncoding.EncodeToString(relayPrivateKey.Bytes()))
	request := serviceMailRequest{
		Version:        1,
		InstallationID: "installation-1",
		PublicKey:      base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, ed25519.PublicKeySize)),
		SourceDomain:   "customer.example",
		Recipient:      "owner@example.net",
		CodeKind:       "login_code",
		SecretValue:    "123456",
		LanguageCode:   "en",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		Signature:      base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, ed25519.SignatureSize)),
	}
	plainPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	encryptedPayload, err := serviceMailEncryptPayloadForRelay("https://sitebrush.com/?service_mail_relay", plainPayload)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encryptedPayload, []byte("123456")) {
		t.Fatalf("encrypted payload contains secret: %s", string(encryptedPayload))
	}
	decodedRequest, err := serviceMailDecodeRelayRequest(encryptedPayload, "sitebrush.com")
	if err != nil {
		t.Fatal(err)
	}
	if decodedRequest.SecretValue != request.SecretValue || decodedRequest.Recipient != request.Recipient {
		t.Fatalf("decoded request = %+v", decodedRequest)
	}
}

func TestServiceMailNetChanV2UsesDurableIdempotentCentralOutbox(t *testing.T) {
	application, _ := newTestApplication(t)
	sentMessages := make(chan mailout.Message, 2)
	application.sendEmail = func(ctx context.Context, message mailout.Message) error {
		sentMessages <- message
		return nil
	}
	request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      2,
		MessageID:    "stable-v2-message",
		SourceDomain: "customer.example",
		Recipient:    "customer@example.net",
		CodeKind:     "invoice",
		Subject:      "Invoice 42",
		Body:         "Plain invoice",
		HTMLBody:     "<strong>Invoice 42</strong>",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	registerServiceMailInstallationForTest(t, application, request)
	stop := make(chan struct{})
	application.durableMailTasks = application.startDurableMailProcess(stop)
	t.Cleanup(func() { close(stop) })

	status, statusCode := application.handleServiceMailRelayRequest(context.Background(), nil, request, "203.0.113.10")
	if statusCode != http.StatusOK || status != mailout.StatusSent {
		t.Fatalf("first status = %d %q", statusCode, status)
	}
	select {
	case message := <-sentMessages:
		if message.MessageID != request.MessageID || message.From != "SiteBrush <sitebrush@sitebrush.com>" || message.Subject != request.Subject || message.HTMLBody != request.HTMLBody {
			t.Fatalf("central message = %#v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("central outbox did not send the message")
	}

	status, statusCode = application.handleServiceMailRelayRequest(context.Background(), nil, request, "203.0.113.10")
	if statusCode != http.StatusOK || status != mailout.StatusSent {
		t.Fatalf("duplicate status = %d %q", statusCode, status)
	}
	select {
	case duplicate := <-sentMessages:
		t.Fatalf("duplicate request sent another message: %#v", duplicate)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestServiceMailNetChanOldServerResponseIsRetryable(t *testing.T) {
	err := serviceMailNetChanResponseError(sitebrushNetChanResponse{
		StatusCode: http.StatusBadRequest,
		Status:     "unsupported netchan request kind",
	})
	if err == nil {
		t.Fatal("old central server response was accepted")
	}
	if mailout.IsPermanentFailure(err) {
		t.Fatalf("old central server response is permanent: %v", err)
	}

	err = serviceMailNetChanResponseError(sitebrushNetChanResponse{
		StatusCode: http.StatusBadRequest,
		Status:     "mail recipient is invalid",
	})
	if !mailout.IsPermanentFailure(err) {
		t.Fatalf("invalid mail request is retryable: %v", err)
	}
}

func TestSitebrushNetChanHandlerDecryptsV2MailPayload(t *testing.T) {
	relayPrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SITEBRUSH_SERVICE_MAIL_RELAY_PUBLIC_KEY_SITEBRUSH_COM", base64.StdEncoding.EncodeToString(relayPrivateKey.PublicKey().Bytes()))
	t.Setenv("SITEBRUSH_SERVICE_MAIL_RELAY_PRIVATE_KEY_SITEBRUSH_COM", base64.StdEncoding.EncodeToString(relayPrivateKey.Bytes()))
	application, _ := newTestApplication(t)
	request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      2,
		MessageID:    "encrypted-v2-message",
		SourceDomain: "customer.example",
		Recipient:    "customer@example.net",
		CodeKind:     "backup_notice",
		Subject:      "Backup ready",
		Body:         "Download the backup",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	registerServiceMailInstallationForTest(t, application, request)
	sentMessages := make(chan mailout.Message, 1)
	application.sendEmail = func(ctx context.Context, message mailout.Message) error {
		sentMessages <- message
		return nil
	}
	plainPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	encryptedPayload, err := serviceMailEncryptPayloadForRelay("https://sitebrush.com/?service_mail_relay", plainPayload)
	if err != nil {
		t.Fatal(err)
	}
	response := application.handleSitebrushNetChanPayload(context.Background(), encryptedPayload)
	if response.StatusCode != http.StatusOK || response.Status != mailout.StatusSent {
		t.Fatalf("response = %#v", response)
	}
	select {
	case message := <-sentMessages:
		if message.Kind != request.CodeKind || message.Subject != request.Subject {
			t.Fatalf("message = %#v", message)
		}
	default:
		t.Fatal("encrypted NetChan payload did not reach the mail sender")
	}
}

func TestSystemMailRouteUsesLocalSMTPOnlyAfterAddressAndSPFAreReady(t *testing.T) {
	application, _ := newTestApplication(t)
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	if err := (hostingandsupport.Store{DB: controlDatabase}).SetOwner(context.Background(), "mail.example", "owner@mail.example"); err != nil {
		t.Fatal(err)
	}
	if err := controlDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	previousTXTLookup := lookupTXTRecords
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	t.Cleanup(func() {
		lookupTXTRecords = previousTXTLookup
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	})
	lookupServerExternalIP = func(context.Context) (string, error) { return "203.0.113.10", nil }
	lookupServerInterfaceIPs = func() ([]net.IP, error) { return nil, nil }
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain != "mail.example" {
			t.Fatalf("IP lookup domain = %q", domain)
		}
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	lookupTXTRecords = func(domain string) ([]string, error) {
		return []string{"v=spf1 ip4:203.0.113.10 ~all"}, nil
	}

	localRoute := application.inspectSystemMailRoute()
	if localRoute.route != mailout.RouteLocal || localRoute.domain != "mail.example" || localRoute.from != "SiteBrush <sitebrush@mail.example>" {
		t.Fatalf("local route = %#v", localRoute)
	}
	lookupTXTRecords = func(domain string) ([]string, error) {
		return []string{"v=spf1 ip4:198.51.100.20 ~all"}, nil
	}
	relayRoute := application.inspectSystemMailRoute()
	if relayRoute.route != mailout.RouteRelay || relayRoute.from != "SiteBrush <sitebrush@sitebrush.com>" {
		t.Fatalf("relay route = %#v", relayRoute)
	}
}

func TestServiceMailRelayRegistrationStoresAttachedHostingSnapshot(t *testing.T) {
	application, _ := newTestApplication(t)
	request := serviceMailRequest{
		Version:      1,
		CodeKind:     "installation_register",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		HostingSnapshot: &hostingandsupport.HostingSnapshot{
			Version:      1,
			OwnerEmail:   "owner@example.com",
			ServerDomain: "host.example.com",
			Sites: []hostingandsupport.HostingSnapshotSite{{
				Domain:         "client.example.com",
				OwnerEmail:     "client@example.com",
				UsedBytes:      12,
				LimitBytes:     10,
				PlanName:       "Pro",
				PlanStatus:     "paid",
				PlanPaidStatus: "paid",
				AdminEmails:    []string{"client@example.com"},
			}},
			Roles: []hostingandsupport.HostingSnapshotRole{{
				Email: "owner@example.com",
				Role:  "superadmin",
				Scope: "installation",
			}},
			Plans: []hostingandsupport.HostingSnapshotPlan{{
				Name:       "Pro",
				QuotaBytes: 10,
				PaidStatus: "paid",
			}},
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
	if err := application.signServiceMailRequest(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "https://sitebrush.com/?service_mail_relay", nil)
	status, statusCode := application.handleServiceMailRelayRequest(context.Background(), httpRequest, request, "203.0.113.10")
	if statusCode != http.StatusOK {
		t.Fatalf("status = %d %q, want ok", statusCode, status)
	}
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDatabase.Close()
	hostings := (hostingandsupport.Store{DB: controlDatabase}).ClientHostings(context.Background())
	if len(hostings) != 1 {
		t.Fatalf("hostings = %d, want 1", len(hostings))
	}
	if hostings[0].InstallationID != request.InstallationID || hostings[0].ServerIP != "203.0.113.10" {
		t.Fatalf("hosting identity was not stored from relay snapshot: %#v", hostings[0])
	}
	if len(hostings[0].Sites) != 1 || !hostings[0].Sites[0].OverLimit || hostings[0].Sites[0].PlanPaidStatus != "paid" {
		t.Fatalf("hosting snapshot site was not stored: %#v", hostings[0].Sites)
	}
}

func TestSitebrushNetChanRegistrationStoresInstalledServer(t *testing.T) {
	application, _ := newTestApplication(t)
	request := serviceMailRequest{
		Version:      1,
		CodeKind:     "installation_register",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		HostingSnapshot: &hostingandsupport.HostingSnapshot{
			Version:      4,
			ServerIP:     "203.0.113.25",
			ServerDomain: "installed.example.com",
			CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		},
	}
	if err := application.signServiceMailRequest(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	response := application.handleSitebrushNetChanPayload(context.Background(), payload)
	if response.StatusCode != http.StatusOK || response.Status != "registered" {
		t.Fatalf("response = %#v, want registered", response)
	}

	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDatabase.Close()
	hostings := (hostingandsupport.Store{DB: controlDatabase}).ClientHostings(context.Background())
	if len(hostings) != 1 {
		t.Fatalf("hostings = %d, want 1", len(hostings))
	}
	if hostings[0].InstallationID != request.InstallationID || hostings[0].ServerIP != request.HostingSnapshot.ServerIP {
		t.Fatalf("installed server was not stored from netchan registration: %#v", hostings[0])
	}
}

func TestHostingSnapshotEndpointRejectsUnknownInstallation(t *testing.T) {
	application, _ := newTestApplication(t)
	request := serviceMailRequest{
		Version:      1,
		CodeKind:     "hosting_snapshot",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		HostingSnapshot: &hostingandsupport.HostingSnapshot{
			Version:  1,
			OSName:   "linux",
			CPUModel: "amd64",
		},
	}
	if err := application.signServiceMailRequest(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	status, statusCode := application.handleHostingSnapshotRequest(context.Background(), request, "203.0.113.10")
	if statusCode != http.StatusForbidden || !strings.Contains(status, "installation is not registered") {
		t.Fatalf("status = %d %q, want unregistered forbidden", statusCode, status)
	}
}

func TestHostingSnapshotEndpointAcceptsRegisteredSignedInstallation(t *testing.T) {
	application, _ := newTestApplication(t)
	request := serviceMailRequest{
		Version:      1,
		CodeKind:     "hosting_snapshot",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		HostingSnapshot: &hostingandsupport.HostingSnapshot{
			Version:      1,
			OwnerEmail:   "owner@example.com",
			ServerDomain: "host.example.com",
			OSName:       "linux",
			CPUModel:     "amd64",
			CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		},
	}
	if err := application.signServiceMailRequest(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: controlDatabase}
	if err := store.UpsertServiceMailInstallation(context.Background(), request.InstallationID, request.PublicKey, "203.0.113.10", "host.example.com"); err != nil {
		t.Fatal(err)
	}
	_ = controlDatabase.Close()

	status, statusCode := application.handleHostingSnapshotRequest(context.Background(), request, "203.0.113.10")
	if statusCode != http.StatusOK || status != "stored" {
		t.Fatalf("status = %d %q, want stored", statusCode, status)
	}
}

func TestHostingSnapshotServerStatusLabels(t *testing.T) {
	statusWithIP := hostingSnapshotServerStatus("203.0.113.10")
	if statusWithIP != "IP 203.0.113.10" {
		t.Fatalf("statusWithIP = %q", statusWithIP)
	}
	statusWithoutIP := hostingSnapshotServerStatus("")
	if strings.Contains(statusWithoutIP, "локальная разработка") {
		t.Fatalf("statusWithoutIP contains forbidden phrase: %q", statusWithoutIP)
	}
	if statusWithoutIP != "локальная инсталляция" {
		t.Fatalf("statusWithoutIP = %q", statusWithoutIP)
	}
}

func TestHostingAndSupportTemplateRendersServerView(t *testing.T) {
	templateBytes, err := fs.ReadFile(embeddedWebFiles, "web/expenses.html")
	if err != nil {
		t.Fatal(err)
	}
	parsedTemplate, err := template.New("expenses.html").Parse(string(templateBytes))
	if err != nil {
		t.Fatal(err)
	}
	view := map[string]any{
		"Domain":         "sitebrush.com",
		"Title":          "Хостинг и поддержка",
		"T":              translationsForLanguageCode("ru"),
		"CompileVersion": CompileVersion,
		"Overview":       hostingandsupport.BuildOverview(nil, 0, nil, nil, nil, nil, nil),
		"Servers": []hostingandsupport.ServerView{{
			Name:               "sitebrush.com",
			Subtitle:           "локальный сервер SiteBrush",
			Local:              true,
			SiteCount:          1,
			ClientCount:        1,
			InvoiceActionLabel: "Выставить счёт",
			InvoiceActionClass: "btn-success",
			SyncStatusLabel:    "локальные данные",
			SyncStatusClass:    "billing-sync-ok",
			NetworkStatusLabel: "ok",
			NetworkStatusClass: "hosting-metric-ok",
			SystemMetrics: []hostingandsupport.ServerMetricView{{
				Name:            "CPU",
				Value:           "97.0%",
				Detail:          "загрузка сейчас",
				StatusClass:     "hosting-metric-danger",
				Percent:         97,
				HasPercent:      true,
				HasProcessModal: true,
				Processes: []hostingandsupport.HostingSnapshotProcess{
					{Name: "sitebrush", PID: 100, CPUPercent: 97},
					{Name: "sqlite", PID: 101, CPUPercent: 12.5},
				},
			}},
			Sites: []hostingandsupport.ServerSiteView{{
				Domain:             "example.com",
				OwnerEmail:         "owner@example.com",
				PlanName:           "Pro",
				PaidStatus:         "paid",
				UsedLabel:          "1 MB",
				LimitLabel:         "10 MB",
				QuotaInput:         "10mb",
				CanEditQuota:       true,
				InvoiceLabel:       "можно выставить счёт",
				CertificateDomains: []hostingandsupport.CertificateDomainView{{Domain: "example.com", Valid: true, ExpiresAt: "2026-09-01 12:00:00 UTC", Remaining: "22 days", CanRenew: true}},
			}},
			Clients: []hostingandsupport.ServerClientView{{
				Email:     "owner@example.com",
				SiteCount: 1,
				Domains:   "example.com",
				Sites: []hostingandsupport.ServerSiteView{{
					Domain:             "example.com",
					UsedLabel:          "1 MB",
					LimitLabel:         "10 MB",
					QuotaInput:         "10mb",
					CanEditQuota:       true,
					CertificateDomains: []hostingandsupport.CertificateDomainView{{Domain: "example.com", Valid: true, ExpiresAt: "2026-09-01 12:00:00 UTC", Remaining: "22 days", CanRenew: true}},
				}},
			}},
		}},
		"Sites":                       nil,
		"Plans":                       nil,
		"PaymentProviders":            hostingandsupport.DemoPaymentProviders("http://sitebrush.com/?hosting_and_support_demo_payment&invoice={invoice}"),
		"Invoices":                    nil,
		"SiteRequests":                nil,
		"ServiceMailInstallations":    nil,
		"ServiceMailEvents":           nil,
		"ClientHostings":              nil,
		"RegistrySyncEvents":          nil,
		"SitebrushComKey":             hostingandsupport.SitebrushComKey{},
		"ShowServers":                 true,
		"ShowCentralRegistry":         true,
		"ShowDemo":                    true,
		"Clients":                     nil,
		"ClientCount":                 1,
		"ClientSiteCount":             1,
		"ClientInstallationCount":     1,
		"ClientLocalDevelopmentCount": 0,
		"ServiceMailBlocks":           nil,
		"ServiceMailRelayEnabled":     false,
		"ServiceMailLimits":           map[string]int{},
		"Backups":                     nil,
		"DemoSettings":                demo.Settings{Domain: "demo.sitebrush.com", Enabled: true},
		"AutoRegistrationEnabled":     false,
		"PublicTrialEmbedHTML":        "",
		"CurrentDomain":               "sitebrush.com",
	}
	var rendered bytes.Buffer
	if err := parsedTemplate.Execute(&rendered, view); err != nil {
		t.Fatal(err)
	}
	renderedHTML := rendered.String()
	if !strings.Contains(renderedHTML, "Серверы") || !strings.Contains(renderedHTML, "sitebrush.com") || !strings.Contains(renderedHTML, "Выставить счёт") {
		t.Fatal("server-first hosting view did not render expected content")
	}
	if !strings.Contains(renderedHTML, "Top 5 CPU процессов") || !strings.Contains(renderedHTML, "sitebrush") {
		t.Fatal("high CPU process modal did not render")
	}
	if strings.Contains(renderedHTML, "<strong>Процесс</strong>") {
		t.Fatal("process metric rendered as a permanent server card")
	}
	if !strings.Contains(renderedHTML, `data-hosting-quota-edit="hostingQuotaEditor0_0_0"`) || !strings.Contains(renderedHTML, "Изменение квоты") || !strings.Contains(renderedHTML, "Применить") {
		t.Fatal("editable server quota confirmation did not render")
	}
	if !strings.Contains(renderedHTML, `name="billing_action" value="update_site_quota"`) {
		t.Fatal("server quota form does not use the isolated quota action")
	}
	if !strings.Contains(renderedHTML, `data-certificate-renew="example.com"`) || !strings.Contains(renderedHTML, "2026-09-01 12:00:00 UTC") {
		t.Fatal("server domain certificate status and renewal control did not render")
	}
	if !strings.Contains(renderedHTML, `class="hosting-installation-tabs"`) ||
		!strings.Contains(renderedHTML, `data-hosting-installation-tab="demo"`) ||
		!strings.Contains(renderedHTML, `data-hosting-installation-panel="demo"`) {
		t.Fatal("installation switcher does not render the styled demo tab")
	}
	if strings.Count(renderedHTML, `class="billing-demo-settings-form"`) != 1 {
		t.Fatal("demo settings form must have one source of truth")
	}
	if !strings.Contains(renderedHTML, "Демо-сайты не участвуют в биллинге.") ||
		!strings.Contains(renderedHTML, "Они не создают клиентов, счетов, оплат и покрытия расходов хостинга.") {
		t.Fatal("demo tab does not explain its billing exclusion")
	}
}

func TestExpensesTemplateShowsOnlyLocalServerAndEnabledDemoOutsideSitebrushCom(t *testing.T) {
	templateBytes, err := fs.ReadFile(embeddedWebFiles, "web/expenses.html")
	if err != nil {
		t.Fatal(err)
	}
	parsedTemplate, err := template.New("expenses.html").Parse(string(templateBytes))
	if err != nil {
		t.Fatal(err)
	}
	application := &App{}
	request := httptest.NewRequest(http.MethodGet, "https://owner.example/?expenses", nil)
	baseSnapshot := hostingAndSupportPanelSnapshot{
		Version:    hostingAndSupportPanelSnapshotVersion,
		MainDomain: "owner.example",
		Sites:      []hostingandsupport.Site{{Domain: "owner.example"}},
		Servers: []hostingandsupport.ServerView{{
			Name: "owner.example", Local: true, Subtitle: "локальный сервер SiteBrush",
			OSLabel: "Linux", CPULabel: "AMD64", SyncStatusLabel: "локальные данные", NetworkStatusLabel: "доступен",
			SystemMetrics: []hostingandsupport.ServerMetricView{{Name: "CPU", Value: "12%", Detail: "загрузка сейчас"}},
			Sites:         []hostingandsupport.ServerSiteView{{Domain: "owner.example"}},
		}},
		Overview: hostingandsupport.BuildOverview([]hostingandsupport.Site{{Domain: "owner.example"}}, 0, nil, nil, nil, nil, nil),
		DemoSettings: demo.Settings{
			Domain:  "demo.owner.example",
			Enabled: false,
		},
	}
	renderSnapshot := func(snapshot hostingAndSupportPanelSnapshot) string {
		t.Helper()
		view := application.hostingAndSupportPanelView(request, snapshot)
		view["Domain"] = snapshot.MainDomain
		view["CompileVersion"] = CompileVersion
		var rendered bytes.Buffer
		if executeErr := parsedTemplate.Execute(&rendered, view); executeErr != nil {
			t.Fatal(executeErr)
		}
		return rendered.String()
	}

	disabledDemoHTML := renderSnapshot(baseSnapshot)
	if !strings.Contains(disabledDemoHTML, "Сервер и расходы") {
		t.Fatal("regular server did not render the singular expenses title")
	}
	for _, forbiddenFragment := range []string{
		`data-hosting-installation-tab="desktop"`,
		`data-hosting-installation-panel="desktop"`,
		`data-hosting-installation-tab="archive"`,
		`data-hosting-installation-panel="archive"`,
		"Ключ sitebrush.com",
	} {
		if strings.Contains(disabledDemoHTML, forbiddenFragment) {
			t.Fatalf("regular server rendered forbidden central or demo fragment %q", forbiddenFragment)
		}
	}
	if !strings.Contains(disabledDemoHTML, "owner.example") || !strings.Contains(disabledDemoHTML, `data-expense-server-panel="0"`) {
		t.Fatal("regular server did not render its local server")
	}
	if !strings.Contains(disabledDemoHTML, `name="monthly_server_expense"`) {
		t.Fatal("simplified expenses did not render the monthly server price")
	}
	for _, settingsFragment := range []string{
		`data-hosting-installation-tab="settings"`,
		`data-site-settings-tab="general"`,
		`data-site-settings-tab="demo"`,
		`data-site-settings-panel="demo"`,
		`name="public_trial_enabled"`,
		`name="demo_site_enabled"`,
	} {
		if !strings.Contains(disabledDemoHTML, settingsFragment) {
			t.Fatalf("disabled demo settings missing recoverable fragment %q", settingsFragment)
		}
	}
	for _, removedFragment := range []string{
		`name="disk_rate_per_100_gb"`,
		`name="expense_mode"`,
		`data-hosting-tab-panel="settings"`,
		`data-hosting-tab-panel="diagnostics"`,
		`id="hostingServerList"`,
		"Ключ sitebrush.com",
	} {
		if strings.Contains(disabledDemoHTML, removedFragment) {
			t.Fatalf("simplified expenses rendered technical fragment %q", removedFragment)
		}
	}
	for _, monitoringFragment := range []string{"локальный сервер SiteBrush", "Linux", "AMD64", "CPU", "12%", "доступен"} {
		if !strings.Contains(disabledDemoHTML, monitoringFragment) {
			t.Fatalf("simplified expenses missing server monitoring fragment %q", monitoringFragment)
		}
	}

	enabledDemoSnapshot := baseSnapshot
	enabledDemoSnapshot.DemoSettings.Enabled = true
	enabledDemoHTML := renderSnapshot(enabledDemoSnapshot)
	if !strings.Contains(enabledDemoHTML, `data-site-settings-tab="demo"`) ||
		!strings.Contains(enabledDemoHTML, `data-site-settings-panel="demo"`) {
		t.Fatal("enabled local demo did not render its settings tab and panel")
	}
	if strings.Contains(enabledDemoHTML, `data-hosting-installation-tab="desktop"`) ||
		strings.Contains(enabledDemoHTML, `data-hosting-installation-tab="archive"`) {
		t.Fatal("enabled demo exposed central desktop or archive tabs")
	}

	centralSnapshot := baseSnapshot
	centralSnapshot.ShowCentralRegistry = true
	centralSnapshot.DesktopHostingGroups = []hostingandsupport.DesktopHostingGroup{{
		ServerIP: "203.0.113.10",
		Hostings: []hostingandsupport.ClientHosting{{
			InstallationID: "desktop-1", ServerIP: "203.0.113.10",
			Sites: []hostingandsupport.ClientHostingSite{{Domain: "desktop.example.com"}},
		}},
	}}
	centralSnapshot.ArchivedHostings = []hostingandsupport.ClientHosting{{InstallationID: "archive-1", LastSeenAt: "2026-07-20"}}
	centralHTML := renderSnapshot(centralSnapshot)
	for _, expectedFragment := range []string{
		`data-hosting-installation-tab="desktop"`,
		`data-hosting-installation-tab="archive"`,
		"desktop.example.com",
		"archive-1",
	} {
		if !strings.Contains(centralHTML, expectedFragment) {
			t.Fatalf("central expenses missing %q", expectedFragment)
		}
	}
}

func TestExpensesTitlesUseServerScopeInEveryLanguage(t *testing.T) {
	for languageCode, translations := range translationCatalog {
		for _, translationKey := range []string{"expenses_title", "expenses_central_title", "menu_expenses", "menu_expenses_central"} {
			if strings.TrimSpace(translations[translationKey]) == "" {
				t.Fatalf("language %s is missing %s", languageCode, translationKey)
			}
		}
	}

	application := &App{}
	for _, testCase := range []struct {
		name       string
		language   string
		mainDomain string
		expected   string
	}{
		{name: "regular Russian server", language: "ru", mainDomain: "owner.example", expected: "Сервер и расходы"},
		{name: "central Russian server", language: "ru", mainDomain: "sitebrush.com", expected: "Серверы и расходы"},
		{name: "regular English server", language: "en", mainDomain: "owner.example", expected: "Server and expenses"},
		{name: "central English server", language: "en", mainDomain: "sitebrush.com", expected: "Servers and expenses"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://"+testCase.mainDomain+"/?expenses", nil)
			request.Header.Set("Accept-Language", testCase.language)
			view := application.hostingAndSupportPanelView(request, hostingAndSupportPanelSnapshot{MainDomain: testCase.mainDomain})
			if title := view["Title"]; title != testCase.expected {
				t.Fatalf("title = %q, want %q", title, testCase.expected)
			}
		})
	}
}

func TestGuestMenuInvitesEditingInEveryLanguage(t *testing.T) {
	expectedLabels := map[string]string{
		"de": "Zum Bearbeiten anmelden",
		"en": "Sign in to edit",
		"es": "Inicia sesión para editar",
		"fa": "ورود برای ویرایش",
		"fi": "Kirjaudu muokkaamaan",
		"fr": "Se connecter pour modifier",
		"he": "התחברות לעריכה",
		"it": "Accedi per modificare",
		"ja": "ログインして編集",
		"kk": "Өңдеу үшін кіру",
		"mn": "Засварлахын тулд нэвтрэх",
		"pt": "Entrar para editar",
		"ru": "Войти, чтобы редактировать",
		"sv": "Logga in för att redigera",
		"tr": "Düzenlemek için giriş yap",
		"zh": "登录以编辑",
	}
	if len(translationCatalog) != len(expectedLabels) {
		t.Fatalf("translation languages = %d, expected labels = %d", len(translationCatalog), len(expectedLabels))
	}
	for languageCode, expectedLabel := range expectedLabels {
		translations := translationCatalog[languageCode]
		if translations["menu_login"] != expectedLabel {
			t.Fatalf("language %s menu_login = %q, want %q", languageCode, translations["menu_login"], expectedLabel)
		}
		menuScript := buildGuestContextMenuScriptForLanguage("/page", "example.com", languageCode)
		if !strings.Contains(menuScript, expectedLabel) {
			t.Fatalf("language %s guest menu does not contain %q", languageCode, expectedLabel)
		}
	}
}

func TestSimplifiedExpenseServerViewsIncludeOwnerAndClientDetails(t *testing.T) {
	server := hostingandsupport.ServerView{
		ID:              "local",
		Local:           true,
		BillingCurrency: "EUR",
		Clients: []hostingandsupport.ServerClientView{
			{
				Email: "client@example.com",
				Sites: []hostingandsupport.ServerSiteView{
					{Domain: "paid.example.com", UsedBytes: 30 * expenses.DecimalGigabyte, BillingAmount: "15.00", BillingPriceLabel: "€15.00/мес"},
					{Domain: "bonus.example.com", UsedBytes: 100 * expenses.DecimalMegabyte, BillingAmount: "0.00", BillingPriceLabel: "€0.00"},
				},
			},
			{
				Email: "owner@example.com",
				Sites: []hostingandsupport.ServerSiteView{{Domain: "owner.example.com", UsedBytes: 10 * expenses.DecimalGigabyte, BillingAmount: "5.00"}},
			},
		},
	}
	clientDetails := []hostingAndSupportClientView{
		{PrimaryEmail: "client@example.com", Emails: []string{"client@example.com"}, InvoiceDay: 5},
		{PrimaryEmail: "owner@example.com", Emails: []string{"owner@example.com"}, InvoiceDay: 7},
	}
	views := simplifiedExpenseServerViews([]hostingandsupport.ServerView{server}, clientDetails, nil)
	if len(views) != 1 || len(views[0].Clients) != 2 {
		t.Fatalf("simplified servers = %#v", views)
	}
	client := views[0].Clients[0]
	if client.Email != "client@example.com" || client.SiteCount != 2 || client.TotalStorageLabel != "28.0 GB" || client.MonthlyShareLabel != "15.00 EUR" {
		t.Fatalf("simplified client = %#v", client)
	}
	owner := views[0].Clients[1]
	if owner.Email != "owner@example.com" || owner.SiteCount != 1 || owner.MonthlyShareLabel != "5.00 EUR" {
		t.Fatalf("simplified owner = %#v", owner)
	}
}

func TestVisibleHostingAndSupportLocalSitesHidesDisabledDemo(t *testing.T) {
	sites := []hostingandsupport.Site{
		{Domain: "owner.example"},
		{Domain: "demo.owner.example", IsDemo: true},
	}
	disabledSites := visibleHostingAndSupportLocalSites(sites, demo.Settings{Domain: "demo.owner.example"})
	if len(disabledSites) != 1 || disabledSites[0].Domain != "owner.example" {
		t.Fatalf("disabled demo sites = %#v", disabledSites)
	}
	enabledSites := visibleHostingAndSupportLocalSites(sites, demo.Settings{Domain: "demo.owner.example", Enabled: true})
	if len(enabledSites) != 2 || !enabledSites[1].IsDemo {
		t.Fatalf("enabled demo sites = %#v", enabledSites)
	}
}

func TestHostingAndSupportViewUsesPreparedSnapshot(t *testing.T) {
	requests := make(chan hostingAndSupportPanelRequest, 4)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	wantSnapshot := hostingAndSupportPanelSnapshot{
		Version:     hostingAndSupportPanelSnapshotVersion,
		BuiltAt:     "2026-07-22T12:00:00Z",
		MainDomain:  "sitebrush.com",
		OwnerEmails: []string{"owner@sitebrush.com"},
		Sites:       []hostingandsupport.Site{{Domain: "sitebrush.com"}},
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case request := <-requests:
				if request.kind == "get" {
					request.reply <- hostingAndSupportPanelResponse{snapshot: wantSnapshot}
				}
			}
		}
	}()
	application := &App{hostingAndSupportPanel: requests}
	request := httptest.NewRequest(http.MethodGet, "https://sitebrush.com/?hosting_and_support", nil)
	view, err := application.expensesView(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	sites, ok := view["Sites"].([]hostingandsupport.Site)
	if !ok || len(sites) != 1 || sites[0].Domain != "sitebrush.com" {
		t.Fatalf("prepared sites = %#v", view["Sites"])
	}
	if view["PanelSnapshotBuiltAt"] != wantSnapshot.BuiltAt {
		t.Fatalf("built at = %#v, want %q", view["PanelSnapshotBuiltAt"], wantSnapshot.BuiltAt)
	}
}

func TestApplyHostingAndSupportPanelQuotaChangeUpdatesServerView(t *testing.T) {
	application := &App{}
	snapshot := hostingAndSupportPanelSnapshot{
		Version:    hostingAndSupportPanelSnapshotVersion,
		MainDomain: "sitebrush.com",
		Sites: []hostingandsupport.Site{{
			Domain:      "sitebrush.com",
			UsedBytes:   24 * 1024 * 1024 * 1024,
			UsedLabel:   "24.0 GB",
			LimitLabel:  "20.0 GB",
			QuotaInput:  "20gb",
			UsedPercent: 100,
		}},
	}

	updatedSnapshot := application.applyHostingAndSupportPanelQuotaChange(snapshot, siteQuotaRow{
		Domain:     "sitebrush.com",
		UsedBytes:  24 * 1024 * 1024 * 1024,
		LimitBytes: 30 * 1024 * 1024 * 1024,
	})

	if len(updatedSnapshot.Sites) != 1 || updatedSnapshot.Sites[0].QuotaInput != "30gb" || updatedSnapshot.Sites[0].LimitLabel != "30.0 GB" || updatedSnapshot.Sites[0].UsedPercent != 80 {
		t.Fatalf("updated site quota = %#v", updatedSnapshot.Sites)
	}
	if len(updatedSnapshot.Servers) != 1 || len(updatedSnapshot.Servers[0].Sites) != 1 || updatedSnapshot.Servers[0].Sites[0].QuotaInput != "30gb" || updatedSnapshot.Servers[0].Sites[0].LimitLabel != "30.0 GB" {
		t.Fatalf("updated server quota = %#v", updatedSnapshot.Servers)
	}
}

func TestRenderTemplateProcessUsesPreparedTemplate(t *testing.T) {
	stop := make(chan struct{})
	requests := startRenderTemplateProcess(stop)
	t.Cleanup(func() { close(stop) })
	reply := make(chan renderTemplateResponse, 1)
	requests <- renderTemplateRequest{
		name: "login.html",
		envelope: map[string]any{
			"Domain":         "sitebrush.com",
			"T":              translationsForLanguageCode("ru"),
			"CompileVersion": CompileVersion,
			"ShowForm":       true,
		},
		reply: reply,
	}
	response := <-reply
	if response.err != nil {
		t.Fatal(response.err)
	}
	if !bytes.Contains(response.html, []byte("<html")) {
		t.Fatalf("rendered template does not contain html: %q", response.html)
	}
}

func TestMobileServicePageDesignCoversEverySharedTemplate(t *testing.T) {
	expectedTemplates := []string{
		"analytics.html",
		"billing_invoice.html",
		"billing_schedule.html",
		"domain_settings.html",
		"edit_mode.html",
		"expenses.html",
		"files.html",
		"import.html",
		"login.html",
		"missing.html",
		"profile.html",
		"recover.html",
		"revisions.html",
		"setup.html",
	}
	coveredTemplates := make([]string, 0, len(expectedTemplates))
	templatePaths, err := fs.Glob(embeddedWebFiles, "web/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, templatePath := range templatePaths {
		templateBytes, readErr := fs.ReadFile(embeddedWebFiles, templatePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		templateText := string(templateBytes)
		if !strings.Contains(templateText, "technical_pages.css") {
			continue
		}
		if _, parseErr := template.New(filepath.Base(templatePath)).Parse(templateText); parseErr != nil {
			t.Errorf("parse %s: %v", templatePath, parseErr)
		}
		coveredTemplates = append(coveredTemplates, filepath.Base(templatePath))
		for _, requiredFragment := range []string{`name="viewport"`, `lang="{{.LanguageCode}}"`, `dir="{{.TextDirection}}"`} {
			if !strings.Contains(templateText, requiredFragment) {
				t.Errorf("%s does not contain %q", templatePath, requiredFragment)
			}
		}
	}
	slices.Sort(coveredTemplates)
	slices.Sort(expectedTemplates)
	if !slices.Equal(coveredTemplates, expectedTemplates) {
		t.Fatalf("shared service templates = %v, want %v", coveredTemplates, expectedTemplates)
	}

	technicalStyleBytes, err := fs.ReadFile(embeddedWebFiles, "web/static/technical_pages.css")
	if err != nil {
		t.Fatal(err)
	}
	directoryStyleBytes, err := fs.ReadFile(embeddedWebFiles, "web/static/directory_listing.css")
	if err != nil {
		t.Fatal(err)
	}
	combinedStyles := string(technicalStyleBytes) + string(directoryStyleBytes)
	for _, requiredFragment := range []string{
		"@media (max-width: 760px) and (prefers-color-scheme: dark)",
		"background: #1e362d !important",
		"background: #9eb6e3",
		"color: #12151d",
		".technical-page .alert-warning",
		".technical-page .alert-danger",
	} {
		if !strings.Contains(combinedStyles, requiredFragment) {
			t.Errorf("mobile service design does not contain %q", requiredFragment)
		}
	}
}

func TestHostingAndSupportClientHostingViewMarksStaleSync(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	view := buildHostingAndSupportClientHostingView(hostingandsupport.ClientHosting{
		InstallationID: "installation-1",
		ServerIP:       "203.0.113.10",
		LastSeenAt:     now.Add(-8 * 24 * time.Hour).Format(time.RFC3339),
	}, map[string]struct{}{}, now)
	if !view.Stale || view.SyncStatusLabel != "устарело" {
		t.Fatalf("sync status = stale:%v label:%q", view.Stale, view.SyncStatusLabel)
	}
	if view.ServerStatusLabel != "IP 203.0.113.10" {
		t.Fatalf("server status label = %q", view.ServerStatusLabel)
	}
}

func TestHostingAndSupportClientHostingViewKeepsOnlyClientRoles(t *testing.T) {
	view := buildHostingAndSupportClientHostingView(hostingandsupport.ClientHosting{
		InstallationID: "installation-1",
		ServerStatus:   "локальная инсталляция",
		LastSeenAt:     time.Now().UTC().Format(time.RFC3339),
		Roles: []hostingandsupport.ClientHostingRole{
			{Email: "owner@example.com", Role: "superadmin", Scope: "installation"},
			{Email: "site@example.com", Role: "site_admin", Scope: "site", Domain: "client.example.com"},
		},
	}, map[string]struct{}{"site@example.com": {}}, time.Now().UTC())
	if view.Stale {
		t.Fatal("fresh sync was marked stale")
	}
	if view.ServerStatusLabel != "локальная инсталляция" {
		t.Fatalf("server status label = %q", view.ServerStatusLabel)
	}
	if len(view.ClientRoles) != 1 || view.ClientRoles[0].Email != "site@example.com" || view.ClientRoles[0].Role != "site_admin" {
		t.Fatalf("client roles = %#v", view.ClientRoles)
	}
}

func TestHostingAndSupportRealClientHostingsExcludeLocalAndUnroutedServers(t *testing.T) {
	hostings := hostingandsupport.RealClientHostings([]hostingandsupport.ClientHosting{
		{InstallationID: "local", ServerIP: "127.0.0.1", ServerDomain: "localhost"},
		{InstallationID: "private", ServerIP: "192.168.1.10", ServerDomain: "sitebrush.local"},
		{InstallationID: "no-domain", ServerIP: "203.0.113.10"},
		{InstallationID: "domain-is-ip", ServerIP: "203.0.113.10", ServerDomain: "203.0.113.10"},
	}, nil)
	if len(hostings) != 0 {
		t.Fatalf("real hostings = %#v, want none", hostings)
	}
}

func TestHostingAndSupportCentralServersRequireSitebrushComDNSAndKey(t *testing.T) {
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	t.Cleanup(func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
	})
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "sitebrush.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, errors.New("unexpected domain")
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := hostingandsupport.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: database}
	if err := store.SetOwner(context.Background(), "sitebrush.com", "owner@sitebrush.com"); err != nil {
		t.Fatal(err)
	}
	application := &App{}
	if !application.hostingAndSupportCanShowCentralServers(context.Background(), "sitebrush.com", hostingandsupport.SitebrushComKey{PublicKey: sitebrushComServiceMailRelayPublicKey}) {
		t.Fatal("sitebrush.com with matching DNS and public key was not accepted")
	}
	if application.hostingAndSupportCanShowCentralServers(context.Background(), "sitebrush.com", hostingandsupport.SitebrushComKey{PublicKey: "wrong"}) {
		t.Fatal("sitebrush.com with wrong public key was accepted")
	}
}

func TestHostingSnapshotDiskThresholdSendsSingleOwnerEmail(t *testing.T) {
	storagePath := t.TempDir()
	databasePath := filepath.Join(storagePath, "hostingandsupport.db")
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := hostingandsupport.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var messages []mailout.Message
	application := &App{
		storagePath: storagePath,
		dbPath:      databasePath,
		sendEmail: func(ctx context.Context, message mailout.Message) error {
			messages = append(messages, message)
			return nil
		},
	}
	snapshot := hostingandsupport.HostingSnapshot{
		InstallationID: "installation-1",
		OwnerEmail:     "owner@example.com",
		ServerDomain:   "sitebrush.ru",
		ServerIP:       "203.0.113.10",
		DiskFreeBytes:  4,
		DiskTotalBytes: 100,
	}
	application.notifyHostingSnapshotDiskThreshold(context.Background(), snapshot)
	application.notifyHostingSnapshotDiskThreshold(context.Background(), snapshot)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].To != "owner@example.com" || !strings.Contains(messages[0].Body, "занято 96%") {
		t.Fatalf("unexpected alert message: %#v", messages[0])
	}
}

func TestAutomaticBillingCreatesInvoicesForClientAndServerOwner(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := hostingandsupport.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: database}
	for _, email := range []string{"client@example.com", "free@example.com", "demo@example.com", "owner@example.com"} {
		customer, customerErr := store.EnsureBillingCustomer(context.Background(), email, nil)
		if customerErr != nil {
			t.Fatal(customerErr)
		}
		if scheduleErr := store.SaveBillingCustomerSchedule(context.Background(), customer.ID, 23, 7, "UTC"); scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
	}
	mailQueue := make(chan mailout.DeliveryJob, 4)
	application := &App{emailDelivery: mailQueue}
	snapshot := hostingAndSupportPanelSnapshot{
		MainDomain: "sitebrush.com", CommissionBPS: 500,
		OwnerEmails: []string{"owner@example.com"},
		LocalCostPolicy: expenses.ServerPolicy{
			InstallationID: "local", Mode: expenses.ModeActual, ActualMonthlyExpenseMinor: 100, DiskRatePer100GBMinor: 1500, Currency: "EUR", FreeSiteThresholdBytes: 100_000_000, DiskTotalBytes: 2_000_000_000,
		},
		Sites: []hostingandsupport.Site{
			{Domain: "paid.example.com", UsedBytes: 600_000_000, BillingAmount: "1.20", BillingCurrency: "EUR", BillingBillable: true, AdminEmails: "client@example.com"},
			{Domain: "bonus.example.com", UsedBytes: 100_000_000, BillingAmount: "0.20", BillingCurrency: "EUR", BillingBillable: false, AdminEmails: "client@example.com"},
			{Domain: "free-only.example.com", UsedBytes: 100_000_000, BillingAmount: "0.20", BillingCurrency: "EUR", BillingBillable: false, AdminEmails: "free@example.com"},
			{Domain: "demo.example.com", UsedBytes: 700_000_000, BillingAmount: "1.40", BillingCurrency: "EUR", BillingBillable: true, AdminEmails: "demo@example.com", IsDemo: true},
			{Domain: "owner.example.com", UsedBytes: 700_000_000, BillingAmount: "1.40", BillingCurrency: "EUR", BillingBillable: true, AdminEmails: "owner@example.com"},
		},
	}
	snapshotWithoutCost := snapshot
	snapshotWithoutCost.LocalCostPolicy = expenses.ServerPolicy{}
	application.runAutomaticBillingOnce(context.Background(), store, snapshotWithoutCost, time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC))
	if invoicesWithoutCost := store.Invoices(context.Background(), 10); len(invoicesWithoutCost) != 0 {
		t.Fatalf("invoices without server cost = %d, want 0", len(invoicesWithoutCost))
	}
	application.runAutomaticBillingOnce(context.Background(), store, snapshot, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	invoices := store.Invoices(context.Background(), 10)
	if len(invoices) != 2 {
		t.Fatalf("invoices = %d, want 2: %#v", len(invoices), invoices)
	}
	invoicesByEmail := make(map[string]hostingandsupport.Invoice, len(invoices))
	for _, invoice := range invoices {
		invoicesByEmail[invoice.CustomerEmail] = invoice
	}
	clientInvoice := invoicesByEmail["client@example.com"]
	if clientInvoice.InstallationID != "local:sitebrush.com" || clientInvoice.AmountMinor != 49 || clientInvoice.ServerCostMinor != 46 || clientInvoice.PaymentFeeMinor != 3 || clientInvoice.ReserveMinor != 0 || len(clientInvoice.Lines) != 3 || !clientInvoice.Lines[1].Bonus {
		t.Fatalf("client invoice = %#v", clientInvoice)
	}
	ownerInvoice := invoicesByEmail["owner@example.com"]
	if ownerInvoice.InstallationID != "local:sitebrush.com" || ownerInvoice.AmountMinor != 57 || ownerInvoice.ServerCostMinor != 54 || ownerInvoice.PaymentFeeMinor != 3 || len(ownerInvoice.Lines) != 2 {
		t.Fatalf("owner invoice = %#v", ownerInvoice)
	}
	deliveredTo := make(map[string]bool)
	for deliveryIndex := 0; deliveryIndex < 2; deliveryIndex++ {
		select {
		case delivery := <-mailQueue:
			invoice := invoicesByEmail[delivery.Message.To]
			if delivery.Message.HTMLBody == "" || !strings.Contains(delivery.Message.HTMLBody, invoice.Number) {
				t.Fatalf("invoice email = %#v", delivery.Message)
			}
			deliveredTo[delivery.Message.To] = true
		default:
			t.Fatal("invoice email was not queued")
		}
	}
	if !deliveredTo["client@example.com"] || !deliveredTo["owner@example.com"] {
		t.Fatalf("invoice recipients = %#v", deliveredTo)
	}
	application.runAutomaticBillingOnce(context.Background(), store, snapshot, time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC))
	if duplicateInvoices := store.Invoices(context.Background(), 10); len(duplicateInvoices) != 2 {
		t.Fatalf("duplicate invoices = %d, want 2", len(duplicateInvoices))
	}
}

func TestSaveServerExpensePolicyUsesOnlyMonthlyServerPrice(t *testing.T) {
	application, _ := newTestApplication(t)
	controlDatabase := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", false)
	defer controlDatabase.Close()

	form := url.Values{}
	form.Set("monthly_server_expense", "75.00")
	form.Set("currency", "EUR")
	form.Set("free_site_threshold_mb", "100")
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?expenses", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status := application.saveServerExpensePolicyFromForm(request)
	if status != translationsForLanguageCode("ru")["expenses_server_policy_saved"] {
		t.Fatalf("save status = %q", status)
	}
	policy, found := (hostingandsupport.Store{DB: controlDatabase}).ServerExpensePolicy(context.Background(), "local", 500*expenses.DecimalGigabyte)
	if !found || policy.Mode != expenses.ModeActual || policy.ActualMonthlyExpenseMinor != 7500 || expenses.CalculateMonthlyExpense(policy) != 7500 {
		t.Fatalf("saved expense policy = %+v, found=%t", policy, found)
	}
}

func TestAutomaticBillingDistributesServerCostByStorage(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := hostingandsupport.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: database}
	for _, email := range []string{"small@example.com", "large@example.com"} {
		customer, customerErr := store.EnsureBillingCustomer(context.Background(), email, nil)
		if customerErr != nil {
			t.Fatal(customerErr)
		}
		if scheduleErr := store.SaveBillingCustomerSchedule(context.Background(), customer.ID, 23, 7, "UTC"); scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
	}
	application := &App{emailDelivery: make(chan mailout.DeliveryJob, 4)}
	snapshot := hostingAndSupportPanelSnapshot{
		MainDomain: "sitebrush.com",
		LocalCostPolicy: expenses.ServerPolicy{
			InstallationID: "local", Mode: expenses.ModeActual, ActualMonthlyExpenseMinor: 101, DiskRatePer100GBMinor: 1500, Currency: "USD", FreeSiteThresholdBytes: 100_000_000, DiskTotalBytes: 5_000_000_000,
		},
		Sites: []hostingandsupport.Site{
			{Domain: "small.example.com", UsedBytes: 600_000_000, AdminEmails: "small@example.com"},
			{Domain: "large.example.com", UsedBytes: 900_000_000, AdminEmails: "large@example.com"},
		},
	}
	application.runAutomaticBillingOnce(context.Background(), store, snapshot, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	invoices := store.Invoices(context.Background(), 10)
	if len(invoices) != 2 {
		t.Fatalf("invoices = %d, want 2: %#v", len(invoices), invoices)
	}
	shares := make(map[string]int64)
	totalCostMinor := int64(0)
	for _, invoice := range invoices {
		if invoice.Currency != "USD" || len(invoice.Lines) != 1 {
			t.Fatalf("invoice = %#v", invoice)
		}
		shares[invoice.CustomerEmail] = invoice.Lines[0].CostShareMinor
		totalCostMinor += invoice.Lines[0].CostShareMinor
	}
	if shares["small@example.com"] != 40 || shares["large@example.com"] != 61 || totalCostMinor != 101 {
		t.Fatalf("cost shares = %#v, total = %d", shares, totalCostMinor)
	}
}

func TestHostingAndSupportClientsExcludeDemoAndIncludeServerOwnerSites(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := hostingandsupport.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	hosting := hostingandsupport.ClientHosting{
		InstallationID: "server-1", ServerDomain: "host.example.com", MonthlyCostMinor: 1000,
		BillingCurrency: "EUR", MinimumPriceGBMinor: 200,
		Roles: []hostingandsupport.ClientHostingRole{{Email: "owner@example.com", Role: "superadmin", Scope: "installation"}},
		Sites: []hostingandsupport.ClientHostingSite{
			{Domain: "real.example.com", OwnerEmail: "client@example.com", AdminEmails: []string{"client@example.com"}, UsedBytes: 600_000_000},
			{Domain: "demo.example.com", OwnerEmail: "demo@example.com", AdminEmails: []string{"demo@example.com"}, UsedBytes: 700_000_000, IsDemo: true},
			{Domain: "owner.example.com", OwnerEmail: "owner@example.com", AdminEmails: []string{"owner@example.com"}, UsedBytes: 700_000_000},
		},
	}
	clients := (&App{}).hostingAndSupportClients(context.Background(), hostingandsupport.Store{DB: database}, nil, nil, nil, []hostingandsupport.ClientHosting{hosting}, nil, 500, nil)
	if len(clients) != 2 {
		t.Fatalf("clients = %#v", clients)
	}
	domainsByEmail := make(map[string]string)
	for _, client := range clients {
		if len(client.Sites) == 1 {
			domainsByEmail[client.PrimaryEmail] = client.Sites[0].Domain
		}
	}
	if domainsByEmail["client@example.com"] != "real.example.com" || domainsByEmail["owner@example.com"] != "owner.example.com" {
		t.Fatalf("client domains = %#v", domainsByEmail)
	}
}

func TestBillingDocumentTemplatesRender(t *testing.T) {
	invoiceBytes, err := fs.ReadFile(embeddedWebFiles, "web/billing_invoice.html")
	if err != nil {
		t.Fatal(err)
	}
	invoiceTemplate, err := template.New("billing_invoice.html").Parse(string(invoiceBytes))
	if err != nil {
		t.Fatal(err)
	}
	var renderedInvoice bytes.Buffer
	err = invoiceTemplate.Execute(&renderedInvoice, map[string]any{
		"T": translationsForLanguageCode("en"), "Title": "Invoice",
		"Invoice":     hostingandsupport.Invoice{Number: "SB-1", CustomerEmail: "client@example.com", ServerName: "host.example.com", Currency: "EUR", PaymentURL: "https://pay.example.com", CreatedAt: "2026-07-23", DueAt: "2026-07-30", PeriodStart: "2026-07-23", PeriodEnd: "2026-08-23"},
		"Lines":       []billingInvoiceLineView{{Domain: "bonus.example.com", Description: "Hosting", UsedLabel: "100 MB", ListLabel: "0.20 EUR", DiscountLabel: "0.20 EUR", TotalLabel: "0.00 EUR", Bonus: true}},
		"AmountLabel": "1.20 EUR", "BonusLabel": "0.20 EUR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renderedInvoice.String(), "SiteBrush bonus") || !strings.Contains(renderedInvoice.String(), "Print / save PDF") {
		t.Fatalf("invoice template did not render expected content: %s", renderedInvoice.String())
	}
	scheduleBytes, err := fs.ReadFile(embeddedWebFiles, "web/billing_schedule.html")
	if err != nil {
		t.Fatal(err)
	}
	scheduleTemplate, err := template.New("billing_schedule.html").Parse(string(scheduleBytes))
	if err != nil {
		t.Fatal(err)
	}
	var renderedSchedule bytes.Buffer
	err = scheduleTemplate.Execute(&renderedSchedule, map[string]any{"T": translationsForLanguageCode("en"), "Title": "Schedule", "Token": "secret", "Customer": hostingandsupport.BillingCustomer{PrimaryEmail: "client@example.com", InvoiceDay: 5, PaymentTermDays: 7, Timezone: "UTC"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renderedSchedule.String(), "client@example.com") || !strings.Contains(renderedSchedule.String(), "name=\"timezone\"") {
		t.Fatalf("schedule template did not render expected content: %s", renderedSchedule.String())
	}
}

func registerServiceMailInstallationForTest(t *testing.T, application *App, request serviceMailRequest) {
	t.Helper()
	controlDatabase, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDatabase.Close()
	store := hostingandsupport.Store{DB: controlDatabase}
	if err := store.UpsertServiceMailInstallation(context.Background(), request.InstallationID, request.PublicKey, "203.0.113.10", request.SourceDomain); err != nil {
		t.Fatal(err)
	}
}

func signedServiceMailRequestForTest(t *testing.T, application *App, request serviceMailRequest) serviceMailRequest {
	t.Helper()
	installationID, publicKey, privateKey, err := application.serviceMailLocalKeyPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request.InstallationID = installationID
	request.PublicKey = base64.StdEncoding.EncodeToString(publicKey)
	request.Signature = ""
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return request
}

func TestContextMenuUsesDirectEditorProfileAndDeleteActions(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "<html><body>docs</body></html>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	result, err := rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "<html><body>docs</body></html>", time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	application.rebuildDomainStorageUsage(context.Background(), "localhost")
	revisionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("revision id: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs/", nil)
	request.Header.Set("Accept-Language", "en")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{"href='?visual'", "href='?text'", "data-sitebrush-action='delete'", "?delete=" + strconv.FormatInt(revisionID, 10), "data-sitebrush-action='protect_password'", "/p/static/lock.png", "Protect with password", "href='?profile'", "href='?analytics'", "/p/static/analytics.svg"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing %q in %s", expectedFragment, body)
		}
	}
	for _, expectedFragment := range []string{"href='https://sitebrush.com'", "class='SiteBrushContextMenuVersion' download>v.", "href='" + latestServerBinaryDownloadURL(runtime.GOOS, runtime.GOARCH) + "'", "SiteBrushMenuStorageUsage", "10.0 GB"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing storage/version fragment %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "href='?edit'") {
		t.Fatalf("context menu still contains intermediate edit link: %s", body)
	}
	for _, expectedFragment := range []string{`window.location.href = targetHref;`, `closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")`, `function closeSitebrushMenu()`, `z-index:2147483647`, `closeSitebrushMenu();`, `data-sitebrush-owned`, `sitebrushContextMenuShadowCSS`, `attachShadow({mode: "open"})`, `menuRoot.appendChild(menuStyleElement)`, `.SiteBrushContextMenuLink:link`, `.SiteBrushContextMenuLink:visited`, `window.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false})`, `installSitebrushLongPressMenu`, `document.addEventListener("pointerdown", startLongPress`, `document.addEventListener("touchstart"`, `positionSitebrushMenuBox(menuBoxElement, menuPoint)`, `max-height:calc(100vh - 16px)`, `@media (pointer: coarse), (max-width: 820px)`, `function openPasswordProtectionDialog`, `SiteBrushPasswordInput`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing navigation guard %q in %s", expectedFragment, body)
		}
	}
	for _, forbiddenFragment := range []string{`window.prompt`, `window.alert`} {
		if strings.Contains(body, forbiddenFragment) {
			t.Fatalf("desktop-safe context menu still contains %q in %s", forbiddenFragment, body)
		}
	}
}

func TestContextMenuShowsRemovePasswordProtectionForProtectedPrefix(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs/child", "/docs/child", "<html><body>docs child</body></html>"); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	application.setPagePasswordRule(context.Background(), "localhost", "/docs", "secret")
	rule, found := application.pagePasswordRuleForPath(context.Background(), "localhost", "/docs/child")
	if !found {
		t.Fatal("page password rule missing")
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs/child", nil)
	request.RemoteAddr = "198.51.100.43:1234"
	request.Header.Set("Accept-Language", "en")
	request.Header.Set("User-Agent", "Sitebrush Test Browser")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	request.AddCookie(newPagePasswordTestCookie(rule, request, time.Now().UTC()))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{"data-sitebrush-action='remove_password_protection'", "/p/static/unlock.png", "Remove password protection"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("protected context menu missing %q in %s", expectedFragment, body)
		}
	}
}

func TestLatestServerBinaryDownloadURL(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
		want   string
	}{
		{name: "linux amd64", goos: "linux", goarch: "amd64", want: "https://files.zabiyaka.net/sitebrush/latest/server-app/sitebrush_linux_amd64"},
		{name: "darwin arm64", goos: "darwin", goarch: "arm64", want: "https://files.zabiyaka.net/sitebrush/latest/server-app/sitebrush_darwin_arm64"},
		{name: "windows amd64", goos: "windows", goarch: "amd64", want: "https://files.zabiyaka.net/sitebrush/latest/server-app/sitebrush_windows_amd64.exe"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := latestServerBinaryDownloadURL(testCase.goos, testCase.goarch)
			if got != testCase.want {
				t.Fatalf("latestServerBinaryDownloadURL() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestAnalyticsReportBuildsGoogleAnalyticsStyleMetrics(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	events := []siteAnalyticsEvent{
		{
			Domain:         "localhost",
			Path:           "/robots",
			Method:         http.MethodGet,
			StatusCode:     http.StatusOK,
			ContentSource:  "dynamic",
			OccurredAt:     now.Add(-90 * time.Minute),
			Duration:       60 * time.Millisecond,
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2; +https://openai.com/gptbot",
			Referer:        "https://example.org/index",
			AcceptLanguage: "en-GB,en;q=0.9",
			VisitorID:      "visitor-bot",
		},
		{
			Domain:         "localhost",
			Path:           "/docs",
			Method:         http.MethodGet,
			StatusCode:     http.StatusOK,
			ContentSource:  "dynamic",
			OccurredAt:     now.Add(-50 * time.Minute),
			Duration:       70 * time.Millisecond,
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2; +https://openai.com/gptbot",
			Referer:        "https://example.org/again",
			AcceptLanguage: "en-GB,en;q=0.9",
			VisitorID:      "visitor-bot",
		},
		{
			Domain:         "localhost",
			Path:           "/",
			Method:         http.MethodGet,
			StatusCode:     http.StatusOK,
			ContentSource:  "dynamic",
			OccurredAt:     now.Add(-40 * time.Minute),
			Duration:       120 * time.Millisecond,
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15",
			Referer:        "https://www.google.com/search?q=sitebrush",
			AcceptLanguage: "en-US,en;q=0.9",
			VisitorID:      "visitor-a",
		},
		{
			Domain:         "localhost",
			Path:           "/pricing",
			Method:         http.MethodGet,
			StatusCode:     http.StatusOK,
			ContentSource:  "dynamic",
			OccurredAt:     now.Add(-35 * time.Minute),
			Duration:       450 * time.Millisecond,
			UserAgent:      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1",
			Referer:        "https://t.co/example",
			AcceptLanguage: "ru-RU,ru;q=0.9",
			VisitorID:      "visitor-a",
		},
		{
			Domain:         "localhost",
			Path:           "/docs",
			Method:         http.MethodGet,
			StatusCode:     http.StatusOK,
			ContentSource:  "dynamic",
			OccurredAt:     now.Add(-5 * time.Minute),
			Duration:       210 * time.Millisecond,
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0.0.0 Safari/537.36",
			Referer:        "",
			AcceptLanguage: "de-DE,de;q=0.9",
			VisitorID:      "visitor-b",
		},
		{
			Domain:        "localhost",
			Path:          "/p/logo.png",
			Method:        http.MethodGet,
			StatusCode:    http.StatusOK,
			ContentSource: "static",
			OccurredAt:    now.Add(-4 * time.Minute),
			Duration:      30 * time.Millisecond,
			UserAgent:     "Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36",
			VisitorID:     "visitor-b",
			IsAsset:       true,
		},
		{
			Domain:        "localhost",
			Path:          "/missing",
			Method:        http.MethodGet,
			StatusCode:    http.StatusNotFound,
			ContentSource: "dynamic",
			OccurredAt:    now.Add(-3 * time.Minute),
			Duration:      80 * time.Millisecond,
			UserAgent:     "Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36",
			VisitorID:     "visitor-c",
		},
		{
			Domain:        "localhost",
			Path:          "/docs",
			Query:         "settings=",
			Method:        http.MethodGet,
			StatusCode:    http.StatusOK,
			ContentSource: "request",
			OccurredAt:    now.Add(-2 * time.Minute),
			Duration:      95 * time.Millisecond,
			UserAgent:     "Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36",
			VisitorID:     "visitor-admin",
			IsAdmin:       true,
			IsController:  true,
		},
	}

	report := buildAnalyticsReportFromEvents(events, now)
	if report.TotalRequests != 8 {
		t.Fatalf("total requests = %d, want 8", report.TotalRequests)
	}
	if report.PageViews != 6 {
		t.Fatalf("page views = %d, want 6", report.PageViews)
	}
	if report.UniqueVisitors != 5 {
		t.Fatalf("unique visitors = %d, want 5", report.UniqueVisitors)
	}
	if report.HumanRequests != 6 || report.BotRequests != 2 {
		t.Fatalf("human/bot requests = %d/%d, want 6/2", report.HumanRequests, report.BotRequests)
	}
	if report.ReturningVisitors != 1 || report.ReturnVisits != 1 {
		t.Fatalf("returning visitors/visits = %d/%d, want 1/1", report.ReturningVisitors, report.ReturnVisits)
	}
	if report.Sessions != 5 {
		t.Fatalf("sessions = %d, want 5", report.Sessions)
	}
	if report.BounceRate < 79.9 || report.BounceRate > 80.1 {
		t.Fatalf("bounce rate = %.1f, want about 80.0", report.BounceRate)
	}
	if report.ErrorCount != 1 {
		t.Fatalf("errors = %d, want 1", report.ErrorCount)
	}
	if report.AdminRequests != 1 {
		t.Fatalf("admin requests = %d, want 1", report.AdminRequests)
	}
	if report.StaticRequests != 1 {
		t.Fatalf("static requests = %d, want 1", report.StaticRequests)
	}
	assertAnalyticsRow(t, report.TopPages, "/", 1)
	assertAnalyticsRow(t, report.TopPages, "/pricing", 1)
	assertAnalyticsRow(t, report.TopPages, "/docs", 2)
	assertAnalyticsRow(t, report.TopPages, "/missing", 1)
	assertAnalyticsRow(t, report.TopPages, "/robots", 1)
	assertAnalyticsRow(t, report.TrafficSources, "organic search", 1)
	assertAnalyticsRow(t, report.TrafficSources, "social", 1)
	assertAnalyticsRow(t, report.TrafficSources, "direct", 2)
	assertAnalyticsRow(t, report.TrafficSources, "referral", 2)
	assertAnalyticsRow(t, report.Devices, "desktop", 3)
	assertAnalyticsRow(t, report.Devices, "mobile", 1)
	assertAnalyticsRow(t, report.Devices, "bot", 2)
	assertAnalyticsRow(t, report.VisitorTypes, "human", 4)
	assertAnalyticsRow(t, report.VisitorTypes, "bot", 2)
	assertAnalyticsRow(t, report.BotCrawlers, "GPTBot", 2)
	assertAnalyticsRow(t, report.BotReturnSources, "referral", 1)
	assertAnalyticsRow(t, report.BotReferrers, "example.org", 2)
	assertAnalyticsRow(t, report.Countries, "United Kingdom", 2)
	assertAnalyticsRow(t, report.Countries, "Russia", 1)
	assertAnalyticsRow(t, report.EntryHours, "11:00", 4)
	assertAnalyticsRow(t, report.StatusCodes, "404", 1)
	assertAnalyticsRow(t, report.TopAssets, "/p/logo.png", 1)
	assertAnalyticsRow(t, report.ErrorPaths, "/missing 404", 1)
}

func TestAnalyticsAggregateStoresProcessedReportAndOverloadMarkers(t *testing.T) {
	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	state := newAnalyticsAggregateState(4096)
	state.record(siteAnalyticsEvent{
		Domain:         "localhost",
		Path:           "/",
		Method:         http.MethodGet,
		StatusCode:     http.StatusOK,
		ContentSource:  "static",
		OccurredAt:     now,
		Duration:       25 * time.Millisecond,
		ClientIP:       "127.0.0.1",
		UserAgent:      "Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36",
		AcceptLanguage: "en-US,en;q=0.9",
		VisitorID:      "visitor-a",
	})
	report := state.reports(now)["localhost"]
	if report.TotalRequests != 1 || report.PageViews != 1 || report.StaticRequests != 1 {
		t.Fatalf("report counts = total:%d views:%d static:%d, want 1/1/1", report.TotalRequests, report.PageViews, report.StaticRequests)
	}
	assertAnalyticsRow(t, report.TopPages, "/", 1)

	overloadedState := newAnalyticsAggregateState(1)
	overloadedState.record(siteAnalyticsEvent{
		Domain:        "localhost",
		Path:          "/heavy",
		Method:        http.MethodGet,
		StatusCode:    http.StatusOK,
		ContentSource: "static",
		OccurredAt:    now,
		Duration:      10 * time.Millisecond,
		UserAgent:     "Mozilla/5.0",
		VisitorID:     "visitor-b",
	})
	overloadReport := overloadedState.reports(now.Add(time.Minute))["localhost"]
	assertAnalyticsRow(t, overloadReport.SystemEvents, "analytics overload started", 1)
	assertAnalyticsRow(t, overloadReport.SystemEvents, "analytics overload ended", 1)
	if overloadReport.TotalRequests != 0 {
		t.Fatalf("overloaded report total requests = %d, want cleared data", overloadReport.TotalRequests)
	}
}

func TestAnalyticsPageRequiresAdminAndRendersPreparedReport(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	report := analyticsPreparedReport{
		GeneratedAt:    time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		PeriodStart:    time.Date(2026, 5, 10, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		PeriodEnd:      time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		TotalRequests:  3,
		PageViews:      2,
		UniqueVisitors: 1,
		Sessions:       1,
		TopPages:       []analyticsCountRow{{Label: "/docs", Count: 2}},
		TrafficSources: []analyticsCountRow{{Label: "direct", Count: 2}},
	}
	if err := application.saveAnalyticsReport(context.Background(), "localhost", report); err != nil {
		t.Fatalf("save analytics report: %v", err)
	}

	guestRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?analytics", nil)
	guestResponse := httptest.NewRecorder()
	application.route(guestResponse, guestRequest)
	if guestResponse.Code != http.StatusFound {
		t.Fatalf("guest status = %d, want %d", guestResponse.Code, http.StatusFound)
	}
	if !strings.Contains(guestResponse.Header().Get("Location"), "login") {
		t.Fatalf("guest redirect missing login: %q", guestResponse.Header().Get("Location"))
	}

	adminRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?analytics", nil)
	adminRequest.Header.Set("Accept-Language", "en")
	adminRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	adminResponse := httptest.NewRecorder()
	application.route(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body=%q", adminResponse.Code, adminResponse.Body.String())
	}
	body := adminResponse.Body.String()
	for _, expectedFragment := range []string{"Analytics", "Total requests", "/docs", "Direct", `href="/docs"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("analytics page missing %q in %s", expectedFragment, body)
		}
	}
}

func assertAnalyticsRow(t *testing.T, rows []analyticsCountRow, label string, count int) {
	t.Helper()
	for _, row := range rows {
		if row.Label == label {
			if row.Count != count {
				t.Fatalf("row %q count = %d, want %d", label, row.Count, count)
			}
			return
		}
	}
	t.Fatalf("missing analytics row %q in %#v", label, rows)
}

func TestDomainStorageUsageRebuildsFromActualDiskUsage(t *testing.T) {
	application, rawDB := newTestApplication(t)
	domainFilesDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(filepath.Join(domainFilesDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "imported.png"), []byte("imported image"), 0o644); err != nil {
		t.Fatal(err)
	}
	chrootDownloadsDir := filepath.Join(application.domainChrootRootDir("localhost"), "downloads")
	if err := os.MkdirAll(chrootDownloadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chrootDownloadsDir, "manual.zip"), []byte("manual download"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := rawDB.Exec(`INSERT OR REPLACE INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"localhost", 0, 0, 0, 0, 0, defaultDomainStorageLimitBytes, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed stale storage usage: %v", err)
	}

	usage := application.domainStorageUsage(context.Background(), "localhost")
	expectedFileBytes := diskusage.DirectorySize(domainFilesDir) + diskusage.DirectorySize(application.domainChrootRootDir("localhost"))
	if usage.FileBytes != expectedFileBytes {
		t.Fatalf("file bytes = %d, want actual disk usage %d", usage.FileBytes, expectedFileBytes)
	}
}

func TestChrootLocationSettingsCreatesDirectoryAndServesIndex(t *testing.T) {
	application, rawDB := newTestApplication(t)
	form := url.Values{}
	form.Set("action", "save_chroot_location")
	form.Set("location_url_path", "/downloads")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	application.handleDomainSettingsPost(request.Context(), request, "localhost", "")

	expectedDirectoryPath := filepath.Join(application.domainChrootRootDir("localhost"), "downloads")
	directoryInfo, err := os.Stat(expectedDirectoryPath)
	if err != nil {
		t.Fatalf("location directory was not created: %v", err)
	}
	if !directoryInfo.IsDir() {
		t.Fatalf("location path is not a directory: %s", expectedDirectoryPath)
	}
	var storedDirectoryPath string
	if err := rawDB.QueryRow(`SELECT directory_path FROM domain_chroot_locations WHERE domain=? AND url_path=?`, "localhost", "/downloads").Scan(&storedDirectoryPath); err != nil {
		t.Fatalf("read stored chroot location: %v", err)
	}
	expectedRealDirectoryPath, err := filepath.EvalSymlinks(expectedDirectoryPath)
	if err != nil {
		t.Fatalf("resolve expected directory: %v", err)
	}
	if storedDirectoryPath != expectedRealDirectoryPath {
		t.Fatalf("stored directory = %q, want %q", storedDirectoryPath, expectedRealDirectoryPath)
	}
	if err := os.WriteFile(filepath.Join(expectedDirectoryPath, "index.html"), []byte("<h1>Downloads</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}

	redirectResponse := httptest.NewRecorder()
	application.route(redirectResponse, httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads", nil))
	if redirectResponse.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d, want %d", redirectResponse.Code, http.StatusMovedPermanently)
	}
	if location := redirectResponse.Header().Get("Location"); location != "/downloads/" {
		t.Fatalf("redirect location = %q, want %q", location, "/downloads/")
	}

	response := httptest.NewRecorder()
	application.route(response, httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "<h1>Downloads</h1>") {
		t.Fatalf("body does not include index.html: %q", response.Body.String())
	}
}

func TestDomainSettingsRendersCustomDomainSetupFirst(t *testing.T) {
	application, _ := newTestApplication(t)
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?settings", nil)
	request.Header.Set("Accept-Language", "en")
	response := httptest.NewRecorder()

	sslSetting := DomainAutomaticSSLSetting{Domain: "example.com", Enabled: true, Available: true}
	application.render(response, request, "domain_settings.html", map[string]any{
		"Domain":         "localhost",
		"SelectedDomain": "example.com",
		"Aliases": []DomainAlias{{
			Domain:            "example.com",
			DNSHostName:       "@",
			VerificationToken: "verify-token",
			TXTVerified:       true,
			ARecordVerified:   true,
			IsActive:          true,
			IsSelected:        true,
			LastCheckedAt:     "2026-06-12T00:00:00Z",
			AutomaticSSL:      DomainAutomaticSSLSetting{Domain: "example.com", CertificateValid: true, CertificateExpiresAt: "2026-09-01 12:00:00 UTC", CertificateRemaining: "22 days"},
		}},
		"ChrootLocations":    []DomainChrootLocation{},
		"AliasCount":         1,
		"CanAddAlias":        true,
		"ReturnPath":         "/",
		"ExternalIP":         "203.0.113.10",
		"AutomaticSSL":       sslSetting,
		"AutomaticSSLStatus": automaticSSLStatusView(sslSetting, nil),
		"AutomaticSSLDomain": sslSetting.Domain,
		"AutomaticSSLReady":  true,
		"BackupDownloadURL":  "http://localhost:8080/?backup_download&token=backup-token",
		"BackupDownloadPath": "/?backup_download&token=backup-token",
		"NativeFileDialog":   false,
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{
		"First step",
		"Connect your own second-level domain",
		"Add domain",
		"How to turn it on",
		"DNS for the root domain",
		"Host/name",
		"<code>@</code>",
		"sitebrush=verify-token",
		"203.0.113.10",
		"Proves that you own this domain.",
		"Sends visitors to this Sitebrush server.",
		"Renew certificate now",
		"2026-09-01 12:00:00 UTC",
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("domain settings page missing %q in %s", expectedFragment, body)
		}
	}
	customDomainIndex := strings.Index(body, "Connect your own second-level domain")
	sslIndex := strings.Index(body, "SSL certificate")
	backupIndex := strings.Index(body, "Site backup")
	staticFoldersIndex := strings.Index(body, "Static folders")
	if customDomainIndex < 0 || sslIndex < 0 || backupIndex < 0 || staticFoldersIndex < 0 {
		t.Fatalf("domain settings page missing ordered sections in %s", body)
	}
	if customDomainIndex > sslIndex || customDomainIndex > backupIndex || customDomainIndex > staticFoldersIndex {
		t.Fatalf("custom domain setup is not the first settings section in %s", body)
	}
}

func TestDNSHostNameForAliasUsesProviderHostFieldValues(t *testing.T) {
	for aliasDomain, expectedHostName := range map[string]string{
		"example.com":     "@",
		"example.com.":    "@",
		"www.example.com": "www",
		"shop.example.ru": "shop",
		"":                "@",
	} {
		if hostName := dnsHostNameForAlias(aliasDomain); hostName != expectedHostName {
			t.Fatalf("dnsHostNameForAlias(%q) = %q, want %q", aliasDomain, hostName, expectedHostName)
		}
	}
}

func TestChrootDirectoryListingUsesEmbeddedSitebrushStyle(t *testing.T) {
	application, _ := newTestApplication(t)
	form := url.Values{}
	form.Set("action", "save_chroot_location")
	form.Set("location_url_path", "/downloads")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(request.Context(), request, "localhost", "")

	downloadsDir := filepath.Join(application.domainChrootRootDir("localhost"), "downloads")
	if err := os.MkdirAll(filepath.Join(downloadsDir, "manuals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloadsDir, "release notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	application.route(response, httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`/p/static/directory_listing.css`,
		`class="directory-panel"`,
		`Содержимое папки`,
		`manuals/`,
		`release notes.txt`,
		`release%20notes.txt`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("listing body missing %q: %q", expected, body)
		}
	}
}

func TestChrootLocationAllowsSymlinksInsideDomainChroot(t *testing.T) {
	application, _ := newTestApplication(t)
	createChrootLocationForTest(t, application, "/downloads")
	chrootRoot := application.domainChrootRootDir("localhost")
	releaseDir := filepath.Join(chrootRoot, "releases", "v1")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "index.html"), []byte("<h1>Latest release</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	createTestSymlink(t, filepath.Join("..", "releases", "v1"), filepath.Join(chrootRoot, "downloads", "latest"))

	response := httptest.NewRecorder()
	application.route(response, httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads/latest/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "<h1>Latest release</h1>") {
		t.Fatalf("body does not include symlinked index: %q", response.Body.String())
	}

	listingRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads/", nil)
	listingRequest.Header.Set("Accept-Language", "en")
	listingResponse := httptest.NewRecorder()
	application.route(listingResponse, listingRequest)
	if listingResponse.Code != http.StatusOK {
		t.Fatalf("listing status = %d, want %d", listingResponse.Code, http.StatusOK)
	}
	if !strings.Contains(listingResponse.Body.String(), "latest/") || !strings.Contains(listingResponse.Body.String(), "Folder link") {
		t.Fatalf("listing does not show internal symlink: %q", listingResponse.Body.String())
	}
}

func TestChrootLocationBlocksSymlinksOutsideDomainChroot(t *testing.T) {
	application, _ := newTestApplication(t)
	createChrootLocationForTest(t, application, "/downloads")
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "passwd"), []byte("root:x:0:0"), 0o644); err != nil {
		t.Fatal(err)
	}
	createTestSymlink(t, outsideDir, filepath.Join(application.domainChrootRootDir("localhost"), "downloads", "escape"))

	response := httptest.NewRecorder()
	application.route(response, httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads/escape/passwd", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if strings.Contains(response.Body.String(), "root:x:0:0") {
		t.Fatalf("blocked external symlink leaked file body: %q", response.Body.String())
	}

	listingResponse := httptest.NewRecorder()
	application.route(listingResponse, httptest.NewRequest(http.MethodGet, "http://localhost:8080/downloads/", nil))
	if listingResponse.Code != http.StatusOK {
		t.Fatalf("listing status = %d, want %d", listingResponse.Code, http.StatusOK)
	}
	if strings.Contains(listingResponse.Body.String(), "escape") {
		t.Fatalf("listing exposes blocked external symlink: %q", listingResponse.Body.String())
	}
}

func createChrootLocationForTest(t *testing.T, application *App, locationPath string) {
	t.Helper()
	form := url.Values{}
	form.Set("action", "save_chroot_location")
	form.Set("location_url_path", locationPath)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(request.Context(), request, "localhost", "")
}

func createTestSymlink(t *testing.T, oldName, newName string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests require privileges on Windows")
	}
	if err := os.Symlink(oldName, newName); err != nil {
		t.Fatalf("create symlink %s -> %s: %v", newName, oldName, err)
	}
}

func TestParseSiteQuotaLimitBytesAcceptsMegabytesOrGigabytes(t *testing.T) {
	megabyteLimit, megabyteRequested, megabyteErr := parseSiteQuotaLimitBytes("512mb")
	if megabyteErr != nil {
		t.Fatalf("megabyte quota: %v", megabyteErr)
	}
	if !megabyteRequested || megabyteLimit != 512*1024*1024 {
		t.Fatalf("megabyte quota = %d requested=%v", megabyteLimit, megabyteRequested)
	}

	gigabyteLimit, gigabyteRequested, gigabyteErr := parseSiteQuotaLimitBytes("3gb")
	if gigabyteErr != nil {
		t.Fatalf("gigabyte quota: %v", gigabyteErr)
	}
	if !gigabyteRequested || gigabyteLimit != 3*1024*1024*1024 {
		t.Fatalf("gigabyte quota = %d requested=%v", gigabyteLimit, gigabyteRequested)
	}

	if _, _, err := parseSiteQuotaLimitBytes("256"); err == nil {
		t.Fatal("expected quota without unit to be rejected")
	}
}

func insertSiteQuotaAdmin(t *testing.T, rawDB *sql.DB, domain string) {
	t.Helper()
	_, err := rawDB.Exec(`INSERT OR REPLACE INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, domain, "admin@"+domain, "hashed-password")
	if err != nil {
		t.Fatalf("insert site quota admin for %s: %v", domain, err)
	}
}

func TestSiteQuotaCommandListsAndUpdatesPerSiteDatabase(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("example.com")+".db")
	if err := ensureParentDir(siteDatabasePath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath}
	if err := application.migrate(contextWithDomain(context.Background(), "example.com")); err != nil {
		t.Fatalf("migrate site db: %v", err)
	}
	insertSiteQuotaAdmin(t, rawDB, "example.com")
	if _, err := rawDB.Exec(`INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,?)`, "example.com", "/", "Home", strings.Repeat("A", 64), 0); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at) VALUES(?,?,?,?,?,?,?)`,
		"example.com", "www.example.com", "token", 1, 1, 1, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert alias: %v", err)
	}

	var listOutput bytes.Buffer
	if err := runSiteQuotaCommand(context.Background(), &listOutput, strings.NewReader("q\n"), storagePath, dbPath, true, "", ""); err != nil {
		t.Fatalf("list sites: %v", err)
	}
	if !strings.Contains(listOutput.String(), "example.com") {
		t.Fatalf("list output missing site: %s", listOutput.String())
	}
	if !strings.Contains(listOutput.String(), "aliases:1") {
		t.Fatalf("list output missing alias: %s", listOutput.String())
	}
	if strings.Contains(listOutput.String(), "dir:") {
		t.Fatalf("list output should stay compact without directory path: %s", listOutput.String())
	}

	var updateOutput bytes.Buffer
	if err := runSiteQuotaCommand(context.Background(), &updateOutput, strings.NewReader(""), storagePath, dbPath, false, "example.com", "2gb"); err != nil {
		t.Fatalf("update quota: %v", err)
	}
	if !strings.Contains(updateOutput.String(), "2.0 GB limit") {
		t.Fatalf("update output missing limit: %s", updateOutput.String())
	}
	var limitBytes int64
	if err := rawDB.QueryRow(`SELECT limit_bytes FROM domain_storage_usage WHERE domain=?`, "example.com").Scan(&limitBytes); err != nil {
		t.Fatalf("read updated quota: %v", err)
	}
	if limitBytes != 2*1024*1024*1024 {
		t.Fatalf("limit bytes = %d, want 2 GiB", limitBytes)
	}
}

func TestListSiteQuotaRowsRequiresRegisteredAdmin(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	workingDatabasePath := filepath.Join(siteDatabaseDir, domainStorageName("working.example")+".db")
	junkDatabasePath := filepath.Join(siteDatabaseDir, domainStorageName("junk.example")+".db")
	for _, databasePath := range []string{workingDatabasePath, junkDatabasePath} {
		if err := ensureParentDir(databasePath); err != nil {
			t.Fatal(err)
		}
	}

	workingDB, err := sql.Open("sqlite", "file:"+workingDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer workingDB.Close()
	junkDB, err := sql.Open("sqlite", "file:"+junkDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer junkDB.Close()
	if err := (&App{db: workingDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "working.example")); err != nil {
		t.Fatalf("migrate working db: %v", err)
	}
	if err := (&App{db: junkDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "junk.example")); err != nil {
		t.Fatalf("migrate junk db: %v", err)
	}
	insertSiteQuotaAdmin(t, workingDB, "working.example")
	if _, err := junkDB.Exec(`INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,?)`, "junk.example", "/", "Junk", "leftover", 0); err != nil {
		t.Fatalf("insert junk page: %v", err)
	}
	if _, err := junkDB.Exec(`INSERT OR REPLACE INTO domain_storage_usage(domain,page_bytes,limit_bytes) VALUES(?,?,?)`, "junk.example", 128, defaultDomainStorageLimitBytes); err != nil {
		t.Fatalf("insert junk usage: %v", err)
	}
	if _, err := junkDB.Exec(`INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at) VALUES(?,?,?,?,?,?,?)`,
		"junk.example", "www.junk.example", "token", 1, 1, 1, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert junk alias: %v", err)
	}

	rows, err := listSiteQuotaRows(context.Background(), storagePath, dbPath)
	if err != nil {
		t.Fatalf("list quota rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Domain != "working.example" {
		t.Fatalf("rows = %#v, want only working.example", rows)
	}
}

func TestPerSiteDBRouterPreloadOpensRegisteredSiteDatabases(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	domains := []string{"alpha.example", "beta.example"}
	for _, domain := range domains {
		databasePath := filepath.Join(siteDatabaseDir, domainStorageName(domain)+".db")
		if err := ensureParentDir(databasePath); err != nil {
			t.Fatal(err)
		}
		rawDB, err := sql.Open("sqlite", "file:"+databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := (&App{db: rawDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), domain)); err != nil {
			_ = rawDB.Close()
			t.Fatalf("migrate %s: %v", domain, err)
		}
		insertSiteQuotaAdmin(t, rawDB, domain)
		if closeErr := rawDB.Close(); closeErr != nil {
			t.Fatalf("close seed db %s: %v", domain, closeErr)
		}
	}

	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, migrateDomain string) error {
		return (&App{db: rawDB, storagePath: storagePath}).migrate(contextWithDomain(migrationCtx, migrateDomain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)

	preloadResponse := router.Preload(context.Background())
	if preloadResponse.err != nil {
		t.Fatalf("preload err: %v", preloadResponse.err)
	}
	if preloadResponse.total != len(domains) || preloadResponse.opened != len(domains) || preloadResponse.failed != 0 {
		t.Fatalf("preload response = %#v, want all registered site dbs opened", preloadResponse)
	}
}

func TestListSiteQuotaRowsMergesDuplicateDomainsAndAliasSites(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	primaryDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("example.com")+".db")
	aliasDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("www.example.com")+".db")
	for _, databasePath := range []string{dbPath, primaryDatabasePath, aliasDatabasePath} {
		if err := ensureParentDir(databasePath); err != nil {
			t.Fatal(err)
		}
	}

	legacyDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyDB.Close()
	primaryDB, err := sql.Open("sqlite", "file:"+primaryDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer primaryDB.Close()
	aliasDB, err := sql.Open("sqlite", "file:"+aliasDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer aliasDB.Close()
	if err := (&App{db: legacyDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "example.com")); err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	if err := (&App{db: primaryDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "example.com")); err != nil {
		t.Fatalf("migrate primary db: %v", err)
	}
	if err := (&App{db: aliasDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "www.example.com")); err != nil {
		t.Fatalf("migrate alias db: %v", err)
	}
	insertSiteQuotaAdmin(t, legacyDB, "example.com")
	insertSiteQuotaAdmin(t, primaryDB, "example.com")
	insertSiteQuotaAdmin(t, aliasDB, "www.example.com")
	if _, err := primaryDB.Exec(`INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at) VALUES(?,?,?,?,?,?,?)`,
		"example.com", "www.example.com", "token", 1, 1, 1, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert primary alias: %v", err)
	}

	rows, err := listSiteQuotaRows(context.Background(), storagePath, dbPath)
	if err != nil {
		t.Fatalf("list quota rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.Domain != "example.com" {
		t.Fatalf("domain = %q, want example.com", row.Domain)
	}
	if !sameSiteQuotaPath(row.DatabasePath, primaryDatabasePath) {
		t.Fatalf("database path = %q, want primary per-site db %q", row.DatabasePath, primaryDatabasePath)
	}
	if len(row.Aliases) != 1 || row.Aliases[0] != "www.example.com" {
		t.Fatalf("aliases = %#v, want www.example.com", row.Aliases)
	}
}

func TestSiteQuotaInteractiveConsoleUpdatesQuota(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("example.com")+".db")
	if err := ensureParentDir(siteDatabasePath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath}
	if err := application.migrate(contextWithDomain(context.Background(), "example.com")); err != nil {
		t.Fatalf("migrate site db: %v", err)
	}
	insertSiteQuotaAdmin(t, rawDB, "example.com")

	var output bytes.Buffer
	input := strings.NewReader("1\n64mb\nq\n")
	if err := runSiteQuotaCommand(context.Background(), &output, input, storagePath, dbPath, true, "", ""); err != nil {
		t.Fatalf("interactive quota: %v", err)
	}
	var limitBytes int64
	if err := rawDB.QueryRow(`SELECT limit_bytes FROM domain_storage_usage WHERE domain=?`, "example.com").Scan(&limitBytes); err != nil {
		t.Fatalf("read interactive quota: %v", err)
	}
	if limitBytes != 64*1024*1024 {
		t.Fatalf("limit bytes = %d, want 64 MiB", limitBytes)
	}
	if !strings.Contains(output.String(), "Updated example.com") {
		t.Fatalf("interactive output missing update confirmation: %s", output.String())
	}
}

func TestSiteQuotaInteractiveConsoleQuitsFromQuotaPrompt(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("example.com")+".db")
	if err := ensureParentDir(siteDatabasePath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath}
	if err := application.migrate(contextWithDomain(context.Background(), "example.com")); err != nil {
		t.Fatalf("migrate site db: %v", err)
	}
	insertSiteQuotaAdmin(t, rawDB, "example.com")

	var output bytes.Buffer
	input := strings.NewReader("1\nq\n")
	if err := runSiteQuotaCommand(context.Background(), &output, input, storagePath, dbPath, true, "", ""); err != nil {
		t.Fatalf("quota prompt quit: %v", err)
	}
	var limitBytes int64
	if err := rawDB.QueryRow(`SELECT limit_bytes FROM domain_storage_usage WHERE domain=?`, "example.com").Scan(&limitBytes); err != nil {
		t.Fatalf("read quota after quit: %v", err)
	}
	if limitBytes != defaultDomainStorageLimitBytes {
		t.Fatalf("limit bytes = %d, want unchanged default", limitBytes)
	}
}

func TestSiteQuotaInteractiveConsoleSupportsArrowSelection(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	firstDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("alpha.com")+".db")
	secondDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("beta.com")+".db")
	for _, databasePath := range []string{firstDatabasePath, secondDatabasePath} {
		if err := ensureParentDir(databasePath); err != nil {
			t.Fatal(err)
		}
	}
	firstDB, err := sql.Open("sqlite", "file:"+firstDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer firstDB.Close()
	secondDB, err := sql.Open("sqlite", "file:"+secondDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	if err := (&App{db: firstDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "alpha.com")); err != nil {
		t.Fatalf("migrate first db: %v", err)
	}
	if err := (&App{db: secondDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "beta.com")); err != nil {
		t.Fatalf("migrate second db: %v", err)
	}
	insertSiteQuotaAdmin(t, firstDB, "alpha.com")
	insertSiteQuotaAdmin(t, secondDB, "beta.com")

	var output bytes.Buffer
	input := strings.NewReader("\x1b[B\n\n128mb\nq\n")
	if err := runSiteQuotaCommand(context.Background(), &output, input, storagePath, dbPath, true, "", ""); err != nil {
		t.Fatalf("interactive arrow quota: %v", err)
	}
	var firstLimitBytes int64
	if err := firstDB.QueryRow(`SELECT limit_bytes FROM domain_storage_usage WHERE domain=?`, "alpha.com").Scan(&firstLimitBytes); err != nil {
		t.Fatalf("read first quota: %v", err)
	}
	if firstLimitBytes != defaultDomainStorageLimitBytes {
		t.Fatalf("first limit bytes = %d, want unchanged default", firstLimitBytes)
	}
	var secondLimitBytes int64
	if err := secondDB.QueryRow(`SELECT limit_bytes FROM domain_storage_usage WHERE domain=?`, "beta.com").Scan(&secondLimitBytes); err != nil {
		t.Fatalf("read second quota: %v", err)
	}
	if secondLimitBytes != 128*1024*1024 {
		t.Fatalf("second limit bytes = %d, want 128 MiB", secondLimitBytes)
	}
	if !strings.Contains(output.String(), "free") {
		t.Fatalf("interactive output missing free space label: %s", output.String())
	}
}

func TestSiteQuotaInteractiveConsoleSetsBillingMainDomain(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	firstDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("alpha.com")+".db")
	secondDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName("beta.com")+".db")
	for _, databasePath := range []string{firstDatabasePath, secondDatabasePath} {
		if err := ensureParentDir(databasePath); err != nil {
			t.Fatal(err)
		}
	}
	firstDB, err := sql.Open("sqlite", "file:"+firstDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer firstDB.Close()
	secondDB, err := sql.Open("sqlite", "file:"+secondDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	if err := (&App{db: firstDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "alpha.com")); err != nil {
		t.Fatalf("migrate first db: %v", err)
	}
	if err := (&App{db: secondDB, storagePath: storagePath}).migrate(contextWithDomain(context.Background(), "beta.com")); err != nil {
		t.Fatalf("migrate second db: %v", err)
	}
	insertSiteQuotaAdmin(t, firstDB, "alpha.com")
	insertSiteQuotaAdmin(t, secondDB, "beta.com")

	var output bytes.Buffer
	input := strings.NewReader("\x1b[Bb\nq\n")
	if err := runSiteQuotaCommand(context.Background(), &output, input, storagePath, dbPath, true, "", ""); err != nil {
		t.Fatalf("set billing main domain: %v", err)
	}

	controlDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDB.Close()
	var ownerDomain string
	var ownerEmail string
	if err := controlDB.QueryRow(`SELECT domain,email FROM server_managers WHERE role='owner'`).Scan(&ownerDomain, &ownerEmail); err != nil {
		t.Fatalf("read billing owner: %v", err)
	}
	if ownerDomain != "beta.com" || ownerEmail != "admin@beta.com" {
		t.Fatalf("owner = %s %s, want beta.com admin@beta.com", ownerDomain, ownerEmail)
	}
	if !strings.Contains(output.String(), "Billing main domain set to beta.com") {
		t.Fatalf("interactive output missing billing confirmation: %s", output.String())
	}
	if !strings.Contains(output.String(), "billing:main") {
		t.Fatalf("interactive output missing billing marker: %s", output.String())
	}
}

func TestHostingAndSupportMainDomainDefaultsToFirstRegisteredDomain(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	if err := ensureParentDir(dbPath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath, dbPath: dbPath}
	if err := application.migrate(contextWithDomain(context.Background(), "localhost")); err != nil {
		t.Fatal(err)
	}
	insertSiteQuotaAdmin(t, rawDB, "localhost")

	mainDomain := application.hostingAndSupportMainDomain(context.Background())
	if mainDomain != "localhost" {
		t.Fatalf("main domain = %q, want localhost", mainDomain)
	}
	var ownerDomain string
	if err := rawDB.QueryRow(`SELECT domain FROM server_managers WHERE role='owner'`).Scan(&ownerDomain); err != nil {
		t.Fatalf("read promoted owner domain: %v", err)
	}
	if ownerDomain != "localhost" {
		t.Fatalf("owner domain = %q, want localhost", ownerDomain)
	}
}

func TestHostingAndSupportRedirectsToMainDomain(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	if err := ensureParentDir(dbPath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath, dbPath: dbPath}
	if err := application.migrate(contextWithDomain(context.Background(), "alpha.com")); err != nil {
		t.Fatal(err)
	}
	if err := hostingandsupport.Migrate(context.Background(), rawDB); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`INSERT INTO server_managers(domain,email,role,scope_domain,created_at) VALUES(?,?,?,?,?)`, "alpha.com", "admin@alpha.com", "owner", "*", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://beta.com/?hosting_and_support", nil)
	response := httptest.NewRecorder()
	if !application.redirectToHostingAndSupportMainDomain(response, request) {
		t.Fatal("redirect was not issued")
	}
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusFound)
	}
	if location := response.Header().Get("Location"); location != "http://alpha.com/?hosting_and_support" {
		t.Fatalf("location = %q, want http://alpha.com/?hosting_and_support", location)
	}
	if redirectHost := hostingAndSupportRedirectHost("alpha.com", "beta.com:18080"); redirectHost != "alpha.com:18080" {
		t.Fatalf("redirect host = %q, want alpha.com:18080", redirectHost)
	}
}

func TestServerManagerEmailTreatsLoopbackAsLocalhost(t *testing.T) {
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	t.Cleanup(func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
	})
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "example.com" {
			return []net.IP{net.ParseIP("198.51.100.10")}, nil
		}
		return nil, fmt.Errorf("unexpected domain %s", domain)
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}

	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	if err := ensureParentDir(dbPath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath, dbPath: dbPath}
	if err := application.migrate(contextWithDomain(context.Background(), "localhost")); err != nil {
		t.Fatal(err)
	}
	if err := hostingandsupport.Migrate(context.Background(), rawDB); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`INSERT INTO server_managers(domain,email,role,scope_domain,created_at) VALUES(?,?,?,?,?)`, "localhost", "admin@localhost", "owner", "*", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if !application.isServerManagerEmail(context.Background(), "127.0.0.1", "admin@localhost") {
		t.Fatal("loopback request domain was not accepted for localhost owner")
	}
	if !sameHostingAndSupportDomain("localhost", "[::1]:18080") {
		t.Fatal("IPv6 loopback was not normalized to localhost")
	}
	if _, err := rawDB.Exec(`DELETE FROM server_managers`); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`INSERT INTO server_managers(domain,email,role,scope_domain,created_at) VALUES(?,?,?,?,?)`, "example.com", "owner@example.com", "owner", "*", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if !application.isServerManagerEmail(context.Background(), "example.com", "owner@example.com") {
		t.Fatal("unbound owner domain was not treated as effective localhost")
	}
	if application.isServerManagerEmail(context.Background(), "example.com", "other@example.com") {
		t.Fatal("non-owner admin was accepted for effective localhost")
	}
	menuScript := buildContextMenuScript(true, true, false, false, true, "/", "example.com", 0, 0, "", translationsForLanguageCode("ru"))
	if !strings.Contains(menuScript, "?expenses") {
		t.Fatal("server manager menu does not contain expenses link")
	}
	if !strings.Contains(menuScript, "Сервер и расходы") {
		t.Fatal("regular server menu does not use the singular expenses title")
	}
	centralMenuScript := buildContextMenuScript(true, true, false, false, true, "/", "sitebrush.com", 0, 0, "", translationsForLanguageCode("ru"))
	if !strings.Contains(centralMenuScript, "Серверы и расходы") {
		t.Fatal("sitebrush.com menu does not use the plural expenses title")
	}
}

func TestHostingSnapshotProcessNameIsSanitized(t *testing.T) {
	processName := sanitizeHostingSnapshotProcessName("/usr/local/bin/sitebrush\n--token secret")
	if processName != "sitebrush" {
		t.Fatalf("processName = %q", processName)
	}
	longName := sanitizeHostingSnapshotProcessName(strings.Repeat("a", 100))
	if len(longName) != 80 {
		t.Fatalf("longName length = %d, want 80", len(longName))
	}
}

func TestSiteQuotaMenuRenderingIsCompact(t *testing.T) {
	rows := []siteQuotaRow{
		{
			Domain:       "example.com",
			Aliases:      []string{"www.example.com", "shop.example.com"},
			UsedBytes:    2 * 1024 * 1024 * 1024,
			LimitBytes:   3 * 1024 * 1024 * 1024,
			FilesPath:    "/private/tmp/sitebrush/storage/sites/example.com",
			DatabasePath: "/private/tmp/sitebrush/storage/sites/example.com.db",
		},
	}

	var output bytes.Buffer
	printSiteQuotaRows(&output, "/private/tmp/sitebrush", "/private/tmp/sitebrush/storage/db/sitebrush.db", rows, 0, siteQuotaTerminalLayout{width: 72, height: 4})

	rendered := output.String()
	if !strings.Contains(rendered, "+") || !strings.Contains(rendered, "|") {
		t.Fatalf("compact menu missing frame: %s", rendered)
	}
	for _, line := range strings.Split(strings.TrimSpace(rendered), "\n") {
		if len(line) > 72 {
			t.Fatalf("compact menu line exceeds width: %d > 72 in %q", len(line), line)
		}
	}
	if strings.Contains(rendered, "quota:   ") {
		t.Fatalf("compact menu should not use legacy detail layout: %s", rendered)
	}
	if strings.Contains(rendered, "dir:") {
		t.Fatalf("compact menu should not render long paths: %s", rendered)
	}
}

func TestSiteQuotaMenuRenderingUsesRawTerminalNewlines(t *testing.T) {
	rows := []siteQuotaRow{
		{
			Domain:     "localhost",
			UsedBytes:  1024 * 1024,
			LimitBytes: 1024 * 1024 * 1024,
			FilesPath:  "/private/tmp/sitebrush/storage/files/localhost",
		},
	}

	var output bytes.Buffer
	printSiteQuotaRows(&output, "/private/tmp/sitebrush", "/private/tmp/sitebrush/storage/db/sitebrush.db", rows, 0, siteQuotaTerminalLayout{width: 60, height: 5, newline: "\r\n"})

	rendered := output.String()
	if !strings.Contains(rendered, "\r\n") {
		t.Fatalf("raw terminal menu should use CRLF line endings: %q", rendered)
	}
	if strings.Contains(rendered, "\n|") && !strings.Contains(rendered, "\r\n|") {
		t.Fatalf("raw terminal menu emitted LF-only content lines: %q", rendered)
	}
}

func TestQuotaRecommendationShowsExhaustedLimit(t *testing.T) {
	row := siteQuotaRow{UsedBytes: 2 * 1024 * 1024 * 1024, LimitBytes: 1024 * 1024 * 1024}
	if label, _ := siteQuotaQuotaState(row); label != "quota:full" {
		t.Fatalf("quota state = %q, want quota:full", label)
	}
	if recommendation := quotaRecommendation(row); !strings.Contains(recommendation, "3gb") {
		t.Fatalf("recommendation = %q, want rounded quota hint", recommendation)
	}
}

func TestLoginReturnPathDefaultsToCurrentPageWithoutAutoVisual(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	if returnPath := loginReturnPathOrDefault(request); returnPath != "/docs" {
		t.Fatalf("return path = %q, want %q", returnPath, "/docs")
	}
}

func TestRegistrationRedirectUsesSelfSignedTLSPort(t *testing.T) {
	application := &App{selfSignedTLSPort: 9899}
	request := httptest.NewRequest(http.MethodGet, "http://code.example:9898/?register", nil)
	response := httptest.NewRecorder()

	application.route(response, request)

	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTemporaryRedirect)
	}
	if location := response.Header().Get("Location"); location != "https://code.example:9899/?register" {
		t.Fatalf("location = %q", location)
	}
}

func TestSelfSignedHTTPSRedirectPreservesIPv6Host(t *testing.T) {
	application := &App{selfSignedTLSPort: 9900}
	request := httptest.NewRequest(http.MethodGet, "http://[::1]:9898/?register", nil)
	request.Host = "[2001:db8::1]:9898"
	response := httptest.NewRecorder()

	application.redirectHTTPS(response, request, http.StatusTemporaryRedirect)

	if location := response.Header().Get("Location"); location != "https://[2001:db8::1]:9900/?register" {
		t.Fatalf("location = %q", location)
	}
}

func TestProtectedControllerRedirectsToLoginAndPreservesController(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	redirectURL, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if redirectURL.Path != "/docs" {
		t.Fatalf("redirect path = %q, want %q", redirectURL.Path, "/docs")
	}
	if _, hasLogin := redirectURL.Query()["login"]; !hasLogin {
		t.Fatalf("redirect query missing login flag: %q", redirectURL.RawQuery)
	}
	if returnPath := redirectURL.Query().Get("return_path"); returnPath != "/docs?files=" {
		t.Fatalf("return_path = %q, want %q", returnPath, "/docs?files=")
	}
}

func TestLoginPostRedirectsBackToRequestedController(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "old")
	form.Set("return_path", "/docs?settings=")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	application.login(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/docs?settings=" {
		t.Fatalf("location = %q, want %q", location, "/docs?settings=")
	}
}

func TestRegisterRequiresEmailConfirmationBeforeCreatingAdmin(t *testing.T) {
	withEmailSPFAllowed(t)
	application, rawDB := newTestApplication(t)
	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?register", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	registrationBody := response.Body.String()
	for _, expectedFragment := range []string{"Проверьте почту", "admin@example.com", "sitebrush@localhost"} {
		if !strings.Contains(registrationBody, expectedFragment) {
			t.Fatalf("confirmation page missing %q in %s", expectedFragment, registrationBody)
		}
	}
	for _, forbiddenFragment := range []string{`name="email"`, `name="password"`, `action="?register"`} {
		if strings.Contains(registrationBody, forbiddenFragment) {
			t.Fatalf("confirmation page still contains registration form %q in %s", forbiddenFragment, registrationBody)
		}
	}
	var pendingToken string
	select {
	case mailJob := <-application.emailDelivery:
		if mailJob.Message.To != "admin@example.com" {
			t.Fatalf("confirmation recipient = %q", mailJob.Message.To)
		}
		if !strings.Contains(mailJob.Message.Subject, "Подтвердите email") || !strings.Contains(mailJob.Message.Body, "Для подтверждения email") {
			t.Fatalf("confirmation email is not Russian: %#v", mailJob.Message)
		}
		if !strings.Contains(mailJob.Message.HTMLBody, "Подтвердить email и войти") || !strings.Contains(mailJob.Message.HTMLBody, `href="http://localhost:8080/?email_confirm=`) {
			t.Fatalf("confirmation email has no localized HTML action: %#v", mailJob.Message)
		}
		pendingToken = confirmationTokenFromBody(t, mailJob.Message.Body)
	default:
		t.Fatal("registration did not enqueue confirmation email")
	}
	var userCount int
	_ = rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain=?`, "localhost").Scan(&userCount)
	if userCount != 0 {
		t.Fatalf("user count before confirmation = %d, want 0", userCount)
	}

	confirmRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?email_confirm="+url.QueryEscape(pendingToken), nil)
	confirmResponse := httptest.NewRecorder()
	application.route(confirmResponse, confirmRequest)
	if confirmResponse.Code != http.StatusFound {
		t.Fatalf("confirm status = %d, body=%q", confirmResponse.Code, confirmResponse.Body.String())
	}
	_ = rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND password=? AND is_admin=1`, "localhost", "admin@example.com", "secret").Scan(&userCount)
	if userCount != 1 {
		t.Fatalf("confirmed admin count = %d, want 1", userCount)
	}
	if len(confirmResponse.Result().Cookies()) == 0 {
		t.Fatal("confirmation did not create session")
	}
}

func TestSetupConfirmationOffersKnownWebmailWithoutRegistrationForm(t *testing.T) {
	application := &App{}
	request := httptest.NewRequest(http.MethodGet, "https://example.com/?register", nil)
	request = request.WithContext(contextWithDomain(request.Context(), "example.com"))
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.renderSetupConfirmationPage(response, request, "example.com", "administrator@gmail.com")
	body := response.Body.String()
	for _, expectedFragment := range []string{
		`lang="ru"`,
		`href="https://mail.google.com/"`,
		`administrator@gmail.com`,
		`Открыть Gmail`,
		`папки «Спам»`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("confirmation page missing %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, `name="password"`) || strings.Contains(body, `action="?register"`) {
		t.Fatalf("confirmation page contains registration form: %s", body)
	}
}

func TestWebmailProviderRecognition(t *testing.T) {
	testCases := []struct {
		address string
		name    string
		url     string
	}{
		{address: "person@gmail.com", name: "Gmail", url: "https://mail.google.com/"},
		{address: "person@hotmail.com", name: "Outlook", url: "https://outlook.live.com/mail/"},
		{address: "person@icloud.com", name: "iCloud Mail", url: "https://www.icloud.com/mail/"},
		{address: "person@mail.ru", name: "Mail.ru", url: "https://e.mail.ru/inbox/"},
		{address: "person@yandex.kz", name: "Yandex Mail", url: "https://mail.yandex.com/"},
		{address: "person@proton.me", name: "Proton Mail", url: "https://mail.proton.me/"},
		{address: "person@company.example", name: "", url: ""},
	}
	for _, testCase := range testCases {
		provider := webmailProviderForAddress(testCase.address)
		if provider.Name != testCase.name || provider.URL != testCase.url {
			t.Errorf("provider for %s = %#v", testCase.address, provider)
		}
	}
}

func TestConfirmationHTMLMailIsLocalizedForEveryInterfaceLanguage(t *testing.T) {
	for languageCode := range translationCatalog {
		htmlBody := emailHTMLBodyForServiceMail(languageCode, "email_confirm", "example.com", "https://example.com/?email_confirm=secret")
		if htmlBody == "" || !strings.Contains(htmlBody, `href="https://example.com/?email_confirm=secret"`) || !strings.Contains(htmlBody, confirmationEmailButtonLabel(languageCode)) {
			t.Errorf("localized HTML confirmation is incomplete for %s: %s", languageCode, htmlBody)
		}
	}
}

func TestRegistrationConfirmationSurvivesProcessMemoryRestart(t *testing.T) {
	storagePath := t.TempDir()
	storageRealRoot, err := prepareStorageJailRoot(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(storagePath, "storage", "db", "sitebrush.db")
	if err := ensureParentDir(databasePath); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := startServerControlDatabaseDispatcher(databasePath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dispatcher.Close)

	firstMemoryContext, stopFirstMemory := context.WithCancel(context.Background())
	application := &App{
		storagePath:               storagePath,
		storageRealRoot:           storageRealRoot,
		dbPath:                    databasePath,
		controlDatabase:           dispatcher,
		registrationConfirmations: startEmailConfirmationMemoryWorker(firstMemoryContext),
	}
	confirmation := EmailConfirmation{
		Token:        "restart-safe-token",
		Domain:       "example.com",
		Action:       "register",
		Email:        "admin@example.com",
		Password:     "secret",
		ReturnPath:   "/docs",
		LanguageCode: "en",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	if err := application.saveRegistrationConfirmation(context.Background(), confirmation); err != nil {
		t.Fatal(err)
	}
	stopFirstMemory()

	secondMemoryContext, stopSecondMemory := context.WithCancel(context.Background())
	t.Cleanup(stopSecondMemory)
	application.registrationConfirmations = startEmailConfirmationMemoryWorker(secondMemoryContext)
	restored, found := application.registrationConfirmationByToken(context.Background(), confirmation.Token)
	if !found || restored != confirmation {
		t.Fatalf("restored confirmation = %#v, found=%t", restored, found)
	}
	application.deleteRegistrationConfirmation(context.Background(), confirmation.Token)
	stopSecondMemory()

	thirdMemoryContext, stopThirdMemory := context.WithCancel(context.Background())
	t.Cleanup(stopThirdMemory)
	application.registrationConfirmations = startEmailConfirmationMemoryWorker(thirdMemoryContext)
	if _, found := application.registrationConfirmationByToken(context.Background(), confirmation.Token); found {
		t.Fatal("deleted confirmation was restored")
	}
}

func TestRegisterRejectsUnverifiedDomainBeforeCreatingSiteDatabase(t *testing.T) {
	const domain = "fake.example"
	t.Setenv("SITEBRUSH_SMTP_FROM", "SiteBrush <sitebrush@sender.example>")
	withRegistrationDNS(t, domain, false)
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, migrateDomain string) error {
		application := &App{db: rawDB, storagePath: storagePath, dbPath: dbPath, grabTracker: newGrabProgressTracker()}
		return application.migrate(contextWithDomain(migrationCtx, migrateDomain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)
	application := &App{
		db:                        router,
		siteDatabaseRouter:        router,
		storagePath:               storagePath,
		dbPath:                    dbPath,
		grabTracker:               newGrabProgressTracker(),
		registrationConfirmations: startEmailConfirmationMemoryWorker(context.Background()),
		emailDelivery:             make(chan mailout.DeliveryJob, 1),
	}
	form := url.Values{}
	form.Set("email", "admin@fake.example")
	form.Set("password", "secret")
	request := httptest.NewRequest(http.MethodPost, "https://"+domain+"/?register", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	siteDatabasePath := filepath.Join(siteDatabaseDir, domainStorageName(domain)+".db")
	if _, err := os.Stat(siteDatabasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("site database stat err = %v, want not exist", err)
	}
	select {
	case mailJob := <-application.emailDelivery:
		t.Fatalf("registration email was sent before DNS verification: %#v", mailJob.Message)
	default:
	}
}

func TestRegisterCreatesSiteDatabaseOnlyAfterConfirmedVerifiedDomain(t *testing.T) {
	const domain = "verified.example"
	withRegistrationDNS(t, domain, true)
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, migrateDomain string) error {
		application := &App{db: rawDB, storagePath: storagePath, dbPath: dbPath, grabTracker: newGrabProgressTracker()}
		return application.migrate(contextWithDomain(migrationCtx, migrateDomain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)
	application := &App{
		db:                        router,
		siteDatabaseRouter:        router,
		storagePath:               storagePath,
		dbPath:                    dbPath,
		grabTracker:               newGrabProgressTracker(),
		registrationConfirmations: startEmailConfirmationMemoryWorker(context.Background()),
		emailDelivery:             make(chan mailout.DeliveryJob, 1),
	}
	form := url.Values{}
	form.Set("email", "admin@verified.example")
	form.Set("password", "secret")
	request := httptest.NewRequest(http.MethodPost, "https://"+domain+"/?register", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	var pendingToken string
	select {
	case mailJob := <-application.emailDelivery:
		pendingToken = confirmationTokenFromBody(t, mailJob.Message.Body)
	default:
		t.Fatal("registration did not enqueue confirmation email")
	}
	siteDatabasePath := filepath.Join(siteDatabaseDir, domainStorageName(domain)+".db")
	if _, err := os.Stat(siteDatabasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("site database before confirmation stat err = %v, want not exist", err)
	}

	confirmRequest := httptest.NewRequest(http.MethodGet, "https://"+domain+"/?email_confirm="+url.QueryEscape(pendingToken), nil)
	confirmResponse := httptest.NewRecorder()
	application.route(confirmResponse, confirmRequest)
	if confirmResponse.Code != http.StatusFound {
		t.Fatalf("confirm status = %d, body=%q", confirmResponse.Code, confirmResponse.Body.String())
	}
	if _, err := os.Stat(siteDatabasePath); err != nil {
		t.Fatalf("site database after confirmation stat err = %v", err)
	}
	var userCount int
	if err := router.QueryRowContext(contextWithDomain(context.Background(), domain), `SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND is_admin=1`, domain, "admin@verified.example").Scan(&userCount); err != nil {
		t.Fatalf("read confirmed admin: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("confirmed admin count = %d, want 1", userCount)
	}
}

func TestDisabledAutoRegistrationShowsOnlySiteRequestForm(t *testing.T) {
	application, _ := newTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", false)
	defer controlDB.Close()

	request := httptest.NewRequest(http.MethodGet, "https://newsite.example/?register", nil)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{
		`action="?site_request"`,
		`name="name"`,
		`name="phone"`,
		`name="plan_id"`,
		`siteRequestPlanModal`,
		`Отправить заявку`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("disabled registration page missing %q in %s", expectedFragment, body)
		}
	}
	for _, forbiddenFragment := range []string{
		`name="password"`,
		`Первый посетитель становится администратором`,
		`setup_first_visitor`,
	} {
		if strings.Contains(body, forbiddenFragment) {
			t.Fatalf("disabled registration page still exposes bootstrap registration fragment %q in %s", forbiddenFragment, body)
		}
	}
}

func TestSiteRequestStoresApplicantWithoutCreatingAdmin(t *testing.T) {
	application, rawDB := newTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", false)
	defer controlDB.Close()
	store := hostingandsupport.Store{DB: controlDB}
	plan, found := store.DefaultPlan(context.Background())
	if !found {
		t.Fatal("default plan missing")
	}

	form := url.Values{}
	form.Set("name", "Customer Name")
	form.Set("email", "customer@example.com")
	form.Set("phone", "+1 555 0100")
	form.Set("plan_id", strconv.Itoa(plan.ID))
	request := httptest.NewRequest(http.MethodPost, "http://customer.example/?site_request", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Заявка отправлена владельцу сервера") {
		t.Fatalf("request page did not confirm submission: %s", response.Body.String())
	}
	var userCount int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain=?`, "customer.example").Scan(&userCount); err != nil {
		t.Fatalf("read user count: %v", err)
	}
	if userCount != 0 {
		t.Fatalf("disabled registration created %d admins", userCount)
	}
	var requestCount int
	if err := controlDB.QueryRow(`SELECT COUNT(1) FROM site_registration_requests WHERE domain=? AND name=? AND email=? AND phone=? AND plan_id=? AND status='pending'`,
		"customer.example", "Customer Name", "customer@example.com", "+1 555 0100", plan.ID).Scan(&requestCount); err != nil {
		t.Fatalf("read request count: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("stored request count = %d, want 1", requestCount)
	}
	select {
	case mailJob := <-application.emailDelivery:
		if mailJob.Message.To != "owner@example.com" || !strings.Contains(mailJob.Message.Body, "customer.example") {
			t.Fatalf("owner notification = %#v", mailJob.Message)
		}
	default:
		t.Fatal("site request did not enqueue owner notification")
	}
}

func TestBillingPlanCanBeEditedWithSiteAndAnalyticsLimits(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "owner@example.com", "old"); err != nil {
		t.Fatalf("insert owner admin: %v", err)
	}
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", true)
	defer controlDB.Close()

	createForm := url.Values{}
	createForm.Set("billing_action", "save_plan")
	createForm.Set("name", "Pro")
	createForm.Set("quota", "10gb")
	createForm.Set("site_limit", "3")
	createForm.Set("analytics_report_limit", "12")
	createForm.Set("price", "19")
	createForm.Set("currency", "USD")
	createForm.Set("billing_period", "monthly")
	createRequest := httptest.NewRequest(http.MethodPost, "http://localhost/?billing", strings.NewReader(createForm.Encode()))
	createRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	createResponse := httptest.NewRecorder()
	application.route(createResponse, createRequest)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%q", createResponse.Code, createResponse.Body.String())
	}

	var planID int
	if err := controlDB.QueryRow(`SELECT id FROM site_service_plans WHERE name='Pro'`).Scan(&planID); err != nil {
		t.Fatalf("read created plan: %v", err)
	}
	editForm := url.Values{}
	editForm.Set("billing_action", "save_plan")
	editForm.Set("plan_id", strconv.Itoa(planID))
	editForm.Set("name", "Business")
	editForm.Set("quota", "20gb")
	editForm.Set("site_limit", "5")
	editForm.Set("analytics_report_limit", "30")
	editForm.Set("price", "49")
	editForm.Set("currency", "EUR")
	editForm.Set("billing_period", "yearly")
	editForm.Set("is_default", "1")
	editRequest := httptest.NewRequest(http.MethodPost, "http://localhost/?billing", strings.NewReader(editForm.Encode()))
	editRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	editRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	editResponse := httptest.NewRecorder()
	application.route(editResponse, editRequest)
	if editResponse.Code != http.StatusOK {
		t.Fatalf("edit status = %d, body=%q", editResponse.Code, editResponse.Body.String())
	}

	var planName string
	var quotaBytes int64
	var siteLimit int
	var analyticsReportLimit int
	var price string
	var currency string
	var billingPeriod string
	var isDefault int
	if err := controlDB.QueryRow(`SELECT name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default FROM site_service_plans WHERE id=?`, planID).Scan(&planName, &quotaBytes, &siteLimit, &analyticsReportLimit, &price, &currency, &billingPeriod, &isDefault); err != nil {
		t.Fatalf("read edited plan: %v", err)
	}
	if planName != "Business" || quotaBytes != 20*1024*1024*1024 || siteLimit != 5 || analyticsReportLimit != 30 || price != "49" || currency != "EUR" || billingPeriod != "yearly" || isDefault != 1 {
		t.Fatalf("edited plan = name:%q quota:%d sites:%d reports:%d price:%q currency:%q period:%q default:%d", planName, quotaBytes, siteLimit, analyticsReportLimit, price, currency, billingPeriod, isDefault)
	}

	viewRequest := httptest.NewRequest(http.MethodGet, "http://localhost/?billing", nil)
	viewRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	viewResponse := httptest.NewRecorder()
	application.route(viewResponse, viewRequest)
	if viewResponse.Code != http.StatusOK {
		t.Fatalf("view status = %d, body=%q", viewResponse.Code, viewResponse.Body.String())
	}
	body := viewResponse.Body.String()
	if !strings.Contains(body, `class="expense-simple-page"`) {
		t.Fatal("expenses page did not render the simplified view")
	}
	if strings.Contains(body, `id="billingPlanEditModal`+strconv.Itoa(planID)+`"`) {
		t.Fatal("expenses page rendered the unrelated plan editor")
	}
}

func TestBillingMigrationAddsPlanLimitColumns(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "hostingandsupport.db")
	controlDB, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDB.Close()
	if _, err := controlDB.Exec(`CREATE TABLE schema_migrations(component TEXT PRIMARY KEY,version INTEGER,updated_at TEXT)`); err != nil {
		t.Fatalf("create schema migrations: %v", err)
	}
	if _, err := controlDB.Exec(`INSERT INTO schema_migrations(component,version,updated_at) VALUES('billing',4,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed schema migration: %v", err)
	}
	if _, err := controlDB.Exec(`CREATE TABLE site_service_plans(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT UNIQUE,quota_bytes INTEGER,price TEXT,currency TEXT,billing_period TEXT,is_default INTEGER DEFAULT 0,created_at TEXT,updated_at TEXT)`); err != nil {
		t.Fatalf("create old plans table: %v", err)
	}
	if _, err := controlDB.Exec(`INSERT INTO site_service_plans(name,quota_bytes,price,currency,billing_period,is_default,created_at,updated_at) VALUES('Legacy',1073741824,'5','USD','monthly',0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed old plan: %v", err)
	}
	if err := hostingandsupport.Migrate(context.Background(), controlDB); err != nil {
		t.Fatalf("migrate billing: %v", err)
	}
	var siteLimit int
	var analyticsReportLimit int
	if err := controlDB.QueryRow(`SELECT site_limit,analytics_report_limit FROM site_service_plans WHERE name='Legacy'`).Scan(&siteLimit, &analyticsReportLimit); err != nil {
		t.Fatalf("read migrated plan limits: %v", err)
	}
	if siteLimit != 1 || analyticsReportLimit != 0 {
		t.Fatalf("migrated limits = sites:%d reports:%d, want sites:1 reports:0", siteLimit, analyticsReportLimit)
	}
}

func TestApproveSiteRequestCreatesSiteAndEmailsApplicant(t *testing.T) {
	application, _ := newTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", false)
	defer controlDB.Close()
	store := hostingandsupport.Store{DB: controlDB}
	plan, found := store.DefaultPlan(context.Background())
	if !found {
		t.Fatal("default plan missing")
	}
	if err := store.CreateSiteRequest(context.Background(), "approved.example", "Applicant", "applicant@example.com", "+1 555 0101", plan.ID); err != nil {
		t.Fatalf("create site request: %v", err)
	}
	siteRequests := store.SiteRequests(context.Background())
	if len(siteRequests) != 1 {
		t.Fatalf("site requests = %#v", siteRequests)
	}

	form := url.Values{}
	form.Set("request_id", strconv.Itoa(siteRequests[0].ID))
	form.Set("owner_message", "Welcome")
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?billing", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status := application.approveSiteRegistrationRequestFromForm(request)
	if !strings.Contains(status, "активирован") {
		t.Fatalf("approve status = %q", status)
	}
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName("approved.example")+".db")
	siteDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()
	var adminCount int
	if err := siteDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND is_admin=1`, "approved.example", "applicant@example.com").Scan(&adminCount); err != nil {
		t.Fatalf("read approved admin count: %v", err)
	}
	if adminCount != 1 {
		t.Fatalf("approved admin count = %d, want 1", adminCount)
	}
	var assignmentCount int
	if err := controlDB.QueryRow(`SELECT COUNT(1) FROM site_service_assignments WHERE domain=? AND plan_id=?`, "approved.example", plan.ID).Scan(&assignmentCount); err != nil {
		t.Fatalf("read assignment count: %v", err)
	}
	if assignmentCount != 1 {
		t.Fatalf("assignment count = %d, want 1", assignmentCount)
	}
	select {
	case mailJob := <-application.emailDelivery:
		if mailJob.Message.To != "applicant@example.com" || !strings.Contains(mailJob.Message.Body, "Временный пароль") {
			t.Fatalf("applicant notification = %#v", mailJob.Message)
		}
	default:
		t.Fatal("approval did not enqueue applicant notification")
	}
}

func TestHostingAndSupportApprovedSiteRequestsAttachToSites(t *testing.T) {
	siteRequests := []hostingandsupport.SiteRequest{
		{ID: 1, Domain: "pending.example", Status: "pending"},
		{ID: 2, Domain: "Approved.Example", Status: "approved", Email: "customer@example.com"},
		{ID: 3, Domain: "rejected.example", Status: "rejected"},
	}
	pendingSiteRequests, approvedSiteRequestsByDomain := hostingAndSupportSplitSiteRequests(siteRequests)
	if len(pendingSiteRequests) != 1 || pendingSiteRequests[0].ID != 1 {
		t.Fatalf("pending requests = %#v, want request 1", pendingSiteRequests)
	}
	siteRows := []hostingandsupport.Site{
		{Domain: "approved.example"},
		{Domain: "other.example"},
	}
	hostingAndSupportAttachApprovedSiteRequests(siteRows, approvedSiteRequestsByDomain)
	if !siteRows[0].HasSiteRequest || siteRows[0].SiteRequest.ID != 2 {
		t.Fatalf("approved request was not attached to site: %#v", siteRows[0])
	}
	if siteRows[1].HasSiteRequest {
		t.Fatalf("unmatched site received request: %#v", siteRows[1])
	}
}

func TestDemoSiteVisitorGetsEditorSessionAndCleanupDeletesSite(t *testing.T) {
	sourceURL := "https://source.example/"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL: {contentType: "text/html; charset=utf-8", body: `<!doctype html><html><head><title>External Demo</title></head><body><h1>External Demo</h1></body></html>`},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	application := newRouterTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer controlDB.Close()
	store := demo.Store{DB: controlDB}
	if err := store.SaveSettings(context.Background(), "demo.example", sourceURL, false, true); err != nil {
		t.Fatalf("save demo settings: %v", err)
	}
	demoSettings := demo.Settings{Domain: "demo.example", SourceURL: sourceURL, Enabled: true}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), demoSettings, false, ""); err != nil {
		t.Fatalf("prepare demo site: %v", err)
	}

	// A partial or old snapshot must not mark the demo ready without a usable page.
	demoContext := contextWithDomain(context.Background(), "demo.example")
	if _, err := application.db.ExecContext(demoContext, `DELETE FROM pages WHERE domain=? AND path='/'`, "demo.example"); err != nil {
		t.Fatalf("delete demo landing page: %v", err)
	}
	orphanMissingHTML := `<!doctype html><html id="SiteBrush"><body><main class="container missing-page"><div class="missing-page-alert">Not found</div></main></body></html>`
	if _, err := application.db.ExecContext(demoContext, `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "demo.example", "/orphan", "Orphan", orphanMissingHTML); err != nil {
		t.Fatalf("create orphan demo page: %v", err)
	}
	if err := application.createDemoSiteSnapshot(context.Background(), "demo.example"); err != nil {
		t.Fatalf("create partial demo snapshot: %v", err)
	}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), demoSettings, false, ""); err != nil {
		t.Fatalf("repair demo landing page: %v", err)
	}
	if !application.demoSiteHasLandingPage(context.Background(), "demo.example") {
		t.Fatal("demo landing page was not repaired")
	}

	request := httptest.NewRequest(http.MethodGet, "https://demo.example/", nil)
	request = request.WithContext(contextWithDomain(request.Context(), "demo.example"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("demo start status = %d, body=%q", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/" {
		t.Fatalf("demo redirect = %q, want /", location)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("demo start did not set a session cookie")
	}
	var sessionCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "sitebrush_session" {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatalf("demo session cookie missing in %#v", cookies)
	}

	landingRequest := httptest.NewRequest(http.MethodGet, "https://demo.example/", nil)
	landingRequest = landingRequest.WithContext(contextWithDomain(landingRequest.Context(), "demo.example"))
	landingRequest.AddCookie(sessionCookie)
	landingResponse := httptest.NewRecorder()
	application.route(landingResponse, landingRequest)
	if landingResponse.Code != http.StatusOK {
		t.Fatalf("demo landing status = %d, body=%q", landingResponse.Code, landingResponse.Body.String())
	}
	if !strings.Contains(landingResponse.Body.String(), "External Demo") {
		t.Fatalf("demo landing content missing after redirect: %s", landingResponse.Body.String())
	}

	siteDatabasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName("demo.example")+".db")
	siteDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	var adminCount int
	if err := siteDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND is_admin=1`, "demo.example", "demo@demo.example").Scan(&adminCount); err != nil {
		t.Fatalf("read demo admin count: %v", err)
	}
	if adminCount != 1 {
		t.Fatalf("demo admin count = %d, want 1", adminCount)
	}
	var pageHTML string
	if err := siteDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path='/'`, "demo.example").Scan(&pageHTML); err != nil {
		t.Fatalf("read demo page: %v", err)
	}
	if !strings.Contains(pageHTML, "External Demo") {
		t.Fatalf("demo source content was not imported: %s", pageHTML)
	}
	if err := siteDB.Close(); err != nil {
		t.Fatalf("close demo db: %v", err)
	}

	logoutRequest := httptest.NewRequest(http.MethodGet, "https://demo.example/?logout", nil)
	logoutRequest = logoutRequest.WithContext(contextWithDomain(logoutRequest.Context(), "demo.example"))
	logoutRequest.AddCookie(sessionCookie)
	logoutResponse := httptest.NewRecorder()
	application.route(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusFound {
		t.Fatalf("demo logout status = %d, body=%q", logoutResponse.Code, logoutResponse.Body.String())
	}
	var deletingSessions int
	if err := controlDB.QueryRow(`SELECT COUNT(1) FROM demo_site_sessions WHERE domain='demo.example' AND status='deleting' AND delete_after<>''`).Scan(&deletingSessions); err != nil {
		t.Fatalf("read deleting demo sessions: %v", err)
	}
	if deletingSessions != 1 {
		t.Fatalf("deleting demo sessions = %d, want 1", deletingSessions)
	}

	application.cleanupExpiredDemoSites(context.Background(), time.Now().Add(31*time.Minute))
	if _, err := os.Stat(siteDatabasePath); err != nil {
		t.Fatalf("demo site database stat err = %v, want restored site", err)
	}
	var remainingDemoSessions int
	if err := controlDB.QueryRow(`SELECT COUNT(1) FROM demo_site_sessions WHERE domain='demo.example'`).Scan(&remainingDemoSessions); err != nil {
		t.Fatalf("read remaining demo sessions: %v", err)
	}
	if remainingDemoSessions != 0 {
		t.Fatalf("remaining demo sessions = %d, want 0", remainingDemoSessions)
	}
	if restoredAt := store.Settings(context.Background()).LastRestoredAt; restoredAt == "" {
		t.Fatal("demo content reset time was not recorded")
	}
}

func TestDemoSiteSourceImportFailureKeepsUsableWelcomePage(t *testing.T) {
	sourceURL := "https://source.example/"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL: {statusCode: http.StatusBadGateway, contentType: "text/plain", body: "upstream failed"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	application := newRouterTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer controlDB.Close()
	store := demo.Store{DB: controlDB}
	if err := store.SaveSettings(context.Background(), "demo-fail.example", sourceURL, false, true); err != nil {
		t.Fatalf("save demo settings: %v", err)
	}
	demoSettings := demo.Settings{Domain: "demo-fail.example", SourceURL: sourceURL, Enabled: true}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), demoSettings, false, ""); err == nil {
		t.Fatal("demo preparation unexpectedly succeeded")
	}

	request := httptest.NewRequest(http.MethodGet, "https://demo-fail.example/", nil)
	request = request.WithContext(contextWithDomain(request.Context(), "demo-fail.example"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("demo start status = %d, body=%q", response.Code, response.Body.String())
	}

	siteDatabasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName("demo-fail.example")+".db")
	siteDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()
	var pageHTML string
	if err := siteDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path='/'`, "demo-fail.example").Scan(&pageHTML); err != nil {
		t.Fatalf("read demo landing page: %v", err)
	}
	if !strings.Contains(pageHTML, "Sitebrush Demo") {
		t.Fatalf("failed source import did not retain a usable landing page: %s", pageHTML)
	}
}

func TestDemoSiteLandingPathSkipsImportedSitebrush404(t *testing.T) {
	application, rawDB := newTestApplication(t)
	missingHTML := `<!doctype html><html id="SiteBrush"><body><main class="container missing-page"><div class="missing-page-alert">Not found</div></main></body></html>`
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"demo.example", "/", "Missing", missingHTML,
		"demo.example", "/products/example", "Product", "<h1>Product</h1>",
	); err != nil {
		t.Fatalf("insert demo pages: %v", err)
	}

	landingPath, found := application.demoSiteLandingPath(context.Background(), "demo.example")
	if !found {
		t.Fatal("usable imported demo page was not found")
	}
	if landingPath != "/products/example" {
		t.Fatalf("demo landing path = %q, want /products/example", landingPath)
	}
}

func TestDownloadGrabSourceRejectsSitebrushSoft404(t *testing.T) {
	sourceURL := "https://source.example/"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL: {contentType: "text/html; charset=utf-8", body: `<!doctype html><html id="SiteBrush"><body><main class="container missing-page"><div class="missing-page-alert">Not found</div></main></body></html>`},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	if _, _, err := downloadGrabSourceHTMLWithResolvedURL(sourceURL, grabSourceOptions{}); err == nil {
		t.Fatal("SiteBrush soft 404 was accepted as source content")
	}
}

func TestBillingDemoGrabPreviewReportsDownloadSize(t *testing.T) {
	sourceURL := "https://source.example/"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL:                         {contentType: "text/html; charset=utf-8", body: `<!doctype html><html><body><img src="/logo.png"><h1>Preview Demo</h1></body></html>`},
			"https://source.example/logo.png": {contentType: "image/png", body: strings.Repeat("P", 90)},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "owner@example.com", "old"); err != nil {
		t.Fatalf("insert owner user: %v", err)
	}
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", true)
	defer controlDB.Close()
	form := url.Values{}
	form.Set("demo_site_domain", "demo-preview.example")
	form.Set("demo_site_source_url", sourceURL)
	previewRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?demo_grab_preview", strings.NewReader(form.Encode()))
	previewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	previewRequest.Header.Set("Accept", "application/json")
	previewRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	previewResponse := httptest.NewRecorder()
	application.route(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%q", previewResponse.Code, previewResponse.Body.String())
	}
	var previewPayload grabPreviewResponse
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewPayload); err != nil {
		t.Fatalf("decode preview payload: %v", err)
	}
	if previewPayload.PageCount != 1 {
		t.Fatalf("page count = %d, want 1", previewPayload.PageCount)
	}
	if previewPayload.SelectedResourceBytes < 90 {
		t.Fatalf("selected resource bytes = %d, want at least 90", previewPayload.SelectedResourceBytes)
	}
	if previewPayload.SourceURL != sourceURL {
		t.Fatalf("source url = %q, want %q", previewPayload.SourceURL, sourceURL)
	}
}

func TestBillingDemoGrabRefreshUpdatesLocalSnapshot(t *testing.T) {
	sourceURL := "https://source.example/"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL: {contentType: "text/html; charset=utf-8", body: `<!doctype html><html><body><h1>Refresh Demo</h1></body></html>`},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "owner@example.com", "old"); err != nil {
		t.Fatalf("insert owner user: %v", err)
	}
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", true)
	defer controlDB.Close()
	form := url.Values{}
	form.Set("demo_site_domain", "demo-refresh.example")
	form.Set("demo_site_source_url", sourceURL)
	form.Set("progress_token", "refresh-test-token")
	refreshRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?demo_grab_refresh", strings.NewReader(form.Encode()))
	refreshRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshRequest.Header.Set("Accept", "application/json")
	refreshRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	refreshResponse := httptest.NewRecorder()
	application.route(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body=%q", refreshResponse.Code, refreshResponse.Body.String())
	}

	var pageHTML string
	if err := application.db.QueryRowContext(contextWithDomain(context.Background(), "demo-refresh.example"), `SELECT html FROM pages WHERE domain=? AND path='/'`, "demo-refresh.example").Scan(&pageHTML); err != nil {
		t.Fatalf("read demo page: %v", err)
	}
	if !strings.Contains(pageHTML, "Refresh Demo") {
		t.Fatalf("demo refresh content was not imported: %s", pageHTML)
	}
	if _, err := os.Stat(application.demoSiteSnapshotPath("demo-refresh.example")); err != nil {
		t.Fatalf("demo snapshot stat err = %v", err)
	}
	demoStatus := application.demoSiteStatusView(context.Background(), demo.Settings{Domain: "demo-refresh.example"})
	if !demoStatus.SnapshotAvailable || demoStatus.SnapshotCreatedAt == "" || demoStatus.SnapshotSize == "" {
		t.Fatalf("demo snapshot status is incomplete: %#v", demoStatus)
	}
}

func TestBillingDemoGrabRefreshRetryDownloadsOnlyFailedResources(t *testing.T) {
	sourceURL := "https://source.example/"
	resourceURL := "https://source.example/logo.png"
	sourceRequestCount := 0
	resourceShouldFail := true
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.String() {
			case sourceURL:
				sourceRequestCount++
				return fakeGrabResponse{contentType: "text/html; charset=utf-8", body: `<!doctype html><html><body><img src="/logo.png"><h1>Retry Demo</h1></body></html>`}.httpResponse(request), nil
			case resourceURL:
				if resourceShouldFail {
					return fakeGrabResponse{statusCode: http.StatusNotFound, body: "not found"}.httpResponse(request), nil
				}
				return fakeGrabResponse{contentType: "image/png", body: strings.Repeat("P", 90)}.httpResponse(request), nil
			default:
				return fakeGrabResponse{statusCode: http.StatusNotFound, body: "not found"}.httpResponse(request), nil
			}
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "owner@example.com", "old"); err != nil {
		t.Fatalf("insert owner user: %v", err)
	}
	controlDB := setupBillingOwnerForTest(t, application, "localhost", "owner@example.com", true)
	defer controlDB.Close()
	form := url.Values{}
	form.Set("demo_site_domain", "demo-retry.example")
	form.Set("demo_site_source_url", sourceURL)
	form.Set("progress_token", "refresh-retry-test-token")
	refreshRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?demo_grab_refresh", strings.NewReader(form.Encode()))
	refreshRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshRequest.Header.Set("Accept", "application/json")
	refreshRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	refreshResponse := httptest.NewRecorder()
	application.route(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body=%q", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshPayload map[string]any
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &refreshPayload); err != nil {
		t.Fatalf("decode refresh payload: %v", err)
	}
	if failedTotal := int(refreshPayload["failed_total"].(float64)); failedTotal != 1 {
		t.Fatalf("failed_total = %d, want 1", failedTotal)
	}

	resourceShouldFail = false
	retryForm := url.Values{}
	retryForm.Set("demo_site_domain", "demo-retry.example")
	retryForm.Set("demo_site_source_url", sourceURL)
	retryForm.Set("progress_token", "refresh-retry-test-token-2")
	retryForm.Add("demo_failed_resource_url", resourceURL)
	retryRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?demo_grab_refresh", strings.NewReader(retryForm.Encode()))
	retryRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	retryRequest.Header.Set("Accept", "application/json")
	retryRequest.AddCookie(newAdminSessionCookie(t, application, "owner@example.com"))
	retryResponse := httptest.NewRecorder()
	application.route(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body=%q", retryResponse.Code, retryResponse.Body.String())
	}
	if sourceRequestCount != 1 {
		t.Fatalf("source request count = %d, want 1", sourceRequestCount)
	}
	var pageHTML string
	if err := application.db.QueryRowContext(contextWithDomain(context.Background(), "demo-retry.example"), `SELECT html FROM pages WHERE domain=? AND path='/'`, "demo-retry.example").Scan(&pageHTML); err != nil {
		t.Fatalf("read demo page: %v", err)
	}
	if strings.Contains(pageHTML, resourceURL) {
		t.Fatalf("retried resource stayed external: %s", pageHTML)
	}
	if !strings.Contains(pageHTML, "/p/") {
		t.Fatalf("retried resource was not rewritten locally: %s", pageHTML)
	}
}

func TestActiveDemoSessionDoesNotBlockScheduledSiteRecreation(t *testing.T) {
	application := newRouterTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer controlDB.Close()
	store := demo.Store{DB: controlDB}
	if err := store.SaveSettings(context.Background(), "demo-active.example", "", false, true); err != nil {
		t.Fatalf("save demo settings: %v", err)
	}
	demoSettings := demo.Settings{Domain: "demo-active.example", Enabled: true}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), demoSettings, false, ""); err != nil {
		t.Fatalf("prepare demo site: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://demo-active.example/", nil)
	request = request.WithContext(contextWithDomain(request.Context(), "demo-active.example"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("demo start status = %d, body=%q", response.Code, response.Body.String())
	}

	siteDatabasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName("demo-active.example")+".db")
	if _, err := os.Stat(siteDatabasePath); err != nil {
		t.Fatalf("demo site database stat err = %v, want exists", err)
	}

	application.cleanupExpiredDemoSites(context.Background(), time.Now().Add(31*time.Minute))
	if _, err := os.Stat(siteDatabasePath); err != nil {
		t.Fatalf("demo site database stat err = %v, want restored site", err)
	}
	var remainingDemoSessions int
	if err := controlDB.QueryRow(`SELECT COUNT(1) FROM demo_site_sessions WHERE domain='demo-active.example'`).Scan(&remainingDemoSessions); err != nil {
		t.Fatalf("read remaining demo sessions: %v", err)
	}
	if remainingDemoSessions != 0 {
		t.Fatalf("remaining demo sessions = %d, want 0", remainingDemoSessions)
	}

	recreateRequest := httptest.NewRequest(http.MethodGet, "https://demo-active.example/", nil)
	recreateRequest = recreateRequest.WithContext(contextWithDomain(recreateRequest.Context(), "demo-active.example"))
	recreateResponse := httptest.NewRecorder()
	application.route(recreateResponse, recreateRequest)
	if recreateResponse.Code != http.StatusFound {
		t.Fatalf("demo recreate status = %d, body=%q", recreateResponse.Code, recreateResponse.Body.String())
	}
	if location := recreateResponse.Header().Get("Location"); location != "/" {
		t.Fatalf("demo recreate redirect = %q, want /", location)
	}
	if _, err := os.Stat(siteDatabasePath); err != nil {
		t.Fatalf("recreated demo site database stat err = %v, want exists", err)
	}
}

func newRouterTestApplication(t *testing.T) *App {
	t.Helper()
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, migrateDomain string) error {
		application := &App{db: rawDB, storagePath: storagePath, dbPath: dbPath, grabTracker: newGrabProgressTracker()}
		return application.migrate(contextWithDomain(migrationCtx, migrateDomain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)
	return &App{
		db:                 router,
		siteDatabaseRouter: router,
		storagePath:        storagePath,
		dbPath:             dbPath,
		grabTracker:        newGrabProgressTracker(),
		emailDelivery:      make(chan mailout.DeliveryJob, 4),
	}
}

func setupBillingOwnerForTest(t *testing.T, application *App, domain, email string, autoRegistrationEnabled bool) *sql.DB {
	t.Helper()
	controlDB, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	store := hostingandsupport.Store{DB: controlDB}
	if err := store.SetOwner(context.Background(), domain, email); err != nil {
		_ = controlDB.Close()
		t.Fatalf("set owner: %v", err)
	}
	if err := store.SaveSettings(context.Background(), autoRegistrationEnabled); err != nil {
		_ = controlDB.Close()
		t.Fatalf("save billing settings: %v", err)
	}
	return controlDB
}

func TestRecoverPageShowsSPFSetupBeforeEmailForm(t *testing.T) {
	t.Setenv("SITEBRUSH_SMTP_FROM", "SiteBrush <sitebrush@example.com>")
	previousTXTLookup := lookupTXTRecords
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	lookupTXTRecords = func(domain string) ([]string, error) {
		if domain != "example.com" {
			t.Fatalf("unexpected SPF lookup domain %q", domain)
		}
		return []string{"v=spf1 ip4:198.51.100.20 ~all"}, nil
	}
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain != "example.com" {
			t.Fatalf("unexpected IP lookup domain %q", domain)
		}
		return []net.IP{net.ParseIP("198.51.100.20")}, nil
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}
	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		return nil, nil
	}
	t.Cleanup(func() {
		lookupTXTRecords = previousTXTLookup
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	})

	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	getRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?recover", nil)
	getRequest.Header.Set("Accept-Language", "ru")
	getResponse := httptest.NewRecorder()
	application.route(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%q", getResponse.Code, getResponse.Body.String())
	}
	getBody := getResponse.Body.String()
	for _, expectedFragment := range []string{"Сначала настройте DNS", "Восстановление пароля через email", "SiteBrush будет отправлять письма с адреса sitebrush@example.com", "example.com. IN A 203.0.113.10", `example.com. IN TXT &#34;v=spf1 a mx ip4:203.0.113.10 ~all&#34;`, "Копировать"} {
		if !strings.Contains(getBody, expectedFragment) {
			t.Fatalf("recover page missing %q in %s", expectedFragment, getBody)
		}
	}
	for _, hiddenFragment := range []string{`name='email'`, `name="captcha"`, `?captcha`} {
		if strings.Contains(getBody, hiddenFragment) {
			t.Fatalf("recover page showed form fragment %q before SPF setup: %s", hiddenFragment, getBody)
		}
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("captcha", "1234")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?recover", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept-Language", "ru")
	request.AddCookie(&http.Cookie{Name: "sitebrush_captcha", Value: "1234"})
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{"Сначала настройте DNS", "example.com. IN A 203.0.113.10", `example.com. IN TXT &#34;v=spf1 a mx ip4:203.0.113.10 ~all&#34;`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("recover page missing %q in %s", expectedFragment, body)
		}
	}
	select {
	case mailJob := <-application.emailDelivery:
		t.Fatalf("recovery enqueued email despite invalid SPF: %#v", mailJob.Message)
	default:
	}
}

func TestRecoverPageShowsEmailFormAfterSPFSetup(t *testing.T) {
	withEmailSPFAllowed(t)
	application, _ := newTestApplication(t)
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?recover", nil)
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`name='email'`, `name="captcha"`, `?captcha`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("recover page missing form fragment %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "Сначала настройте DNS") {
		t.Fatalf("recover page showed SPF warning after valid SPF setup: %s", body)
	}
}

func TestSPFRecordAllowsExactAndCIDRServerIPs(t *testing.T) {
	if !spfRecordAllowsIP("v=spf1 ip4:203.0.113.10 ~all", net.ParseIP("203.0.113.10")) {
		t.Fatal("exact IPv4 SPF mechanism did not match")
	}
	if !spfRecordAllowsIP("v=spf1 ip4:203.0.113.0/24 ~all", net.ParseIP("203.0.113.10")) {
		t.Fatal("CIDR IPv4 SPF mechanism did not match")
	}
	if spfRecordAllowsIP("v=spf1 ip4:198.51.100.0/24 ~all", net.ParseIP("203.0.113.10")) {
		t.Fatal("unrelated IPv4 SPF mechanism matched")
	}
	if spfRecordsAllowAnyServerIP([]string{"v=spf1 ip4:203.0.113.10 ~all", "v=spf1 ip4:203.0.113.10 ~all"}, []net.IP{net.ParseIP("203.0.113.10")}) {
		t.Fatal("multiple SPF TXT records must not be accepted")
	}
}

func TestPagePasswordProtectionAppliesToNestedPaths(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/passport/one", "Nested", "<html><body>nested secret</body></html>"); err != nil {
		t.Fatalf("insert nested page: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/passports", "Sibling", "<html><body>sibling public</body></html>"); err != nil {
		t.Fatalf("insert sibling page: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/passport/one", "<html><body>nested static secret</body></html>")
	application.writePublishedStaticHTML("localhost", "/passports", "<html><body>sibling static public</body></html>")
	application.setPagePasswordRule(context.Background(), "localhost", "/passport", "secret")

	protectedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport/one", nil)
	protectedResponse := httptest.NewRecorder()
	application.route(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("protected status = %d, want %d, body=%q", protectedResponse.Code, http.StatusUnauthorized, protectedResponse.Body.String())
	}
	if strings.Contains(protectedResponse.Body.String(), "nested static secret") {
		t.Fatalf("protected page leaked nested content: %s", protectedResponse.Body.String())
	}

	form := url.Values{}
	form.Set("password", "secret")
	unlockRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/passport/one?page_password_unlock", strings.NewReader(form.Encode()))
	unlockRequest.RemoteAddr = "198.51.100.40:1234"
	unlockRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	unlockRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockResponse := httptest.NewRecorder()
	application.route(unlockResponse, unlockRequest)
	if unlockResponse.Code != http.StatusFound {
		t.Fatalf("unlock status = %d, want %d, body=%q", unlockResponse.Code, http.StatusFound, unlockResponse.Body.String())
	}
	unlockCookies := unlockResponse.Result().Cookies()
	if len(unlockCookies) == 0 {
		t.Fatal("unlock did not set a page password cookie")
	}
	if unlockCookies[0].MaxAge > int(pagePasswordSessionTTL.Seconds()) {
		t.Fatalf("page password cookie MaxAge = %d, want at most %d", unlockCookies[0].MaxAge, int(pagePasswordSessionTTL.Seconds()))
	}
	if unlockCookies[0].Expires.IsZero() || unlockCookies[0].Expires.After(time.Now().Add(pagePasswordSessionTTL+time.Minute)) {
		t.Fatalf("page password cookie Expires = %s, want about one hour", unlockCookies[0].Expires.Format(time.RFC3339))
	}

	openedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport/one", nil)
	openedRequest.RemoteAddr = "198.51.100.40:1234"
	openedRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	openedRequest.AddCookie(unlockCookies[0])
	openedResponse := httptest.NewRecorder()
	application.route(openedResponse, openedRequest)
	if openedResponse.Code != http.StatusOK {
		t.Fatalf("opened status = %d, want %d, body=%q", openedResponse.Code, http.StatusOK, openedResponse.Body.String())
	}
	if !strings.Contains(openedResponse.Body.String(), "nested static secret") {
		t.Fatalf("opened protected page missing content: %s", openedResponse.Body.String())
	}

	siblingRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passports/", nil)
	siblingResponse := httptest.NewRecorder()
	application.route(siblingResponse, siblingRequest)
	if siblingResponse.Code != http.StatusOK || !strings.Contains(siblingResponse.Body.String(), "sibling static public") {
		t.Fatalf("sibling response = %d %q, want public sibling content", siblingResponse.Code, siblingResponse.Body.String())
	}
}

func TestPagePasswordProtectionAppliesToLoggedInAdmin(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/admin-secret", "Admin Secret", "<html><body>admin draft secret</body></html>"); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	application.setPagePasswordRule(context.Background(), "localhost", "/admin-secret", "secret")

	adminSessionCookie := newAdminSessionCookie(t, application, "admin@example.com")
	protectedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin-secret", nil)
	protectedRequest.AddCookie(adminSessionCookie)
	protectedResponse := httptest.NewRecorder()
	application.route(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("protected admin status = %d, want %d, body=%q", protectedResponse.Code, http.StatusUnauthorized, protectedResponse.Body.String())
	}
	if strings.Contains(protectedResponse.Body.String(), "admin draft secret") {
		t.Fatalf("protected admin page leaked content: %s", protectedResponse.Body.String())
	}

	form := url.Values{}
	form.Set("password", "secret")
	unlockRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/admin-secret?page_password_unlock", strings.NewReader(form.Encode()))
	unlockRequest.RemoteAddr = "198.51.100.42:1234"
	unlockRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	unlockRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockRequest.AddCookie(adminSessionCookie)
	unlockResponse := httptest.NewRecorder()
	application.route(unlockResponse, unlockRequest)
	if unlockResponse.Code != http.StatusFound {
		t.Fatalf("unlock status = %d, want %d, body=%q", unlockResponse.Code, http.StatusFound, unlockResponse.Body.String())
	}
	unlockCookies := unlockResponse.Result().Cookies()
	if len(unlockCookies) == 0 {
		t.Fatal("unlock did not set a page password cookie")
	}

	openedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin-secret", nil)
	openedRequest.RemoteAddr = "198.51.100.42:1234"
	openedRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	openedRequest.AddCookie(adminSessionCookie)
	openedRequest.AddCookie(unlockCookies[0])
	openedResponse := httptest.NewRecorder()
	application.route(openedResponse, openedRequest)
	if openedResponse.Code != http.StatusOK {
		t.Fatalf("opened status = %d, want %d, body=%q", openedResponse.Code, http.StatusOK, openedResponse.Body.String())
	}
	if !strings.Contains(openedResponse.Body.String(), "admin draft secret") {
		t.Fatalf("opened admin page missing content: %s", openedResponse.Body.String())
	}
}

func TestGuestProtectedStaticRouteUsesPrefixFileWithoutDatabase(t *testing.T) {
	storagePath := t.TempDir()
	application := &App{db: panicSQLExecutor{t: t}, storagePath: storagePath}
	application.writePublishedStaticHTML("localhost", "/passport/one", "<html><body>nested static secret</body></html>")
	application.writePublishedStaticHTML("localhost", "/public", "<html><body>public static page</body></html>")

	prefixFilePath := application.pagePasswordPrefixFilePath("localhost")
	if err := os.MkdirAll(filepath.Dir(prefixFilePath), 0o700); err != nil {
		t.Fatal(err)
	}
	passwordHash, hashErr := dirprotect.Hash("secret")
	if hashErr != nil {
		t.Fatalf("hash password: %v", hashErr)
	}
	rule := PagePasswordRule{Domain: "localhost", Path: "/passport", PasswordHash: passwordHash}
	if err := os.WriteFile(prefixFilePath, []byte(rule.Path+"\t"+rule.PasswordHash+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	protectedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport/one", nil)
	protectedResponse := httptest.NewRecorder()
	application.route(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("protected status = %d, want %d, body=%q", protectedResponse.Code, http.StatusUnauthorized, protectedResponse.Body.String())
	}
	if strings.Contains(protectedResponse.Body.String(), "nested static secret") {
		t.Fatalf("protected page leaked static content: %s", protectedResponse.Body.String())
	}

	openedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport/one", nil)
	openedRequest.RemoteAddr = "198.51.100.41:1234"
	openedRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	parsedRule, parsedRuleFound := application.pagePasswordRuleFromPrefixFile("localhost", "/passport/one")
	if !parsedRuleFound {
		t.Fatal("prefix-file page password rule missing")
	}
	issuedAt := time.Now().UTC()
	sessionRule := parsedRule
	sessionRule.Domain = normalizeDomainName(sessionRule.Domain)
	sessionToken := dirprotect.BoundSessionToken(sessionRule, clientIPAddress(openedRequest), openedRequest.UserAgent(), issuedAt)
	if !dirprotect.BoundSessionTokenValid(sessionRule, sessionToken, clientIPAddress(openedRequest), openedRequest.UserAgent(), issuedAt.Add(time.Second), pagePasswordSessionTTL) {
		t.Fatal("prefix-file page password token should be valid before cookie storage")
	}
	openedRequest.AddCookie(newPagePasswordTestCookie(parsedRule, openedRequest, issuedAt))
	if !application.pagePasswordSessionValid(openedRequest, parsedRule) {
		t.Fatal("prefix-file page password cookie should be valid before routing")
	}
	openedResponse := httptest.NewRecorder()
	application.route(openedResponse, openedRequest)
	if openedResponse.Code != http.StatusOK {
		t.Fatalf("opened status = %d, want %d, body=%q", openedResponse.Code, http.StatusOK, openedResponse.Body.String())
	}
	if !strings.Contains(openedResponse.Body.String(), "nested static secret") {
		t.Fatalf("opened protected page missing content: %s", openedResponse.Body.String())
	}

	publicRedirectRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/public", nil)
	publicRedirectResponse := httptest.NewRecorder()
	application.route(publicRedirectResponse, publicRedirectRequest)
	if publicRedirectResponse.Code != http.StatusMovedPermanently {
		t.Fatalf("public redirect status = %d, want %d, body=%q", publicRedirectResponse.Code, http.StatusMovedPermanently, publicRedirectResponse.Body.String())
	}
	if location := publicRedirectResponse.Header().Get("Location"); location != "/public/" {
		t.Fatalf("public redirect location = %q, want %q", location, "/public/")
	}

	publicRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/public/", nil)
	publicResponse := httptest.NewRecorder()
	application.route(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusOK || !strings.Contains(publicResponse.Body.String(), "public static page") {
		t.Fatalf("public response = %d %q, want public static content", publicResponse.Code, publicResponse.Body.String())
	}
}

func TestPagePasswordSessionFollowsClientIPAddress(t *testing.T) {
	passwordHash, hashErr := dirprotect.Hash("secret")
	if hashErr != nil {
		t.Fatalf("hash password: %v", hashErr)
	}
	rule := PagePasswordRule{Domain: "localhost", Path: "/passport", PasswordHash: passwordHash}
	issuedAt := time.Now().UTC().Add(-time.Minute)
	originalRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport", nil)
	originalRequest.RemoteAddr = "198.51.100.42:1234"
	originalRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	token := dirprotect.BoundSessionToken(rule, clientIPAddress(originalRequest), originalRequest.UserAgent(), issuedAt)

	if !dirprotect.BoundSessionTokenValid(rule, token, clientIPAddress(originalRequest), originalRequest.UserAgent(), issuedAt.Add(time.Minute), pagePasswordSessionTTL) {
		t.Fatal("page password token should be valid for the original IP and browser")
	}

	changedIPRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport", nil)
	changedIPRequest.RemoteAddr = "198.51.100.43:1234"
	changedIPRequest.Header.Set("User-Agent", "Sitebrush Test Browser")
	if dirprotect.BoundSessionTokenValid(rule, token, clientIPAddress(changedIPRequest), changedIPRequest.UserAgent(), issuedAt.Add(time.Minute), pagePasswordSessionTTL) {
		t.Fatal("page password token should expire when the IP address changes")
	}

	changedBrowserRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport", nil)
	changedBrowserRequest.RemoteAddr = "198.51.100.42:1234"
	changedBrowserRequest.Header.Set("User-Agent", "Other Browser")
	if !dirprotect.BoundSessionTokenValid(rule, token, clientIPAddress(changedBrowserRequest), changedBrowserRequest.UserAgent(), issuedAt.Add(time.Minute), pagePasswordSessionTTL) {
		t.Fatal("page password token should stay valid when only the browser changes")
	}

	if dirprotect.BoundSessionTokenValid(rule, token, clientIPAddress(originalRequest), originalRequest.UserAgent(), issuedAt.Add(pagePasswordSessionTTL+time.Second), pagePasswordSessionTTL) {
		t.Fatal("page password token should expire after one hour")
	}
}

func TestUnlockedProtectedGuestCanUsePublishedPageFallback(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/passport", "Passport", "<html><body>published passport fallback</body></html>"); err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	application.setPagePasswordRule(context.Background(), "localhost", "/passport", "secret")

	form := url.Values{}
	form.Set("password", "secret")
	unlockRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/passport?page_password_unlock", strings.NewReader(form.Encode()))
	unlockRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockResponse := httptest.NewRecorder()
	application.route(unlockResponse, unlockRequest)
	if unlockResponse.Code != http.StatusFound {
		t.Fatalf("unlock status = %d, want %d, body=%q", unlockResponse.Code, http.StatusFound, unlockResponse.Body.String())
	}

	openedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/passport", nil)
	for _, cookie := range unlockResponse.Result().Cookies() {
		openedRequest.AddCookie(cookie)
	}
	openedResponse := httptest.NewRecorder()
	application.route(openedResponse, openedRequest)
	if openedResponse.Code != http.StatusOK {
		t.Fatalf("opened status = %d, want %d, body=%q", openedResponse.Code, http.StatusOK, openedResponse.Body.String())
	}
	if !strings.Contains(openedResponse.Body.String(), "published passport fallback") {
		t.Fatalf("opened protected page missing fallback content: %s", openedResponse.Body.String())
	}
	if strings.Contains(openedResponse.Body.String(), "404:") {
		t.Fatalf("opened protected page used guest 404: %s", openedResponse.Body.String())
	}
}

func TestPagePasswordProtectionCanBeAddedAndRemovedFromMenuAction(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/docs", "Docs", "<html><body>docs secret</body></html>"); err != nil {
		t.Fatalf("insert page: %v", err)
	}

	form := url.Values{}
	form.Set("password", "secret")
	protectRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?page_password=protect", strings.NewReader(form.Encode()))
	protectRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	protectRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	protectResponse := httptest.NewRecorder()
	application.route(protectResponse, protectRequest)
	if protectResponse.Code != http.StatusFound {
		t.Fatalf("protect status = %d, want %d", protectResponse.Code, http.StatusFound)
	}
	if !application.pagePasswordRuleExists(context.Background(), "localhost", "/docs") {
		t.Fatal("protect action did not create page password rule")
	}

	removeRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?page_password=remove", nil)
	removeRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	removeResponse := httptest.NewRecorder()
	application.route(removeResponse, removeRequest)
	if removeResponse.Code != http.StatusFound {
		t.Fatalf("remove status = %d, want %d", removeResponse.Code, http.StatusFound)
	}
	if application.pagePasswordRuleExists(context.Background(), "localhost", "/docs") {
		t.Fatal("remove action left page password rule in place")
	}

	protectAgainRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?page_password=protect", strings.NewReader(form.Encode()))
	protectAgainRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	protectAgainRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	protectAgainResponse := httptest.NewRecorder()
	application.route(protectAgainResponse, protectAgainRequest)
	if protectAgainResponse.Code != http.StatusFound {
		t.Fatalf("protect-again status = %d, want %d", protectAgainResponse.Code, http.StatusFound)
	}

	nestedRemoveRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs/child?page_password=remove", nil)
	nestedRemoveRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	nestedRemoveResponse := httptest.NewRecorder()
	application.route(nestedRemoveResponse, nestedRemoveRequest)
	if nestedRemoveResponse.Code != http.StatusFound {
		t.Fatalf("nested remove status = %d, want %d", nestedRemoveResponse.Code, http.StatusFound)
	}
	if application.pagePasswordRuleExists(context.Background(), "localhost", "/docs") {
		t.Fatal("nested remove action did not remove parent protected prefix rule")
	}
}

func TestPagePasswordFailedAttemptsEscalateToIPBlock(t *testing.T) {
	application, _ := newTestApplication(t)
	application.writePublishedStaticHTML("localhost", "/secret", "<html><body>protected static content</body></html>")
	application.setPagePasswordRule(context.Background(), "localhost", "/secret", "secret")

	for attemptIndex := 1; attemptIndex <= 4; attemptIndex++ {
		form := url.Values{}
		form.Set("password", "wrong")
		request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/secret?page_password_unlock", strings.NewReader(form.Encode()))
		request.RemoteAddr = "198.51.100.30:1234"
		request.Header.Set("User-Agent", "First Test Browser")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()

		application.route(response, request)
		expectedStatus := http.StatusUnauthorized
		if attemptIndex == 4 {
			expectedStatus = http.StatusTooManyRequests
		}
		if response.Code != expectedStatus {
			t.Fatalf("attempt %d status = %d, want %d, body=%q", attemptIndex, response.Code, expectedStatus, response.Body.String())
		}
	}

	form := url.Values{}
	form.Set("password", "secret")
	blockedRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/secret?page_password_unlock", strings.NewReader(form.Encode()))
	blockedRequest.RemoteAddr = "198.51.100.30:1234"
	blockedRequest.Header.Set("User-Agent", "Second Test Browser")
	blockedRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	blockedRequest.Header.Set("Accept-Language", "ru")
	blockedResponse := httptest.NewRecorder()
	application.route(blockedResponse, blockedRequest)
	if blockedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status = %d, want %d, body=%q", blockedResponse.Code, http.StatusTooManyRequests, blockedResponse.Body.String())
	}
	if blockedResponse.Header().Get("Retry-After") == "" {
		t.Fatal("blocked password response did not set Retry-After")
	}
	blockedBody := blockedResponse.Body.String()
	for _, expectedFragment := range []string{
		`id="SiteBrushProtectedCountdown"`,
		`data-countdown-text`,
		"Повторить попытку можно через:",
		"С этого IP введено слишком много неправильных паролей",
	} {
		if !strings.Contains(blockedBody, expectedFragment) {
			t.Fatalf("blocked password page missing %q in %s", expectedFragment, blockedBody)
		}
	}
	if strings.Contains(blockedBody, `name="password"`) {
		t.Fatalf("blocked password page should hide password input: %s", blockedBody)
	}
	if len(blockedResponse.Result().Cookies()) != 0 {
		t.Fatalf("blocked password response set cookies: %#v", blockedResponse.Result().Cookies())
	}

	blockedGetRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
	blockedGetRequest.RemoteAddr = "198.51.100.30:1234"
	blockedGetRequest.Header.Set("User-Agent", "Second Test Browser")
	blockedGetRequest.Header.Set("Accept-Language", "ru")
	blockedGetResponse := httptest.NewRecorder()
	application.route(blockedGetResponse, blockedGetRequest)
	if blockedGetResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked GET status = %d, want %d, body=%q", blockedGetResponse.Code, http.StatusTooManyRequests, blockedGetResponse.Body.String())
	}
	if strings.Contains(blockedGetResponse.Body.String(), `name="password"`) {
		t.Fatalf("blocked GET should hide password input: %s", blockedGetResponse.Body.String())
	}

	rule, found := application.pagePasswordRuleFromPrefixFile("localhost", "/secret")
	if !found {
		t.Fatal("page password rule missing from prefix file")
	}
	cookieBypassRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
	cookieBypassRequest.RemoteAddr = "198.51.100.30:1234"
	cookieBypassRequest.Header.Set("User-Agent", "First Test Browser")
	cookieBypassRequest.Header.Set("Accept-Language", "ru")
	cookieBypassRequest.AddCookie(newPagePasswordTestCookie(rule, cookieBypassRequest, time.Now().UTC()))
	cookieBypassResponse := httptest.NewRecorder()
	application.route(cookieBypassResponse, cookieBypassRequest)
	if cookieBypassResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("cookie bypass status = %d, want %d, body=%q", cookieBypassResponse.Code, http.StatusTooManyRequests, cookieBypassResponse.Body.String())
	}
	if strings.Contains(cookieBypassResponse.Body.String(), "protected static content") {
		t.Fatalf("blocked IP with page password cookie leaked protected content: %s", cookieBypassResponse.Body.String())
	}
}

func TestPagePasswordBlockUsesMaximumTimeoutForIPAddress(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.writePublishedStaticHTML("localhost", "/secret", "<html><body>protected static content</body></html>")
	application.setPagePasswordRule(context.Background(), "localhost", "/secret", "secret")

	now := time.Now().UTC()
	clientIP := "198.51.100.81"
	globalFailureDomain := pagePasswordFailureDomain("localhost", "/secret")
	legacyPathFailureDomain := "localhost|page-password|/secret"
	shortBlockUntil := now.Add(5 * time.Minute).Format(time.RFC3339)
	longBlockUntil := now.Add(15 * time.Minute).Format(time.RFC3339)
	for _, seededState := range []struct {
		domain       string
		failureCount int
		blockedUntil string
	}{
		{domain: globalFailureDomain, failureCount: 5, blockedUntil: shortBlockUntil},
		{domain: legacyPathFailureDomain, failureCount: 6, blockedUntil: longBlockUntil},
	} {
		_, err := rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
			seededState.domain, clientIP, seededState.failureCount, seededState.blockedUntil, 0, now.Format(time.RFC3339), now.Format(time.RFC3339))
		if err != nil {
			t.Fatalf("seed auth failure %s: %v", seededState.domain, err)
		}
		application.storeAuthIPFailureState(seededState.domain, clientIP, authIPFailure{FailureCount: seededState.failureCount, BlockedUntil: seededState.blockedUntil})
	}

	blockedGetRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
	blockedGetRequest.RemoteAddr = clientIP + ":1234"
	blockedGetRequest.Header.Set("User-Agent", "First Browser")
	blockedGetResponse := httptest.NewRecorder()
	application.route(blockedGetResponse, blockedGetRequest)
	if blockedGetResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked GET status = %d, want %d, body=%q", blockedGetResponse.Code, http.StatusTooManyRequests, blockedGetResponse.Body.String())
	}
	if strings.Contains(blockedGetResponse.Body.String(), `name="password"`) {
		t.Fatalf("blocked GET should not show password input: %s", blockedGetResponse.Body.String())
	}
	retryAfter, err := strconv.Atoi(blockedGetResponse.Header().Get("Retry-After"))
	if err != nil || retryAfter < int((10*time.Minute).Seconds()) {
		t.Fatalf("blocked GET Retry-After = %q, want maximum timeout near 15 minutes", blockedGetResponse.Header().Get("Retry-After"))
	}

	form := url.Values{}
	form.Set("password", "secret")
	blockedPostRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/secret?page_password_unlock", strings.NewReader(form.Encode()))
	blockedPostRequest.RemoteAddr = clientIP + ":5678"
	blockedPostRequest.Header.Set("User-Agent", "Second Browser")
	blockedPostRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	blockedPostResponse := httptest.NewRecorder()
	application.route(blockedPostResponse, blockedPostRequest)
	if blockedPostResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked POST status = %d, want %d, body=%q", blockedPostResponse.Code, http.StatusTooManyRequests, blockedPostResponse.Body.String())
	}
	if strings.Contains(blockedPostResponse.Body.String(), `name="password"`) {
		t.Fatalf("blocked POST should not show password input: %s", blockedPostResponse.Body.String())
	}
	retryAfter, err = strconv.Atoi(blockedPostResponse.Header().Get("Retry-After"))
	if err != nil || retryAfter < int((10*time.Minute).Seconds()) {
		t.Fatalf("blocked POST Retry-After = %q, want maximum timeout near 15 minutes", blockedPostResponse.Header().Get("Retry-After"))
	}
}

func TestPagePasswordBlockTreatsLocalhostIPv4AndIPv6AsSameClient(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.writePublishedStaticHTML("localhost", "/secret", "<html><body>protected static content</body></html>")
	application.setPagePasswordRule(context.Background(), "localhost", "/secret", "secret")

	blockedUntil := time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339)
	_, err := rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		pagePasswordFailureDomain("localhost", "/secret"), "127.0.0.1", 6, blockedUntil, 0, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failure: %v", err)
	}
	application.storeAuthIPFailureState(pagePasswordFailureDomain("localhost", "/secret"), "127.0.0.1", authIPFailure{FailureCount: 6, BlockedUntil: blockedUntil})

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
	request.RemoteAddr = "[::1]:1234"
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `name="password"`) {
		t.Fatalf("blocked localhost alias should not show password input: %s", response.Body.String())
	}
	retryAfter, err := strconv.Atoi(response.Header().Get("Retry-After"))
	if err != nil || retryAfter < int((10*time.Minute).Seconds()) {
		t.Fatalf("Retry-After = %q, want timeout from IPv4 localhost record", response.Header().Get("Retry-After"))
	}
}

func TestBlockedLoginPageTreatsLocalhostIPv4AndIPv6AsSameClient(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "127.0.0.1", 6, time.Now().UTC().Add(15*time.Minute).Format(time.RFC3339), 0, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failure: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	request.RemoteAddr = "[::1]:1234"
	response := httptest.NewRecorder()
	application.login(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	for _, unexpectedFragment := range []string{`name="password"`, `name="email"`} {
		if strings.Contains(response.Body.String(), unexpectedFragment) {
			t.Fatalf("blocked localhost alias should hide login field %q in %s", unexpectedFragment, response.Body.String())
		}
	}
}

func TestClientIPAddressIgnoresSpoofedForwardedHeadersFromPublicRemote(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
	request.RemoteAddr = "198.51.100.80:1234"
	request.Header.Set("Forwarded", `for=203.0.113.10`)
	request.Header.Set("X-Forwarded-For", "203.0.113.11")

	if clientIP := clientIPAddress(request); clientIP != "198.51.100.80" {
		t.Fatalf("client IP = %q, want public remote address", clientIP)
	}
}

func TestClientIPAddressNormalizesLocalhostAliases(t *testing.T) {
	for _, remoteAddress := range []string{"127.0.0.1:1234", "[::1]:1234"} {
		request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
		request.RemoteAddr = remoteAddress
		if clientIP := clientIPAddress(request); clientIP != "localhost" {
			t.Fatalf("client IP for %s = %q, want localhost", remoteAddress, clientIP)
		}
	}
}

func TestClientIPAddressAcceptsForwardedHeadersFromTrustedProxy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/secret", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.11")

	if clientIP := clientIPAddress(request); clientIP != "203.0.113.11" {
		t.Fatalf("client IP = %q, want forwarded address from trusted proxy", clientIP)
	}
}

func TestFailedLoginAttemptsEscalateToIPBlock(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	for attemptIndex := 1; attemptIndex <= 4; attemptIndex++ {
		form := url.Values{}
		form.Set("email", "admin@example.com")
		form.Set("password", "wrong")
		request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
		request.RemoteAddr = "198.51.100.10:1234"
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()

		application.login(response, request)
		expectedStatus := http.StatusUnauthorized
		if attemptIndex == 4 {
			expectedStatus = http.StatusTooManyRequests
		}
		if response.Code != expectedStatus {
			t.Fatalf("attempt %d status = %d, want %d, body=%q", attemptIndex, response.Code, expectedStatus, response.Body.String())
		}
	}
}

func TestFailedLoginRendersLoginFormWithLocalizedStatus(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "wrong")
	form.Set("return_path", "/docs")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
	request.RemoteAddr = "198.51.100.11:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.login(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`<form class="card card-body" method="post" action="?login"`, "Неверный email или пароль.", `value="admin@example.com"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("login failure page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestTenthFailedLoginAttemptRequiresRecovery(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.20", 9, "", 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "wrong")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
	request.RemoteAddr = "198.51.100.20:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	application.login(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestBlockedLoginPageShowsCountdownInsteadOfForm(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.21", 4, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	router := http.NewServeMux()
	router.HandleFunc("/", application.route)
	protectedHandler := application.authAbuseMiddleware(router)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	request.RemoteAddr = "198.51.100.21:1234"
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()
	protectedHandler.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	body := response.Body.String()
	for _, unexpectedFragment := range []string{`name="password"`, `name="email"`} {
		if strings.Contains(body, unexpectedFragment) {
			t.Fatalf("blocked login page should hide form field %q in %s", unexpectedFragment, body)
		}
	}
	for _, expectedFragment := range []string{"Повторить попытку можно через:", `id="SiteBrushLoginCountdown"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("blocked login page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestHardLockedLoginPageShowsRecoveryInsteadOfForm(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.22", 10, "", 1, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	router := http.NewServeMux()
	router.HandleFunc("/", application.route)
	protectedHandler := application.authAbuseMiddleware(router)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	request.RemoteAddr = "198.51.100.22:1234"
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()
	protectedHandler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusForbidden, response.Body.String())
	}
	body := response.Body.String()
	for _, unexpectedFragment := range []string{`name="password"`, `name="email"`} {
		if strings.Contains(body, unexpectedFragment) {
			t.Fatalf("hard-locked login page should hide form field %q in %s", unexpectedFragment, body)
		}
	}
	for _, expectedFragment := range []string{"используйте восстановление доступа", `href="?recover"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("hard-locked login page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestBlockedIPMiddlewareLeavesNonLoginRequestsAvailable(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.30", 4, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	embeddedStaticAssets, err := buildEmbeddedStaticAssetCache(testStaticFiles(t))
	if err != nil {
		t.Fatalf("build static cache: %v", err)
	}
	application.embeddedStaticAssets = embeddedStaticAssets
	router := http.NewServeMux()
	router.HandleFunc("/p/static/", application.serveEmbeddedStaticAsset)
	router.HandleFunc("/p/", application.servePublicAsset)
	router.HandleFunc("/", application.route)
	protectedHandler := application.authAbuseMiddleware(router)

	dynamicRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	dynamicRequest.RemoteAddr = "198.51.100.30:1234"
	dynamicResponse := httptest.NewRecorder()
	protectedHandler.ServeHTTP(dynamicResponse, dynamicRequest)
	if dynamicResponse.Code == http.StatusTooManyRequests || dynamicResponse.Code == http.StatusForbidden {
		t.Fatalf("dynamic status = %d, want non-login request to remain available, body=%q", dynamicResponse.Code, dynamicResponse.Body.String())
	}

	staticRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/static/jodit.min.js", nil)
	staticRequest.RemoteAddr = "198.51.100.30:1234"
	staticResponse := httptest.NewRecorder()
	protectedHandler.ServeHTTP(staticResponse, staticRequest)
	if staticResponse.Code != http.StatusOK {
		t.Fatalf("static status = %d, want %d", staticResponse.Code, http.StatusOK)
	}
}

func TestEmbeddedStaticAssetsServedFromMemory(t *testing.T) {
	application, _ := newTestApplication(t)
	embeddedStaticAssets, err := buildEmbeddedStaticAssetCache(testStaticFiles(t))
	if err != nil {
		t.Fatalf("build static cache: %v", err)
	}
	application.embeddedStaticAssets = embeddedStaticAssets

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/static/login.png", nil)
	response := httptest.NewRecorder()
	application.serveEmbeddedStaticAsset(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "image/png") {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
	if response.Body.Len() == 0 {
		t.Fatal("static asset body is empty")
	}

	codeMirrorRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/static/codemirror/htmlmixed.min.js", nil)
	codeMirrorResponse := httptest.NewRecorder()
	application.serveEmbeddedStaticAsset(codeMirrorResponse, codeMirrorRequest)
	if codeMirrorResponse.Code != http.StatusOK {
		t.Fatalf("codemirror status = %d, body=%q", codeMirrorResponse.Code, codeMirrorResponse.Body.String())
	}
	if contentType := codeMirrorResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("codemirror content type = %q, want javascript", contentType)
	}
	if !strings.Contains(codeMirrorResponse.Body.String(), "htmlmixed") {
		t.Fatalf("codemirror asset body did not look like htmlmixed mode")
	}
}

func TestBlockedLoginPageUsesSameTimerFromAnyURI(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.31", 4, time.Now().UTC().Add(2*time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/address/book?login", nil)
	request.RemoteAddr = "198.51.100.31:1234"
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{
		"Повторить попытку можно через:",
		`id="SiteBrushLoginCountdown"`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("blocked login page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestDeleteRevisionByQueryDisablesRevisionAndAppliesPreviousActiveRevision(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "new")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "old", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert old revision: %v", err)
	}
	result, err := rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "new", "2026-01-02T00:00:00Z")
	if err != nil {
		t.Fatalf("insert new revision: %v", err)
	}
	revisionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("revision id: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?delete="+strconv.FormatInt(revisionID, 10), nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var isActive int
	if err := rawDB.QueryRow(`SELECT is_active FROM revisions WHERE id=?`, revisionID).Scan(&isActive); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	if isActive != 0 {
		t.Fatalf("revision is_active = %d, want 0", isActive)
	}
	var pageHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/docs").Scan(&pageHTML); err != nil {
		t.Fatalf("read page: %v", err)
	}
	if pageHTML != "old" {
		t.Fatalf("page html = %q, want old", pageHTML)
	}
}

func TestRevisionsPageShowsPreviewButtonForEditableUser(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	result, err := rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "<h1>old</h1>", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	revisionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("revision id: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?revisions", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	expectedPreviewURL := `/docs?revision_preview&id=` + strconv.FormatInt(revisionID, 10)
	for _, expectedFragment := range []string{`revision-preview-button`, `href="` + expectedPreviewURL + `"`, `data-revision-preview-url="/docs?revision_preview&id=` + strconv.FormatInt(revisionID, 10) + `"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("revisions page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestProfilePageUpdatesAdminEmailAndPassword(t *testing.T) {
	withEmailSPFAllowed(t)
	application, rawDB := newTestApplication(t)
	captureImmediateProfileEmail(t, application)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	form := url.Values{}
	form.Set("email", "new@example.com")
	form.Set("password", "new-secret")
	form.Set("password_confirm", "new-secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	for _, expectedFragment := range []string{`name="password_confirmation_code"`, `maxlength="6"`, `class="profile-code-submit" type="submit" disabled`} {
		if !strings.Contains(response.Body.String(), expectedFragment) {
			t.Fatalf("profile password code form missing %q in %s", expectedFragment, response.Body.String())
		}
	}
	select {
	case mailJob := <-application.emailDelivery:
		if mailJob.Message.To != "admin@example.com" || !strings.Contains(mailJob.Message.Body, "SiteBrush") {
			t.Fatalf("unexpected password code email: %#v", mailJob.Message)
		}
	default:
		t.Fatal("profile update did not enqueue password code email")
	}

	var pendingToken, pendingCode string
	if err := rawDB.QueryRow(`SELECT token,verification_code FROM email_confirmations WHERE domain=? AND action=? AND current_email=?`, "localhost", "profile_password", "admin@example.com").Scan(&pendingToken, &pendingCode); err != nil {
		t.Fatalf("read password confirmation: %v", err)
	}
	if len(pendingCode) != 6 {
		t.Fatalf("pending code = %q, want 6 digits", pendingCode)
	}
	codeForm := url.Values{}
	codeForm.Set("password_confirmation_token", pendingToken)
	codeForm.Set("password_confirmation_code", pendingCode)
	codeRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(codeForm.Encode()))
	codeRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	codeRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	codeResponse := httptest.NewRecorder()
	application.route(codeResponse, codeRequest)
	if codeResponse.Code != http.StatusOK {
		t.Fatalf("code status = %d, body=%q", codeResponse.Code, codeResponse.Body.String())
	}
	select {
	case mailJob := <-application.emailDelivery:
		if mailJob.Message.To != "new@example.com" || !strings.Contains(mailJob.Message.Body, "email_confirm=") {
			t.Fatalf("unexpected email confirmation: %#v", mailJob.Message)
		}
	default:
		t.Fatal("profile update did not enqueue email confirmation")
	}

	var emailToken string
	if err := rawDB.QueryRow(`SELECT token FROM email_confirmations WHERE domain=? AND action=? AND email=?`, "localhost", "profile", "new@example.com").Scan(&emailToken); err != nil {
		t.Fatalf("read email confirmation token: %v", err)
	}
	confirmRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?email_confirm="+url.QueryEscape(emailToken), nil)
	confirmResponse := httptest.NewRecorder()
	application.route(confirmResponse, confirmRequest)
	if confirmResponse.Code != http.StatusFound {
		t.Fatalf("confirm status = %d, body=%q", confirmResponse.Code, confirmResponse.Body.String())
	}

	var password string
	if err := rawDB.QueryRow(`SELECT password FROM users WHERE domain=? AND email=?`, "localhost", "new@example.com").Scan(&password); err != nil {
		t.Fatalf("read updated user: %v", err)
	}
	if password != "new-secret" {
		t.Fatalf("password = %q, want new-secret", password)
	}
	profileCookies := confirmResponse.Result().Cookies()
	if len(profileCookies) == 0 {
		t.Fatal("profile update did not refresh the session cookie")
	}
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	authenticatedRequest.AddCookie(profileCookies[0])
	if !application.isAdminRequest(authenticatedRequest) {
		t.Fatal("refreshed profile session is not authenticated")
	}
}

func TestProfilePasswordCodeShowsSMTPFailureDetails(t *testing.T) {
	withEmailSPFAllowed(t)
	application, rawDB := newTestApplication(t)
	application.sendEmail = func(context.Context, mailout.Message) error {
		return errors.New("alt4.gmail-smtp-in.l.google.com close data: 550 5.7.26 Your email has been blocked because the sender is unauthenticated. DKIM = did not pass SPF [localhost] with ip: [148.251.254.62] = did not pass")
	}
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "new-secret")
	form.Set("password_confirm", "new-secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`alert-danger`, `Письмо не отправлено.`, `data-profile-delivery-modal`, `Код SMTP: 550`, `profile-delivery-dns`, `v=spf1 a mx ip4:203.0.113.10 ~all`, `SPF`, `DKIM`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("SMTP failure page missing %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, `<input class="profile-code-input"`) {
		t.Fatalf("SMTP failure should not show code form: %s", body)
	}
	var pendingCount int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM email_confirmations WHERE domain=? AND action=? AND current_email=?`, "localhost", "profile_password", "admin@example.com").Scan(&pendingCount); err != nil {
		t.Fatalf("count password confirmations: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("pending password confirmations after SMTP failure = %d, want 0", pendingCount)
	}
}

func TestProfilePasswordCodeAttemptsEscalateToBlock(t *testing.T) {
	withEmailSPFAllowed(t)
	application, rawDB := newTestApplication(t)
	captureImmediateProfileEmail(t, application)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "new-secret")
	form.Set("password_confirm", "new-secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	select {
	case <-application.emailDelivery:
	case <-time.After(time.Second):
		t.Fatal("profile update did not enqueue password code email")
	}

	var pendingToken, pendingCode string
	if err := rawDB.QueryRow(`SELECT token,verification_code FROM email_confirmations WHERE domain=? AND action=? AND current_email=?`, "localhost", "profile_password", "admin@example.com").Scan(&pendingToken, &pendingCode); err != nil {
		t.Fatalf("read password confirmation: %v", err)
	}
	wrongCode := "000000"
	if pendingCode == wrongCode {
		wrongCode = "111111"
	}
	for attemptIndex := 1; attemptIndex <= 4; attemptIndex++ {
		codeForm := url.Values{}
		codeForm.Set("password_confirmation_token", pendingToken)
		codeForm.Set("password_confirmation_code", wrongCode)
		codeRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(codeForm.Encode()))
		codeRequest.RemoteAddr = "198.51.100.77:1234"
		codeRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		codeRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
		codeResponse := httptest.NewRecorder()
		application.route(codeResponse, codeRequest)
		expectedStatus := http.StatusUnauthorized
		if attemptIndex == 4 {
			expectedStatus = http.StatusTooManyRequests
		}
		if codeResponse.Code != expectedStatus {
			t.Fatalf("attempt %d status = %d, want %d, body=%q", attemptIndex, codeResponse.Code, expectedStatus, codeResponse.Body.String())
		}
		if attemptIndex == 4 {
			if codeResponse.Header().Get("Retry-After") == "" {
				t.Fatal("blocked profile password code response did not set Retry-After")
			}
			body := codeResponse.Body.String()
			if !strings.Contains(body, `id="SiteBrushLoginCountdown"`) {
				t.Fatalf("blocked profile password code page missing countdown: %s", body)
			}
			if strings.Contains(body, `<input class="profile-code-input"`) {
				t.Fatalf("blocked profile password code page should hide code field: %s", body)
			}
		}
	}
	var password string
	if err := rawDB.QueryRow(`SELECT password FROM users WHERE domain=? AND email=?`, "localhost", "admin@example.com").Scan(&password); err != nil {
		t.Fatalf("read password: %v", err)
	}
	if password != "old" {
		t.Fatalf("password changed after failed code attempts: %q", password)
	}
}

func TestProfilePasswordCodeResendClearsFailedCodeAttempts(t *testing.T) {
	withEmailSPFAllowed(t)
	application, rawDB := newTestApplication(t)
	captureImmediateProfileEmail(t, application)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		profilePasswordFailureDomain("localhost", "admin@example.com"), "198.51.100.88", 4, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed profile password failures: %v", err)
	}
	application.storeAuthIPFailureState(profilePasswordFailureDomain("localhost", "admin@example.com"), "198.51.100.88", authIPFailure{FailureCount: 4, BlockedUntil: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)})

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "new-secret")
	form.Set("password_confirm", "new-secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(form.Encode()))
	request.RemoteAddr = "198.51.100.88:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusOK, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "Too many failed code attempts") {
		t.Fatalf("resend should not show previous failed-code error: %s", response.Body.String())
	}
	select {
	case <-application.emailDelivery:
	case <-time.After(time.Second):
		t.Fatal("profile password resend did not enqueue code email")
	}
	var failureCount int
	_ = rawDB.QueryRow(`SELECT COUNT(1) FROM auth_ip_failures WHERE domain=? AND client_ip=?`, profilePasswordFailureDomain("localhost", "admin@example.com"), "198.51.100.88").Scan(&failureCount)
	if failureCount != 0 {
		t.Fatalf("profile password failure rows after resend = %d, want 0", failureCount)
	}
}

func TestSavePageImportsExternalImageAndKeepsLocalReferences(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	imageURL := "https://images.example/render?id=hero"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			imageURL: {contentType: "image/png", body: "image-bytes"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", `<html><body><img src="`+imageURL+`"><img src="/p/existing.png"><iframe src="/preview"></iframe><a href="https://external.example/page">External</a></body></html>`)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var storedHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/").Scan(&storedHTML); err != nil {
		t.Fatalf("read saved page: %v", err)
	}
	if strings.Contains(storedHTML, imageURL) {
		t.Fatalf("saved page still contains external image URL: %s", storedHTML)
	}
	if !strings.Contains(storedHTML, `src="/p/`) || !strings.Contains(storedHTML, `.png"`) {
		t.Fatalf("saved page does not contain hashed local image URL: %s", storedHTML)
	}
	if !strings.Contains(storedHTML, `src="/p/existing.png"`) {
		t.Fatalf("saved page changed an existing local resource: %s", storedHTML)
	}
	if !strings.Contains(storedHTML, `<iframe src="/preview">`) {
		t.Fatalf("saved page changed a local embedded document: %s", storedHTML)
	}
	if !strings.Contains(storedHTML, `href="https://external.example/page"`) {
		t.Fatalf("saved page changed an external navigation link: %s", storedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("localhost"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) != 1 || filepath.Ext(storedFiles[0]) != ".png" {
		t.Fatalf("stored external image files = %#v, want one png", storedFiles)
	}
	storedImage, readErr := os.ReadFile(storedFiles[0])
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(storedImage) != "image-bytes" {
		t.Fatalf("stored external image = %q", storedImage)
	}
}

func TestSavePagePropagatesSiteBrushTemplateToOtherPagesAndPublishedOutputs(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	navigationBefore := `<nav class="SiteBrush-Template shared-nav"><a href="/">Home</a></nav>`
	navigationAfter := `<nav class="SiteBrush-Template shared-nav"><a href="/">Updated</a></nav>`
	homeHTML := "<html><body>" + navigationBefore + `<main>Home</main></body></html>`
	aboutHTML := "<html><body><header>About</header>" + navigationBefore + `<main>About</main></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/about", "About", aboutHTML,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/about", "About", aboutHTML,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeHTML)
	application.writePublishedStaticHTML("localhost", "/about", aboutHTML)

	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", "<html><body>"+navigationAfter+`<main>Home</main></body></html>`)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedAboutHTML); err != nil {
		t.Fatalf("read about page: %v", err)
	}
	if !strings.Contains(updatedAboutHTML, navigationAfter) {
		t.Fatalf("about page did not receive propagated template: %s", updatedAboutHTML)
	}

	var updatedPublishedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedPublishedAboutHTML); err != nil {
		t.Fatalf("read published about page: %v", err)
	}
	if !strings.Contains(updatedPublishedAboutHTML, navigationAfter) {
		t.Fatalf("published about page did not receive propagated template: %s", updatedPublishedAboutHTML)
	}

	aboutStaticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/about")))
	if readErr != nil {
		t.Fatalf("read static about page: %v", readErr)
	}
	if !strings.Contains(string(aboutStaticHTML), navigationAfter) {
		t.Fatalf("static about page did not receive propagated template: %s", string(aboutStaticHTML))
	}

	var aboutRevisionCount int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM revisions WHERE domain=? AND page_path=?`, "localhost", "/about").Scan(&aboutRevisionCount); err != nil {
		t.Fatalf("count about revisions: %v", err)
	}
	if aboutRevisionCount != 1 {
		t.Fatalf("about revision count = %d, want 1", aboutRevisionCount)
	}
}

func TestSavePagePropagatesSiteBrushTemplateFromChildPageToRoot(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	navigationBefore := `<nav class="SiteBrush-Template shared-nav"><a href="/">Home</a></nav>`
	navigationAfter := `<nav class="SiteBrush-Template shared-nav"><a href="/">Updated</a></nav>`
	homeHTML := "<html><body>" + navigationBefore + `<main>Home</main></body></html>`
	aboutHTML := "<html><body><header>About</header>" + navigationBefore + `<main>About</main></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/about", "About", aboutHTML,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/about", "About", aboutHTML,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeHTML)
	application.writePublishedStaticHTML("localhost", "/about", aboutHTML)

	form := url.Values{}
	form.Set("path", "/about")
	form.Set("title", "About")
	form.Set("html", "<html><body><header>About</header>"+navigationAfter+`<main>About</main></body></html>`)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/about?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedHomeHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/").Scan(&updatedHomeHTML); err != nil {
		t.Fatalf("read home page: %v", err)
	}
	if !strings.Contains(updatedHomeHTML, navigationAfter) {
		t.Fatalf("root page did not receive propagated template: %s", updatedHomeHTML)
	}

	var updatedPublishedHomeHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/").Scan(&updatedPublishedHomeHTML); err != nil {
		t.Fatalf("read published home page: %v", err)
	}
	if !strings.Contains(updatedPublishedHomeHTML, navigationAfter) {
		t.Fatalf("published root page did not receive propagated template: %s", updatedPublishedHomeHTML)
	}

	homeStaticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/")))
	if readErr != nil {
		t.Fatalf("read static root page: %v", readErr)
	}
	if !strings.Contains(string(homeStaticHTML), navigationAfter) {
		t.Fatalf("static root page did not receive propagated template: %s", string(homeStaticHTML))
	}
}

func TestSavePagePropagatesAddedSiteBrushTemplateWithDifferentClassOrderToRoot(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	homeBefore := `<html><body><section class="SiteBrush-Template lead hero"><h1>Shared</h1><p>Old</p></section></body></html>`
	aboutBefore := `<html><body><section class="SiteBrush-Template hero lead"><h1>Shared</h1><p>Old</p></section></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeBefore)
	application.writePublishedStaticHTML("localhost", "/about", aboutBefore)

	aboutAfter := `<html><body><section class="SiteBrush-Template hero lead"><h1>Shared</h1><p>New</p></section></body></html>`
	form := url.Values{}
	form.Set("path", "/about")
	form.Set("title", "About")
	form.Set("html", aboutAfter)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/about?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedHomeHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/").Scan(&updatedHomeHTML); err != nil {
		t.Fatalf("read home page: %v", err)
	}
	if !strings.Contains(updatedHomeHTML, `<section class="SiteBrush-Template hero lead"><h1>Shared</h1><p>New</p></section>`) {
		t.Fatalf("root page did not receive class-order-independent template update: %s", updatedHomeHTML)
	}

	var updatedPublishedHomeHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/").Scan(&updatedPublishedHomeHTML); err != nil {
		t.Fatalf("read published home page: %v", err)
	}
	if updatedPublishedHomeHTML != updatedHomeHTML {
		t.Fatalf("published root html = %q, want %q", updatedPublishedHomeHTML, updatedHomeHTML)
	}

	homeStaticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/")))
	if readErr != nil {
		t.Fatalf("read static root page: %v", readErr)
	}
	if string(homeStaticHTML) != updatedHomeHTML {
		t.Fatalf("static root html = %q, want %q", string(homeStaticHTML), updatedHomeHTML)
	}
}

func TestSavePageSynchronizesAddedSiteBrushTemplateClassByNormalizedInnerHTML(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	homeBefore := `<html><body><section class="hero lead"><h1>Shared</h1><p>Copy</p></section><section class="secondary"><h1>Unique</h1></section></body></html>`
	aboutBefore := "<html><body><main><section class=\"lead hero\"><h1>\nShared\n</h1>\n<p>Copy</p></section></main></body></html>"
	contactBefore := `<html><body><section class="target"><h1>Shared</h1><p>Copy</p></section><div class="lead hero"><h1>Shared</h1><p>Copy</p></div></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
		"localhost", "/contact", "Contact", contactBefore,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
		"localhost", "/contact", "Contact", contactBefore,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeBefore)
	application.writePublishedStaticHTML("localhost", "/about", aboutBefore)
	application.writePublishedStaticHTML("localhost", "/contact", contactBefore)

	homeAfter := `<html><body><section class="hero SiteBrush-Template SiteBrush-Template lead"><h1>Shared</h1><p>Copy</p></section><section class="secondary"><h1>Unique</h1></section></body></html>`
	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", homeAfter)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedHomeHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/").Scan(&updatedHomeHTML); err != nil {
		t.Fatalf("read home page: %v", err)
	}
	if !strings.Contains(updatedHomeHTML, `<section class="SiteBrush-Template hero lead"><h1>Shared</h1><p>Copy</p></section>`) {
		t.Fatalf("home page did not canonicalize added class first: %s", updatedHomeHTML)
	}

	var updatedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedAboutHTML); err != nil {
		t.Fatalf("read about page: %v", err)
	}
	if !strings.Contains(updatedAboutHTML, `<section class="SiteBrush-Template hero lead"><h1>Shared</h1><p>Copy</p></section>`) {
		t.Fatalf("about page did not receive synchronized class first: %s", updatedAboutHTML)
	}

	var updatedContactHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/contact").Scan(&updatedContactHTML); err != nil {
		t.Fatalf("read contact page: %v", err)
	}
	if !strings.Contains(updatedContactHTML, `<section class="SiteBrush-Template hero lead target"><h1>Shared</h1><p>Copy</p></section>`) {
		t.Fatalf("contact section with matching tag/content did not receive synchronized class: %s", updatedContactHTML)
	}
	if strings.Contains(updatedContactHTML, `<div class="SiteBrush-Template`) {
		t.Fatalf("contact div with different tag type changed unexpectedly: %s", updatedContactHTML)
	}

	var updatedPublishedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedPublishedAboutHTML); err != nil {
		t.Fatalf("read published about page: %v", err)
	}
	if updatedPublishedAboutHTML != updatedAboutHTML {
		t.Fatalf("published about html = %q, want %q", updatedPublishedAboutHTML, updatedAboutHTML)
	}

	aboutStaticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/about")))
	if readErr != nil {
		t.Fatalf("read static about page: %v", readErr)
	}
	if string(aboutStaticHTML) != updatedAboutHTML {
		t.Fatalf("static about html = %q, want %q", string(aboutStaticHTML), updatedAboutHTML)
	}

	var aboutRevisionCount int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM revisions WHERE domain=? AND page_path=?`, "localhost", "/about").Scan(&aboutRevisionCount); err != nil {
		t.Fatalf("count about revisions: %v", err)
	}
	if aboutRevisionCount != 2 {
		t.Fatalf("about revision count = %d, want 2", aboutRevisionCount)
	}
}

func TestSavePageSynchronizesAddedSiteBrushTemplateClassToStyleByNormalizedContent(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	homeBefore := `<html><head><style type="text/css">body { color: red; }</style></head><body>Home</body></html>`
	aboutBefore := `<html><head><style
  type="text/css">
body {
	color: red;
}
</style></head><body>About</body></html>`
	tableBefore := `<html><body><table class="mainstyle"><tr><td>body { color: red; }</td></tr></table></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
		"localhost", "/table", "Table", tableBefore,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
		"localhost", "/table", "Table", tableBefore,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeBefore)
	application.writePublishedStaticHTML("localhost", "/about", aboutBefore)
	application.writePublishedStaticHTML("localhost", "/table", tableBefore)

	homeAfter := `<html><head><style type="text/css" class="SiteBrush-Template mainstyle">body{color:red;}</style></head><body>Home</body></html>`
	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", homeAfter)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedAboutHTML); err != nil {
		t.Fatalf("read about page: %v", err)
	}
	if !strings.Contains(updatedAboutHTML, `class="SiteBrush-Template mainstyle"`) {
		t.Fatalf("style page did not receive synchronized template class: %s", updatedAboutHTML)
	}
	var updatedTableHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/table").Scan(&updatedTableHTML); err != nil {
		t.Fatalf("read table page: %v", err)
	}
	if updatedTableHTML != tableBefore {
		t.Fatalf("different tag type changed unexpectedly: %s", updatedTableHTML)
	}
}

func TestSavePagePropagatesRootStyleTemplateWhenClassAndContentChangeTogether(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	homeBefore := `<html><head><style type="text/css">body { color: red; }</style></head><body>Home</body></html>`
	aboutBefore := `<html><head><style
  type="text/css">
body {
	color: red;
}
</style></head><body>About</body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeBefore)
	application.writePublishedStaticHTML("localhost", "/about", aboutBefore)

	homeAfter := `<html><head><style type="text/css" class="SiteBrush-Template mainstyle">body { color: blue; }</style></head><body>Home</body></html>`
	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", homeAfter)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedAboutHTML); err != nil {
		t.Fatalf("read about page: %v", err)
	}
	if !strings.Contains(updatedAboutHTML, `<style type="text/css" class="SiteBrush-Template mainstyle">body { color: blue; }</style>`) {
		t.Fatalf("about page did not receive root style template update: %s", updatedAboutHTML)
	}

	var updatedPublishedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedPublishedAboutHTML); err != nil {
		t.Fatalf("read published about page: %v", err)
	}
	if updatedPublishedAboutHTML != updatedAboutHTML {
		t.Fatalf("published about html = %q, want %q", updatedPublishedAboutHTML, updatedAboutHTML)
	}
}

func TestSavePageSynchronizesRemovedSiteBrushTemplateClassByNormalizedInnerHTML(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	homeBefore := `<html><body><footer class="SiteBrush-Template footer shared"><span>Phone</span></footer></body></html>`
	aboutBefore := "<html><body><footer class=\"SiteBrush-Template shared footer\"><span>\nPhone\n</span></footer></body></html>"
	contactBefore := `<html><body><footer class="SiteBrush-Template contact"><span>Phone</span></footer><div class="SiteBrush-Template footer shared"><span>Phone</span></div></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
		"localhost", "/contact", "Contact", contactBefore,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeBefore,
		"localhost", "/about", "About", aboutBefore,
		"localhost", "/contact", "Contact", contactBefore,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeBefore)
	application.writePublishedStaticHTML("localhost", "/about", aboutBefore)
	application.writePublishedStaticHTML("localhost", "/contact", contactBefore)

	homeAfter := `<html><body><footer class="footer shared"><span>Phone</span></footer></body></html>`
	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", homeAfter)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedAboutHTML); err != nil {
		t.Fatalf("read about page: %v", err)
	}
	if !strings.Contains(updatedAboutHTML, `<footer class="shared footer"><span>
Phone
</span></footer>`) {
		t.Fatalf("about page did not remove synchronized class: %s", updatedAboutHTML)
	}

	var updatedContactHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/contact").Scan(&updatedContactHTML); err != nil {
		t.Fatalf("read contact page: %v", err)
	}
	if updatedContactHTML != contactBefore {
		t.Fatalf("contact page with different tag or class set changed unexpectedly: %s", updatedContactHTML)
	}

	var updatedPublishedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedPublishedAboutHTML); err != nil {
		t.Fatalf("read published about page: %v", err)
	}
	if updatedPublishedAboutHTML != updatedAboutHTML {
		t.Fatalf("published about html = %q, want %q", updatedPublishedAboutHTML, updatedAboutHTML)
	}

	aboutStaticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/about")))
	if readErr != nil {
		t.Fatalf("read static about page: %v", readErr)
	}
	if string(aboutStaticHTML) != updatedAboutHTML {
		t.Fatalf("static about html = %q, want %q", string(aboutStaticHTML), updatedAboutHTML)
	}
}

func TestFrozenSavePublishUpdatesPublishedStaticForGuests(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	oldHTML := "<html><body><h1>Old public page</h1></body></html>"
	newHTML := "<html><body><h1>New frozen edit</h1></body></html>"
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "Docs", oldHTML)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/docs", "Docs", oldHTML)
	if err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", oldHTML, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/docs", oldHTML)
	application.setDomainFrozenState(context.Background(), "localhost", 1)

	saveForm := url.Values{}
	saveForm.Set("path", "/docs")
	saveForm.Set("title", "Docs")
	saveForm.Set("html", newHTML)
	saveRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?save", strings.NewReader(saveForm.Encode()))
	saveRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	saveResponse := httptest.NewRecorder()
	application.route(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusFound {
		t.Fatalf("save status = %d, body=%q", saveResponse.Code, saveResponse.Body.String())
	}

	frozenGuestRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs/", nil)
	frozenGuestResponse := httptest.NewRecorder()
	application.route(frozenGuestResponse, frozenGuestRequest)
	if !strings.Contains(frozenGuestResponse.Body.String(), "Old public page") {
		t.Fatalf("frozen guest did not keep old static page: %s", frozenGuestResponse.Body.String())
	}
	if strings.Contains(frozenGuestResponse.Body.String(), "New frozen edit") {
		t.Fatalf("frozen guest saw draft edit before publish: %s", frozenGuestResponse.Body.String())
	}

	publishRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?publish", nil)
	publishRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	publishResponse := httptest.NewRecorder()
	application.route(publishResponse, publishRequest)
	if publishResponse.Code != http.StatusFound {
		t.Fatalf("publish status = %d, body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	var publishedHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/docs").Scan(&publishedHTML); err != nil {
		t.Fatalf("read published page: %v", err)
	}
	if publishedHTML != newHTML {
		t.Fatalf("published html = %q, want %q", publishedHTML, newHTML)
	}
	staticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/docs")))
	if readErr != nil {
		t.Fatalf("read static page: %v", readErr)
	}
	if string(staticHTML) != newHTML {
		t.Fatalf("static html = %q, want %q", string(staticHTML), newHTML)
	}
	if application.isDomainFrozen(context.Background(), "localhost") {
		t.Fatal("domain remained frozen after publish")
	}

	publishedGuestRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs/", nil)
	publishedGuestResponse := httptest.NewRecorder()
	application.route(publishedGuestResponse, publishedGuestRequest)
	if !strings.Contains(publishedGuestResponse.Body.String(), "New frozen edit") {
		t.Fatalf("published guest did not see new static page: %s", publishedGuestResponse.Body.String())
	}
}

func TestFrozenPublishWithoutRevisionsUnfreezesUnchangedSite(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	pageHTML := "<html><body><h1>Published page</h1></body></html>"
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "Docs", pageHTML)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/docs", "Docs", pageHTML)
	if err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/docs", pageHTML)
	application.setDomainFrozenState(context.Background(), "localhost", 1)
	if _, err := rawDB.Exec(`DROP TABLE revisions`); err != nil {
		t.Fatalf("drop revisions table: %v", err)
	}

	publishRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?publish", nil)
	publishRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	publishResponse := httptest.NewRecorder()
	application.route(publishResponse, publishRequest)
	if publishResponse.Code != http.StatusFound {
		t.Fatalf("publish status = %d, body=%q", publishResponse.Code, publishResponse.Body.String())
	}
	if application.isDomainFrozen(context.Background(), "localhost") {
		t.Fatal("unchanged site remained frozen after publish")
	}
}

func TestAdminRequestUsesStaticPageWhenDomainIsNotFrozen(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	draftHTML := "<html><body><h1>Draft database page</h1></body></html>"
	staticHTML := "<html><body><h1>Published static page</h1></body></html>"
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "Docs", draftHTML)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/docs", staticHTML)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs/", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Published static page") {
		t.Fatalf("admin did not receive static page: %s", body)
	}
	if strings.Contains(body, "Draft database page") {
		t.Fatalf("admin received database draft while domain is not frozen: %s", body)
	}
	if !strings.Contains(body, "initializeSitebrushContextMenuForAdmin") {
		t.Fatalf("admin static page did not include admin menu: %s", body)
	}
	if response.Header().Get("X-Sitebrush-Source") != "static" {
		t.Fatalf("source header = %q, want static", response.Header().Get("X-Sitebrush-Source"))
	}
}

func TestAdminRequestUsesDraftDatabaseWhenDomainIsFrozen(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	draftHTML := "<html><body><h1>Draft frozen page</h1></body></html>"
	staticHTML := "<html><body><h1>Published static page</h1></body></html>"
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "Docs", draftHTML)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/docs", staticHTML)
	application.setDomainFrozenState(context.Background(), "localhost", 1)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs/", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Draft frozen page") {
		t.Fatalf("frozen admin did not receive database draft: %s", body)
	}
	if strings.Contains(body, "Published static page") {
		t.Fatalf("frozen admin received static page instead of draft: %s", body)
	}
	if !strings.Contains(body, "initializeSitebrushContextMenuForAdmin") {
		t.Fatalf("frozen admin page did not include admin menu: %s", body)
	}
	if response.Header().Get("X-Sitebrush-Source") != "dynamic" {
		t.Fatalf("source header = %q, want dynamic", response.Header().Get("X-Sitebrush-Source"))
	}
}

func TestSavePagePropagatesLegacySitebrushTemplateClass(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	legacyBefore := `<div class="sitebrush-template-footer">Old footer</div>`
	legacyAfter := `<div class="sitebrush-template-footer">New footer</div>`
	homeHTML := "<html><body>" + legacyBefore + `</body></html>`
	contactsHTML := "<html><body><main>Contacts</main>" + legacyBefore + `</body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/contacts", "Contacts", contactsHTML,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}

	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", "<html><body>"+legacyAfter+`</body></html>`)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedContactsHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/contacts").Scan(&updatedContactsHTML); err != nil {
		t.Fatalf("read contacts page: %v", err)
	}
	if !strings.Contains(updatedContactsHTML, legacyAfter) {
		t.Fatalf("legacy template did not propagate: %s", updatedContactsHTML)
	}
}

func TestMirrorRemotePageImportsNestedExternalResources(t *testing.T) {
	assetBaseURL := "https://assets.example"
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><head>` +
		`<link rel="stylesheet" href="` + assetBaseURL + `/style.css">` +
		`<script type="module" src="` + assetBaseURL + `/app.js"></script>` +
		`<script>const assetURL = "${l}"; const assetPath = "+e.url+"; const dynamicValue = this.videoId;</script>` +
		`<iframe src="https://www.youtube.com/embed/demo"></iframe>` +
		`</head><body>Imported</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			assetBaseURL + "/style.css":        {contentType: "text/css", body: `@import url("/nested.css"); body{background:url("/image.png")} @font-face{src:url("/font.eot?v=1#iefix")}`},
			assetBaseURL + "/nested.css":       {contentType: "text/css", body: `.nested{background:url("/nested-image.png")}`},
			assetBaseURL + "/app.js":           {contentType: "application/javascript", body: `import "/module.js"; console.log("app");`},
			assetBaseURL + "/module.js":        {contentType: "application/javascript", body: `console.log("module");`},
			assetBaseURL + "/font.eot?v=1":     {contentType: "application/vnd.ms-fontobject", body: "font"},
			assetBaseURL + "/image.png":        {contentType: "image/png", body: "png"},
			assetBaseURL + "/nested-image.png": {contentType: "image/png", body: "nested-png"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 7 {
		t.Fatalf("expected 7 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{})
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, assetBaseURL) {
		t.Fatalf("imported HTML still references external asset host: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "youtube.com/embed/demo") {
		t.Fatalf("imported HTML lost external iframe reference: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "/p/") {
		t.Fatalf("imported HTML does not reference local public assets: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) != len(previewResources) {
		t.Fatalf("expected %d stored files, got %d: %#v", len(previewResources), len(storedFiles), storedFiles)
	}
	usage := application.domainStorageUsage(context.Background(), "example.test")
	expectedFileBytes := diskusage.DirectorySize(application.domainFilesDirForDomain("example.test"))
	if usage.FileBytes != expectedFileBytes {
		t.Fatalf("imported asset bytes = %d, want actual disk usage %d", usage.FileBytes, expectedFileBytes)
	}
	storedFontWithCleanExtension := false
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) == ".eot" {
			storedFontWithCleanExtension = true
		}
		if filepath.Ext(storedFilePath) != ".css" && filepath.Ext(storedFilePath) != ".js" {
			continue
		}
		storedContent, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(storedContent), assetBaseURL) {
			t.Fatalf("stored nested resource still references external host in %s: %s", storedFilePath, string(storedContent))
		}
	}
	if !storedFontWithCleanExtension {
		t.Fatalf("expected font resource to be stored with clean .eot extension: %#v", storedFiles)
	}
	for _, forbiddenFragment := range []string{"/module.js", "/deep.js", "youtube.com/embed/demo"} {
		if strings.Contains(importedHTML, forbiddenFragment) && forbiddenFragment != "youtube.com/embed/demo" {
			t.Fatalf("imported HTML still contains forbidden fragment %q: %s", forbiddenFragment, importedHTML)
		}
	}
}

func TestMirrorRemotePageResolvesRootAssetJSReferencesWithoutDuplicatingDirectory(t *testing.T) {
	pageRawURL := "https://karman.cafe/"
	sourceHTML := `<!doctype html><html><head>` +
		`<script src="/assets/entries/entry-server-routing.js" type="module"></script>` +
		`<link rel="modulepreload" href="/assets/entries/renderer_default.page.client.js" as="script" type="text/javascript">` +
		`<link rel="modulepreload" href="/assets/chunks/chunk-BXl3LOEh.js" as="script" type="text/javascript">` +
		`</head><body>Imported</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://karman.cafe/assets/entries/entry-server-routing.js":         {contentType: "application/javascript", body: "const renderer = \"assets/entries/renderer_default.page.client.js\"; const chunk = \"assets/chunks/chunk-BXl3LOEh.js\"; const templateChunk = `assets/chunks/chunk-Cpu9IR5w.js`; const sibling = \"./entry-helper.js\";"},
			"https://karman.cafe/assets/entries/renderer_default.page.client.js": {contentType: "application/javascript", body: `const dependencies = ["assets/chunks/chunk-CZSVq2mV.js"]; import "../chunks/chunk-4KdN5x27.js";`},
			"https://karman.cafe/assets/chunks/chunk-BXl3LOEh.js":                {contentType: "application/javascript", body: `console.log("chunk");`},
			"https://karman.cafe/assets/chunks/chunk-CZSVq2mV.js":                {contentType: "application/javascript", body: `console.log("nested map chunk");`},
			"https://karman.cafe/assets/chunks/chunk-4KdN5x27.js":                {contentType: "application/javascript", body: `console.log("nested import chunk");`},
			"https://karman.cafe/assets/chunks/chunk-Cpu9IR5w.js":                {contentType: "application/javascript", body: `console.log("template chunk");`},
			"https://karman.cafe/assets/entries/entry-helper.js":                 {contentType: "application/javascript", body: `console.log("helper");`},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	for _, previewResource := range previewResources {
		if strings.Contains(previewResource.URL, "/assets/entries/assets/") {
			t.Fatalf("preview resource duplicated asset directory: %#v", previewResources)
		}
	}
	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}

	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, "karman.cafe/assets") {
		t.Fatalf("imported HTML still references source assets: %s", importedHTML)
	}
	if strings.Contains(importedHTML, `"//p/`) || strings.Contains(importedHTML, `'//p/`) {
		t.Fatalf("imported HTML contains protocol-relative local asset reference: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) != 7 {
		t.Fatalf("stored files = %d, want 7: %#v", len(storedFiles), storedFiles)
	}
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) != ".js" {
			continue
		}
		storedBytes, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		storedScript := string(storedBytes)
		if strings.Contains(storedScript, "assets/entries/assets") || strings.Contains(storedScript, "karman.cafe/assets") {
			t.Fatalf("stored script keeps broken source reference in %s: %s", storedFilePath, storedScript)
		}
		for _, forbiddenFragment := range []string{"assets/chunks/chunk-CZSVq2mV.js", "../chunks/chunk-4KdN5x27.js", `"//p/`, `'//p/`} {
			if strings.Contains(storedScript, forbiddenFragment) {
				t.Fatalf("stored script keeps broken reference %q in %s: %s", forbiddenFragment, storedFilePath, storedScript)
			}
		}
	}
}

func TestPreviewGrabResourcesDoesNotDownloadBinaryBodies(t *testing.T) {
	pageRawURL := "https://preview.example/"
	sourceHTML := `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><img src="/hero.png"></body></html>`
	stylesheetBody := `body{background:url("/hero.png")}`
	imageSize := int64(12345)
	imageGETCount := 0

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			header := make(http.Header)
			switch request.URL.String() {
			case "https://preview.example/app.css":
				header.Set("Content-Type", "text/css")
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(strings.NewReader(stylesheetBody)), ContentLength: int64(len(stylesheetBody)), Request: request}, nil
			case "https://preview.example/hero.png":
				header.Set("Content-Type", "image/png")
				if request.Method == http.MethodGet {
					imageGETCount++
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(strings.NewReader("")), ContentLength: imageSize, Request: request}, nil
			default:
				return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: header, Body: io.NopCloser(strings.NewReader("")), ContentLength: 0, Request: request}, nil
			}
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if imageGETCount != 0 {
		t.Fatalf("preview downloaded binary image body %d times", imageGETCount)
	}
	foundImage := false
	for _, previewResource := range previewResources {
		if previewResource.URL == "https://preview.example/hero.png" {
			foundImage = true
			if previewResource.SizeBytes != imageSize {
				t.Fatalf("image size = %d, want %d", previewResource.SizeBytes, imageSize)
			}
		}
	}
	if !foundImage {
		t.Fatalf("preview resources missed image: %#v", previewResources)
	}
}

func TestPreviewResourceMetadataFallsBackToRangeGET(t *testing.T) {
	pageRawURL := "https://preview.example/"
	imageURL := "https://assets.example/render?id=hero"
	sourceHTML := `<img src="` + imageURL + `">`
	rangeRequestHeaders := make(chan string, 1)

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			header := make(http.Header)
			if request.URL.String() != imageURL {
				return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
			}
			if request.Method == http.MethodHead {
				return &http.Response{StatusCode: http.StatusMethodNotAllowed, Status: "405 Method Not Allowed", Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
			}
			rangeRequestHeaders <- request.Header.Get("Range")
			header.Set("Content-Type", "image/jpeg")
			header.Set("Content-Range", "bytes 0-0/12345")
			return &http.Response{StatusCode: http.StatusPartialContent, Status: "206 Partial Content", Header: header, Body: io.NopCloser(strings.NewReader("x")), ContentLength: 1, Request: request}, nil
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 1 {
		t.Fatalf("preview resources = %#v, want one image", previewResources)
	}
	if previewResources[0].SizeBytes != 12345 || previewResources[0].Kind != "image" {
		t.Fatalf("preview image = %#v, want image with full range size", previewResources[0])
	}
	select {
	case rangeHeader := <-rangeRequestHeaders:
		if rangeHeader != "bytes=0-0" {
			t.Fatalf("Range = %q, want bytes=0-0", rangeHeader)
		}
	default:
		t.Fatal("metadata fallback did not send a range GET")
	}
}

func TestWholeSitePreviewProcesses594Pages(t *testing.T) {
	const pageCount = 594
	var startHTML strings.Builder
	startHTML.WriteString("<!doctype html><html><body>")
	for pageIndex := 1; pageIndex < pageCount; pageIndex++ {
		startHTML.WriteString(`<a href="/page/` + strconv.Itoa(pageIndex) + `">Page</a>`)
	}
	startHTML.WriteString("</body></html>")

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := "<!doctype html><html><body>Imported</body></html>"
			header := http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	startURL, parseErr := url.Parse("https://large.example/")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewContext, cancelPreview := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelPreview()
	preview := previewWholeRemoteSiteResources(previewContext, startURL, startHTML.String(), "/", nil, "", grabSourceOptions{})
	if preview.Partial {
		t.Fatal("594-page preview unexpectedly completed partially")
	}
	if preview.PageCount != pageCount || len(preview.ImportedPages) != pageCount {
		t.Fatalf("preview pages = %d/%d, want %d", preview.PageCount, len(preview.ImportedPages), pageCount)
	}
}

func TestDemoRefreshFailureKeepsExistingLandingPage(t *testing.T) {
	sourceURL := "https://source.example/"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL: {statusCode: http.StatusBadGateway, contentType: "text/plain", body: "upstream failed"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	application := newRouterTestApplication(t)
	controlDB := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer controlDB.Close()
	fallbackSettings := demo.Settings{Domain: "demo-preserve.example", Enabled: true}
	adminEmail, _, fallbackErr := application.ensureDemoSiteReady(context.Background(), fallbackSettings, false, "")
	if fallbackErr != nil || adminEmail == "" {
		t.Fatalf("create fallback demo: email=%q error=%v", adminEmail, fallbackErr)
	}
	var originalHTML string
	demoContext := contextWithDomain(context.Background(), fallbackSettings.Domain)
	if err := application.db.QueryRowContext(demoContext, `SELECT html FROM pages WHERE domain=? AND path='/'`, fallbackSettings.Domain).Scan(&originalHTML); err != nil {
		t.Fatal(err)
	}

	refreshSettings := fallbackSettings
	refreshSettings.SourceURL = sourceURL
	if _, _, refreshErr := application.ensureDemoSiteReady(context.Background(), refreshSettings, true, ""); refreshErr == nil {
		t.Fatal("demo refresh unexpectedly succeeded")
	}
	var retainedHTML string
	if err := application.db.QueryRowContext(demoContext, `SELECT html FROM pages WHERE domain=? AND path='/'`, fallbackSettings.Domain).Scan(&retainedHTML); err != nil {
		t.Fatal(err)
	}
	if retainedHTML != originalHTML {
		t.Fatal("failed demo refresh replaced the working landing page")
	}
}

func TestMirrorRemotePageRewritesDataManifestRelativeURLs(t *testing.T) {
	pageRawURL := "https://karman.cafe/"
	manifest := `{"name":"Karman","start_url":"/","icons":[{"src":"./assets/apple-touch-icon.png","sizes":"180x180","type":"image/png"}]}`
	sourceHTML := `<!doctype html><html><head><link rel="manifest" href="data:application/manifest+json,` + url.PathEscape(manifest) + `"></head><body>Imported</body></html>`
	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}

	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", nil, "")
	manifestHrefStart := strings.Index(importedHTML, `href="`)
	if manifestHrefStart < 0 {
		t.Fatalf("manifest href not found in imported HTML: %s", importedHTML)
	}
	manifestHrefStart += len(`href="`)
	manifestHrefEnd := strings.Index(importedHTML[manifestHrefStart:], `"`)
	if manifestHrefEnd < 0 {
		t.Fatalf("manifest href is not closed in imported HTML: %s", importedHTML)
	}
	manifestHref := importedHTML[manifestHrefStart : manifestHrefStart+manifestHrefEnd]
	_, manifestPayload, ok := crawler.SplitDataURL(manifestHref)
	if !ok {
		t.Fatalf("manifest href is not a data URL: %s", manifestHref)
	}
	decodedManifest, decodeErr := url.PathUnescape(manifestPayload)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	var manifestPayloadObject map[string]any
	if err := json.Unmarshal([]byte(decodedManifest), &manifestPayloadObject); err != nil {
		t.Fatalf("decode rewritten manifest: %v, payload=%s", err, decodedManifest)
	}
	if _, found := manifestPayloadObject["start_url"]; found {
		t.Fatalf("manifest keeps source-origin start_url: %#v", manifestPayloadObject["start_url"])
	}
	icons, ok := manifestPayloadObject["icons"].([]any)
	if !ok || len(icons) != 1 {
		t.Fatalf("manifest icons = %#v", manifestPayloadObject["icons"])
	}
	icon, ok := icons[0].(map[string]any)
	if !ok || icon["src"] != "https://karman.cafe/assets/apple-touch-icon.png" {
		t.Fatalf("manifest icon src = %#v", manifestPayloadObject["icons"])
	}
}

func TestNormalizeMirroredAssetReferenceCollapsesProtocolRelativeLocalPath(t *testing.T) {
	if normalizedReference := crawler.NormalizeMirroredAssetReference("//p/app.js"); normalizedReference != "/p/app.js" {
		t.Fatalf("normalized reference = %q", normalizedReference)
	}
	if normalizedReference := crawler.NormalizeMirroredAssetReference("/p/app.js"); normalizedReference != "/p/app.js" {
		t.Fatalf("stable reference = %q", normalizedReference)
	}
	if normalizedReference := crawler.NormalizeMirroredAssetReference("https://cdn.example/app.js"); normalizedReference != "https://cdn.example/app.js" {
		t.Fatalf("external reference = %q", normalizedReference)
	}
}

func TestMirrorRemotePagePersistsInlineJavaScriptGalleryImage(t *testing.T) {
	const pageRawURL = "https://twochicks.ru/products/klatch-transformer-black/"
	const galleryImageURL = "https://twochicks.ru/p/gallery-image.jpg"
	const galleryImageBody = "gallery-image-body"
	sourceHTML := `<script>const galleryImages = ['/p/gallery-image.jpg'];</script>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			galleryImageURL: {contentType: "image/jpeg", body: galleryImageBody},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 1 || previewResources[0].URL != galleryImageURL {
		t.Fatalf("gallery preview resources = %#v", previewResources)
	}
	selectedResourceURLs := map[string]struct{}{galleryImageURL: {}}
	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("gallery.example", "/product", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")

	expectedFileName, hashErr := contentHashName([]byte(galleryImageBody), ".jpg")
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	expectedAssetReference := "/p/" + expectedFileName
	if !strings.Contains(importedHTML, expectedAssetReference) {
		t.Fatalf("gallery image reference was not rewritten to %q: %s", expectedAssetReference, importedHTML)
	}
	storedImagePath := filepath.Join(application.domainFilesDirForDomain("gallery.example"), expectedFileName)
	storedImageBody, readErr := os.ReadFile(storedImagePath)
	if readErr != nil {
		t.Fatalf("read stored gallery image: %v", readErr)
	}
	if string(storedImageBody) != galleryImageBody {
		t.Fatalf("stored gallery image = %q", storedImageBody)
	}
}

func TestMirrorRemotePagePersistsImagesReferencedByExternalJavaScript(t *testing.T) {
	const pageRawURL = "https://shop.example/products/item/"
	const scriptURL = "https://shop.example/assets/gallery.js"
	const imageURL = "https://cdn.example/images/gallery.webp?width=1600&format=webp"
	const imageBody = "external-javascript-gallery-image"
	sourceHTML := `<script type="module" src="/assets/gallery.js"></script>`
	scriptBody := `const galleryImages = ["https:\/\/cdn.example\/images\/gallery.webp?width=1600\u0026format=webp"];`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			scriptURL: {contentType: "application/javascript", body: scriptBody},
			imageURL:  {contentType: "image/webp", body: imageBody},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	for _, expectedURL := range []string{scriptURL, imageURL} {
		if _, found := selectedResourceURLs[expectedURL]; !found {
			t.Fatalf("external JavaScript resource was not discovered: %q, resources = %#v", expectedURL, previewResources)
		}
	}

	application, _ := newTestApplication(t)
	_ = application.mirrorRemotePage("external-script.example", "/product", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	expectedImageFileName, hashErr := contentHashName([]byte(imageBody), ".webp")
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	storedImagePath := filepath.Join(application.domainFilesDirForDomain("external-script.example"), expectedImageFileName)
	if _, statErr := os.Stat(storedImagePath); statErr != nil {
		t.Fatalf("external JavaScript image was not persisted: %v", statErr)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("external-script.example"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedImageReference := "/p/" + expectedImageFileName
	rewrittenScriptFound := false
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) != ".js" {
			continue
		}
		storedScriptBody, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(storedScriptBody), expectedImageReference) {
			rewrittenScriptFound = true
		}
	}
	if !rewrittenScriptFound {
		t.Fatalf("stored external JavaScript does not reference %q", expectedImageReference)
	}
}

func TestDownloadGrabSourceHTMLUsesSelectedLanguageAndResolvedURL(t *testing.T) {
	requestedLanguageHeaders := make([]string, 0, 2)
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestedLanguageHeaders = append(requestedLanguageHeaders, request.Header.Get("Accept-Language"))
			switch request.URL.String() {
			case "https://lang.example/":
				return &http.Response{
					StatusCode: http.StatusFound,
					Status:     "302 Found",
					Header:     http.Header{"Location": []string{"/ru/index.html"}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    request,
				}, nil
			case "https://lang.example/ru/index.html":
				return &http.Response{
					StatusCode:    http.StatusOK,
					Status:        "200 OK",
					Header:        http.Header{"Content-Type": []string{"text/html"}},
					Body:          io.NopCloser(strings.NewReader("<html>ru</html>")),
					ContentLength: int64(len("<html>ru</html>")),
					Request:       request,
				}, nil
			default:
				t.Fatalf("unexpected URL requested: %s", request.URL.String())
				return nil, nil
			}
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	htmlBytes, resolvedURL, err := downloadGrabSourceHTMLWithResolvedURL("https://lang.example/", grabSourceOptions{LanguageCode: "ru"})
	if err != nil {
		t.Fatalf("download source HTML: %v", err)
	}
	if string(htmlBytes) != "<html>ru</html>" {
		t.Fatalf("html = %q", string(htmlBytes))
	}
	if resolvedURL.String() != "https://lang.example/ru/index.html" {
		t.Fatalf("resolved URL = %s", resolvedURL.String())
	}
	if len(requestedLanguageHeaders) < 2 {
		t.Fatalf("expected redirect request headers, got %#v", requestedLanguageHeaders)
	}
	for _, acceptLanguageHeader := range requestedLanguageHeaders {
		if !strings.HasPrefix(acceptLanguageHeader, "ru,ru-RU") {
			t.Fatalf("Accept-Language = %q", acceptLanguageHeader)
		}
	}
}

func TestDecodeImportedHTMLBytesDetectsWindows1251AndAddsUTF8Meta(t *testing.T) {
	sourceHTML := `<!doctype html><html><head><title>Искусство</title></head><body>Русский текст страницы</body></html>`
	sourceBytes, encodeErr := charmap.Windows1251.NewEncoder().Bytes([]byte(sourceHTML))
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}

	importedHTML := decodeImportedHTMLBytes(sourceBytes, "text/html")
	if !strings.Contains(importedHTML, "Русский текст страницы") {
		t.Fatalf("imported HTML was not decoded as readable text: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, `<meta charset="utf-8">`) {
		t.Fatalf("imported HTML is missing UTF-8 meta tag: %s", importedHTML)
	}
}

func TestRewriteImportedHTMLCharsetDeclarationAddsHeadWhenMissing(t *testing.T) {
	importedHTML := rewriteImportedHTMLCharsetDeclaration(`<html><body>Imported</body></html>`)
	if !strings.Contains(importedHTML, `<head><meta charset="utf-8"></head>`) {
		t.Fatalf("imported HTML is missing generated UTF-8 head: %s", importedHTML)
	}
}

func TestRewriteImportedHTMLCharsetDeclarationOnlyChangesMetaTags(t *testing.T) {
	sourceHTML := `<html><head><meta charset="windows-1251"><script>window.database = "charset=utf8mb4_unicode_ci";</script></head><body></body></html>`

	importedHTML := rewriteImportedHTMLCharsetDeclaration(sourceHTML)
	if !strings.Contains(importedHTML, `<meta charset="utf-8">`) {
		t.Fatalf("meta charset was not rewritten: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, `"charset=utf8mb4_unicode_ci"`) {
		t.Fatalf("JavaScript charset text was changed: %s", importedHTML)
	}
}

func TestRewriteImportedHTMLCharsetDeclarationUpdatesHTTPMeta(t *testing.T) {
	sourceHTML := `<html><head><meta http-equiv="Content-Type" content="text/html; charset=windows-1251"></head><body></body></html>`

	importedHTML := rewriteImportedHTMLCharsetDeclaration(sourceHTML)
	if !strings.Contains(importedHTML, `content="text/html; charset=utf-8"`) {
		t.Fatalf("HTTP meta charset was not rewritten: %s", importedHTML)
	}
}

func TestRewriteImportedHTMLCharsetDeclarationPreservesMalformedRemainder(t *testing.T) {
	sourceHTML := `<html><head><meta charset="windows-1251"></head><body><div title="unfinished`

	importedHTML := rewriteImportedHTMLCharsetDeclaration(sourceHTML)
	if !strings.HasSuffix(importedHTML, `<body><div title="unfinished`) {
		t.Fatalf("malformed HTML remainder was lost: %s", importedHTML)
	}
}

func TestMirrorRemotePageRemovesImportedHostLanguageRedirect(t *testing.T) {
	pageRawURL := "https://overmobile.example/"
	sourceHTML := `<!doctype html><html lang="ru"><head>` +
		`<script>if (window.location.hostname.includes('.com') && window.location.pathname === '/') { window.location.href = '/en/index.html'; }</script>` +
		`</head><body><main>Русская версия</main></body></html>`
	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}

	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/", pageRawURL, pageURL, sourceHTML, "", nil, "")
	if strings.Contains(importedHTML, "/en/index.html") || strings.Contains(strings.ToLower(importedHTML), "location.href") {
		t.Fatalf("host language redirect survived import: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "Русская версия") {
		t.Fatalf("imported content was lost: %s", importedHTML)
	}
}

func TestMirrorRemotePageDoesNotDoubleRewriteFirstInlineCSSImport(t *testing.T) {
	pageRawURL := "http://perftoran-archive.ru/"
	sourceHTML := `<!doctype html><html><head>` +
		`<style class="SiteBrush-Template perftoran-css-main-style" type="text/css">` +
		`@import url("/f/fb6473a435b5347875cbe04e61f91d17.css");  ` +
		`@import url("/f/166fbb8fd4a3f5207a500bdf6c2d9186.css");  ` +
		`@import url("/f/db93670dc2c4f8f877dbaabcf30b91d4.css");` +
		`</style></head><body>Imported</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"http://perftoran-archive.ru/f/fb6473a435b5347875cbe04e61f91d17.css": {contentType: "text/css", body: ".first{color:red}"},
			"http://perftoran-archive.ru/f/166fbb8fd4a3f5207a500bdf6c2d9186.css": {contentType: "text/css", body: ".second{color:green}"},
			"http://perftoran-archive.ru/f/db93670dc2c4f8f877dbaabcf30b91d4.css": {contentType: "text/css", body: ".third{color:blue}"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 3 {
		t.Fatalf("expected 3 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, "http://perftoran-archive.ru/p/") {
		t.Fatalf("first inline CSS import was double rewritten through source host: %s", importedHTML)
	}
	if strings.Count(importedHTML, `@import url("/p/`) != 3 {
		t.Fatalf("inline CSS imports were not all rewritten to local assets: %s", importedHTML)
	}
}

func TestMirrorRemotePageImportsImageAltResourceURLs(t *testing.T) {
	pageRawURL := "https://elburus.example/gallery"
	sourceHTML := `<!doctype html><html><body>` +
		`<img alt="/f/d79e6970493ed0c96a836dc2c35a0ae9.jpeg" src="/f/19fc3fc8d6ff9413f475b5b208e6cc37.jpeg">` +
		`<img alt="Станция Мир: 3500м Ледник" src="/f/thumb.jpeg">` +
		`<script>var imgL_source = $thumb.attr('alt');</script>` +
		`</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://elburus.example/f/d79e6970493ed0c96a836dc2c35a0ae9.jpeg": {contentType: "image/jpeg", body: "large"},
			"https://elburus.example/f/19fc3fc8d6ff9413f475b5b208e6cc37.jpeg": {contentType: "image/jpeg", body: "thumb"},
			"https://elburus.example/f/thumb.jpeg":                            {contentType: "image/jpeg", body: "plain-alt-thumb"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 3 {
		t.Fatalf("expected 3 preview resources, got %d: %#v", len(previewResources), previewResources)
	}
	largeImageFound := false
	for _, previewResource := range previewResources {
		if previewResource.URL == "https://elburus.example/f/d79e6970493ed0c96a836dc2c35a0ae9.jpeg" {
			largeImageFound = true
		}
		if strings.Contains(previewResource.URL, "%D0%A1%D1%82%D0%B0%D0%BD%D1%86%D0%B8%D1%8F") {
			t.Fatalf("plain alt text was treated as a resource: %#v", previewResources)
		}
	}
	if !largeImageFound {
		t.Fatalf("preview resources missed image URL from alt: %#v", previewResources)
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, "/f/d79e6970493ed0c96a836dc2c35a0ae9.jpeg") {
		t.Fatalf("imported HTML still references source image alt URL: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, `alt="/p/`) {
		t.Fatalf("imported HTML did not rewrite image alt URL to a local asset: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, `alt="Станция Мир: 3500м Ледник"`) {
		t.Fatalf("imported HTML rewrote plain alt text: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) != 3 {
		t.Fatalf("expected 3 stored images, got %d: %#v", len(storedFiles), storedFiles)
	}
}

func TestMirrorRemotePageImportsCrossDomainAssetsWithoutURLExtensions(t *testing.T) {
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><head>` +
		`<link rel="stylesheet" href="https://cdn.example/styles?id=42">` +
		`</head><body><img src="https://img.example/render?asset=hero"></body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://cdn.example/styles?id=42":      {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.example/open?id=7")} body{background:url("https://img.example/bg?asset=1")}`},
			"https://fonts.example/open?id=7":       {contentType: "font/woff2", body: "font"},
			"https://img.example/bg?asset=1":        {contentType: "image/png", body: "bg"},
			"https://img.example/render?asset=hero": {contentType: "image/png", body: "hero"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 4 {
		t.Fatalf("expected 4 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, "cdn.example") || strings.Contains(importedHTML, "img.example") || strings.Contains(importedHTML, "fonts.example") {
		t.Fatalf("imported HTML still references external extensionless assets: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "/p/") {
		t.Fatalf("imported HTML does not reference local assets: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedExtensions := map[string]bool{".css": false, ".woff2": false, ".png": false}
	for _, storedFilePath := range storedFiles {
		if _, tracked := expectedExtensions[filepath.Ext(storedFilePath)]; tracked {
			expectedExtensions[filepath.Ext(storedFilePath)] = true
		}
	}
	for expectedExtension, found := range expectedExtensions {
		if !found {
			t.Fatalf("expected imported extensionless asset with extension %s, stored files: %#v", expectedExtension, storedFiles)
		}
	}
}

func TestMirrorRemotePageImportsDocumentMediaAndArchiveLinks(t *testing.T) {
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><body>` +
		`<a href="https://cdn.example/download?id=manual">Manual</a>` +
		`<a href="https://cdn.example/archive.zip">Archive</a>` +
		`<a href="https://cdn.example/feed.json">Feed</a>` +
		`<video controls src="https://cdn.example/media/intro.mp4"></video>` +
		`<audio controls src="https://cdn.example/audio/theme.mp3"></audio>` +
		`</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://cdn.example/download?id=manual": {contentType: "application/pdf", body: "%PDF-1.7"},
			"https://cdn.example/archive.zip":        {contentType: "application/zip", body: "zip"},
			"https://cdn.example/feed.json":          {contentType: "application/json", body: `{"ok":true}`},
			"https://cdn.example/media/intro.mp4":    {contentType: "video/mp4", body: "mp4"},
			"https://cdn.example/audio/theme.mp3":    {contentType: "audio/mpeg", body: "mp3"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 5 {
		t.Fatalf("expected 5 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}

	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	for _, forbiddenFragment := range []string{"cdn.example/download?id=manual", "cdn.example/archive.zip", "cdn.example/feed.json", "cdn.example/media/intro.mp4", "cdn.example/audio/theme.mp3"} {
		if strings.Contains(importedHTML, forbiddenFragment) {
			t.Fatalf("imported HTML still references external resource %q: %s", forbiddenFragment, importedHTML)
		}
	}
	if strings.Count(importedHTML, `/p/`) < 5 {
		t.Fatalf("imported HTML does not contain local references for all resources: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedExtensions := map[string]bool{
		".pdf":  false,
		".zip":  false,
		".json": false,
		".mp4":  false,
		".mp3":  false,
	}
	for _, storedFilePath := range storedFiles {
		if _, tracked := expectedExtensions[filepath.Ext(storedFilePath)]; tracked {
			expectedExtensions[filepath.Ext(storedFilePath)] = true
		}
	}
	for expectedExtension, found := range expectedExtensions {
		if !found {
			t.Fatalf("expected imported resource with extension %s, stored files: %#v", expectedExtension, storedFiles)
		}
	}
}

func TestGrabPreviewReportsQuotaAndGrabRejectsOversizedImport(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	application.ensureDomainStorageUsageRow(context.Background(), "localhost")
	_, err = rawDB.Exec(`UPDATE domain_storage_usage SET limit_bytes=? WHERE domain=?`, 120, "localhost")
	if err != nil {
		t.Fatalf("update storage limit: %v", err)
	}

	sourceURL := "https://quota.example/page"
	sourceHTML := `<!doctype html><html><body><a href="https://quota.example/manual.pdf">Manual</a></body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL:                          {contentType: "text/html", body: sourceHTML},
			"https://quota.example/manual.pdf": {contentType: "application/pdf", body: strings.Repeat("P", 80)},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	previewForm := url.Values{}
	previewForm.Set("path", "/quota")
	previewForm.Set("source_url", sourceURL)
	previewRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/quota?grab_preview", strings.NewReader(previewForm.Encode()))
	previewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	previewRequest.Header.Set("Accept", "application/json")
	previewRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	previewResponse := httptest.NewRecorder()
	application.route(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%q", previewResponse.Code, previewResponse.Body.String())
	}

	var previewPayload grabPreviewResponse
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewPayload); err != nil {
		t.Fatalf("decode preview payload: %v", err)
	}
	if previewPayload.FitsQuota {
		t.Fatalf("expected preview to exceed quota, payload=%+v", previewPayload)
	}
	if previewPayload.ProjectedUsedBytes <= previewPayload.LimitBytes {
		t.Fatalf("expected projected usage %d to exceed limit %d", previewPayload.ProjectedUsedBytes, previewPayload.LimitBytes)
	}
	if previewPayload.SelectedResourceBytes < 80 {
		t.Fatalf("expected selected resource bytes to include pdf, payload=%+v", previewPayload)
	}

	grabForm := url.Values{}
	grabForm.Set("path", "/quota")
	grabForm.Set("source_url", sourceURL)
	grabRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/quota?grab", strings.NewReader(grabForm.Encode()))
	grabRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	grabRequest.Header.Set("Accept", "application/json")
	grabRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	grabResponse := httptest.NewRecorder()
	application.route(grabResponse, grabRequest)
	if grabResponse.Code != http.StatusInsufficientStorage {
		t.Fatalf("grab status = %d, body=%q", grabResponse.Code, grabResponse.Body.String())
	}
	if !strings.Contains(grabResponse.Body.String(), "storage limit reached:") {
		t.Fatalf("grab body does not mention storage limit: %q", grabResponse.Body.String())
	}
}

func TestGrabUsesRequestPathWhenPostedPathIsMissing(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	sourceURL := "https://fallback.example/page"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL: {contentType: "text/html", body: `<!doctype html><html><body>fallback path</body></html>`},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	grabForm := url.Values{}
	grabForm.Set("source_url", sourceURL)
	grabRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/fallback?grab", strings.NewReader(grabForm.Encode()))
	grabRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	grabRequest.Header.Set("Accept", "application/json")
	grabRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	grabResponse := httptest.NewRecorder()
	application.route(grabResponse, grabRequest)
	if grabResponse.Code != http.StatusOK {
		t.Fatalf("grab status = %d, body=%q", grabResponse.Code, grabResponse.Body.String())
	}

	var grabPayload struct {
		Redirect string `json:"redirect"`
	}
	if err := json.Unmarshal(grabResponse.Body.Bytes(), &grabPayload); err != nil {
		t.Fatalf("decode grab payload: %v", err)
	}
	if grabPayload.Redirect != "/fallback" {
		t.Fatalf("redirect = %q, want /fallback", grabPayload.Redirect)
	}
}

func TestGrabStoresPageAndRevisionAfterRequestConnectionIsLost(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	sourceURL := "https://kmv.ru/for-home.html"
	imageURL := "https://kmv.ru/images/for-home.jpg"
	requestContext, cancelRequest := context.WithCancel(context.Background())
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.String() {
			case sourceURL:
				return fakeGrabResponse{
					contentType: "text/html; charset=utf-8",
					body:        `<!doctype html><html><body><h1>Для дома</h1><img src="/images/for-home.jpg"></body></html>`,
				}.httpResponse(request), nil
			case imageURL:
				cancelRequest()
				return fakeGrabResponse{contentType: "image/jpeg", body: "image"}.httpResponse(request), nil
			default:
				return fakeGrabResponse{statusCode: http.StatusNotFound, body: "not found"}.httpResponse(request), nil
			}
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	grabForm := url.Values{}
	grabForm.Set("path", "/for-home.html")
	grabForm.Set("source_url", sourceURL)
	grabRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/for-home.html?grab", strings.NewReader(grabForm.Encode())).WithContext(requestContext)
	grabRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	grabRequest.Header.Set("Accept", "application/json")
	grabRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	grabResponse := httptest.NewRecorder()
	application.route(grabResponse, grabRequest)
	if grabResponse.Code != http.StatusOK {
		t.Fatalf("grab status = %d, body=%q", grabResponse.Code, grabResponse.Body.String())
	}

	var storedHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/for-home.html").Scan(&storedHTML); err != nil {
		t.Fatalf("read imported page: %v", err)
	}
	if !strings.Contains(storedHTML, "Для дома") {
		t.Fatalf("imported page HTML = %q", storedHTML)
	}
	var revisionCount int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM revisions WHERE domain=? AND page_path=?`, "localhost", "/for-home.html").Scan(&revisionCount); err != nil {
		t.Fatalf("count imported page revisions: %v", err)
	}
	if revisionCount != 1 {
		t.Fatalf("revision count = %d, want 1", revisionCount)
	}
}

func TestRewriteJSResourceReferencesLeavesLibraryCodeIntact(t *testing.T) {
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><body><script src="/js/app.js"></script></body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://page.example/js/app.js": {contentType: "application/javascript", body: `
				var analyticsURL = ('https:' == document.location.protocol ? 'https://ssl' : 'http://www') + '.google-analytics.com/ga.js';
				var selectorOperator = "*=";
				var imagePath = "/images/logo.png";
			`},
			"https://page.example/images/logo.png": {contentType: "image/png", body: "logo"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}

	application, _ := newTestApplication(t)
	application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	var storedScript string
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) != ".js" {
			continue
		}
		storedBytes, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		storedScript = string(storedBytes)
		break
	}
	if storedScript == "" {
		t.Fatalf("expected rewritten JS file, stored files: %#v", storedFiles)
	}
	if !strings.Contains(storedScript, `'.google-analytics.com/ga.js'`) {
		t.Fatalf("analytics suffix string was unexpectedly rewritten: %s", storedScript)
	}
	if !strings.Contains(storedScript, `"*="`) {
		t.Fatalf("selector operator string was unexpectedly rewritten: %s", storedScript)
	}
	if !strings.Contains(storedScript, `/p/`) {
		t.Fatalf("real JS asset path was not rewritten to local resource: %s", storedScript)
	}
}

func TestGrabPageCanCopyWholeExternalSiteUnderLocalPath(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	sourceBaseURL := "https://source.example"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceBaseURL + "/":                                         {contentType: "text/html", body: `<!doctype html><html><head><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=PT+Sans+Narrow&v1"><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=Monoton"><script src="/app.js"></script></head><body><a href="/about">About</a><a href="https://outside.example/x">Outside</a><img src="/images/logo.png"></body></html>`},
			sourceBaseURL + "/about":                                    {contentType: "text/html", body: `<!doctype html><html><body><a href="/">Home</a><a href="contact.html">Contact</a><img src="about.png"><iframe src="/contact.html"></iframe></body></html>`},
			sourceBaseURL + "/contact.html":                             {contentType: "text/html", body: `<!doctype html><html><body><a href="/about">About</a></body></html>`},
			sourceBaseURL + "/style.css":                                {contentType: "text/css", body: `@import url("/nested.css"); body{background:url("/images/bg.png")}`},
			sourceBaseURL + "/nested.css":                               {contentType: "text/css", body: `.nested{background:url("/fonts/font.woff2")}`},
			sourceBaseURL + "/app.js":                                   {contentType: "application/javascript", body: `import "/chunk.js"; const icon = "/icons/icon.svg";`},
			sourceBaseURL + "/chunk.js":                                 {contentType: "application/javascript", body: `console.log("chunk");`},
			sourceBaseURL + "/images/logo.png":                          {contentType: "image/png", body: "logo"},
			sourceBaseURL + "/images/bg.png":                            {contentType: "image/png", body: "bg"},
			sourceBaseURL + "/about.png":                                {contentType: "image/png", body: "about"},
			sourceBaseURL + "/fonts/font.woff2":                         {contentType: "font/woff2", body: "font"},
			sourceBaseURL + "/icons/icon.svg":                           {contentType: "image/svg+xml", body: "<svg/>"},
			"https://fonts.googleapis.com/css?family=PT+Sans+Narrow&v1": {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.gstatic.com/s/ptsansnarrow.woff2")}`},
			"https://fonts.googleapis.com/css?family=Monoton":           {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.gstatic.com/s/monoton.woff2")}`},
			"https://fonts.gstatic.com/s/ptsansnarrow.woff2":            {contentType: "font/woff2", body: "ptsans"},
			"https://fonts.gstatic.com/s/monoton.woff2":                 {contentType: "font/woff2", body: "monoton"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	previewForm := url.Values{}
	previewForm.Set("source_url", sourceBaseURL+"/")
	previewForm.Set("copy_whole_site", "1")
	previewForm.Set("progress_token", "preview-token")
	previewRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab_preview", strings.NewReader(previewForm.Encode()))
	previewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	previewRequest.Header.Set("Accept", "application/json")
	previewRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	previewResponse := httptest.NewRecorder()
	application.route(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("whole-site preview status = %d, body=%q", previewResponse.Code, previewResponse.Body.String())
	}
	var previewPayload grabPreviewResponse
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewPayload); err != nil {
		t.Fatalf("decode whole-site preview: %v", err)
	}
	if previewPayload.PageCount != 3 {
		t.Fatalf("whole-site preview page count = %d, want 3", previewPayload.PageCount)
	}
	if previewPayload.ResourceCount < 8 {
		t.Fatalf("whole-site preview resource count = %d, want at least 8: %#v", previewPayload.ResourceCount, previewPayload.Resources)
	}
	if previewPayload.DownloadTotal != previewPayload.PageCount+previewPayload.ResourceCount {
		t.Fatalf("whole-site preview download total = %d, want page + resource count %d", previewPayload.DownloadTotal, previewPayload.PageCount+previewPayload.ResourceCount)
	}
	if previewPayload.DownloadTotalBytes <= previewPayload.PageDownloadBytes {
		t.Fatalf("whole-site preview download bytes = %d, want more than page bytes %d", previewPayload.DownloadTotalBytes, previewPayload.PageDownloadBytes)
	}
	for _, previewResource := range previewPayload.Resources {
		if strings.Contains(previewResource.URL, "outside.example") {
			t.Fatalf("whole-site preview included external document link as resource: %#v", previewPayload.Resources)
		}
	}

	form := url.Values{}
	form.Set("path", "/URI")
	form.Set("source_url", sourceBaseURL+"/")
	form.Set("copy_whole_site", "1")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("whole-site import status = %d, body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"/URI"`) || strings.Contains(response.Body.String(), `?visual`) {
		t.Fatalf("whole-site import redirect does not point to local base path: %s", response.Body.String())
	}

	expectedPages := []string{"/URI", "/URI/about", "/URI/contact.html"}
	for _, expectedPagePath := range expectedPages {
		var pageHTML string
		if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", expectedPagePath).Scan(&pageHTML); err != nil {
			t.Fatalf("missing imported page %s: %v", expectedPagePath, err)
		}
		if strings.Contains(pageHTML, sourceBaseURL) {
			t.Fatalf("imported page %s still references source host: %s", expectedPagePath, pageHTML)
		}
	}

	var rootHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/URI").Scan(&rootHTML); err != nil {
		t.Fatalf("read root imported page: %v", err)
	}
	for _, expectedFragment := range []string{`href="/URI/about"`, `href="https://outside.example/x"`, `href="/URI/p/`, `src="/URI/p/`} {
		if !strings.Contains(rootHTML, expectedFragment) {
			t.Fatalf("root imported page missing %q in %s", expectedFragment, rootHTML)
		}
	}
	var aboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/URI/about").Scan(&aboutHTML); err != nil {
		t.Fatalf("read about imported page: %v", err)
	}
	for _, expectedFragment := range []string{`href="/URI"`, `href="/URI/contact.html"`, `src="/URI/contact.html"`} {
		if !strings.Contains(aboutHTML, expectedFragment) {
			t.Fatalf("about imported page missing %q in %s", expectedFragment, aboutHTML)
		}
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("localhost"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) < 8 {
		t.Fatalf("expected imported resources to be stored locally, got %d: %#v", len(storedFiles), storedFiles)
	}
	foundRewrittenJS := false
	foundRewrittenCSS := false
	for _, storedFilePath := range storedFiles {
		storedBytes, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		storedText := string(storedBytes)
		if filepath.Ext(storedFilePath) == ".js" && strings.Contains(storedText, "/URI/p/") {
			foundRewrittenJS = true
		}
		if filepath.Ext(storedFilePath) == ".css" && strings.Contains(storedText, "/URI/p/") {
			foundRewrittenCSS = true
		}
	}
	if !foundRewrittenJS {
		t.Fatalf("imported JS did not rewrite nested local resources: %#v", storedFiles)
	}
	if !foundRewrittenCSS {
		t.Fatalf("imported CSS did not rewrite nested local resources: %#v", storedFiles)
	}

	assetPrefixIndex := strings.Index(rootHTML, "/URI/p/")
	if assetPrefixIndex < 0 {
		t.Fatalf("root imported page does not contain base-prefixed asset path: %s", rootHTML)
	}
	assetPathEnd := assetPrefixIndex
	for assetPathEnd < len(rootHTML) && rootHTML[assetPathEnd] != '"' && rootHTML[assetPathEnd] != '\'' && rootHTML[assetPathEnd] != ' ' {
		assetPathEnd++
	}
	assetRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080"+rootHTML[assetPrefixIndex:assetPathEnd], nil)
	assetResponse := httptest.NewRecorder()
	application.route(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("base-prefixed asset status = %d, path=%q", assetResponse.Code, rootHTML[assetPrefixIndex:assetPathEnd])
	}
}

func TestGrabPageCanCopyWholeExternalSiteWithCrossDomainAssetsWithoutURLExtensions(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	sourceBaseURL := "https://source.example"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceBaseURL + "/":                     {contentType: "text/html", body: `<!doctype html><html><head><link rel="stylesheet" href="https://cdn.example/theme?id=5"></head><body><img src="https://img.example/render?asset=hero"><a href="/about">About</a></body></html>`},
			sourceBaseURL + "/about":                {contentType: "text/html", body: `<!doctype html><html><body><a href="/">Home</a></body></html>`},
			"https://cdn.example/theme?id=5":        {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.example/family?id=7")} body{background:url("https://img.example/bg?asset=1")}`},
			"https://fonts.example/family?id=7":     {contentType: "font/woff2", body: "font"},
			"https://img.example/bg?asset=1":        {contentType: "image/png", body: "bg"},
			"https://img.example/render?asset=hero": {contentType: "image/png", body: "hero"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	form := url.Values{}
	form.Set("path", "/URI")
	form.Set("source_url", sourceBaseURL+"/")
	form.Set("copy_whole_site", "1")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("whole-site import status = %d, body=%q", response.Code, response.Body.String())
	}

	var rootHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/URI").Scan(&rootHTML); err != nil {
		t.Fatalf("read root imported page: %v", err)
	}
	if strings.Contains(rootHTML, "cdn.example") || strings.Contains(rootHTML, "img.example") || strings.Contains(rootHTML, "fonts.example") {
		t.Fatalf("whole-site import still references external extensionless assets: %s", rootHTML)
	}
	for _, expectedFragment := range []string{`href="/URI/p/`, `src="/URI/p/`} {
		if !strings.Contains(rootHTML, expectedFragment) {
			t.Fatalf("whole-site import missing local asset fragment %q in %s", expectedFragment, rootHTML)
		}
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("localhost"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedExtensions := map[string]bool{".css": false, ".woff2": false, ".png": false}
	for _, storedFilePath := range storedFiles {
		if _, tracked := expectedExtensions[filepath.Ext(storedFilePath)]; tracked {
			expectedExtensions[filepath.Ext(storedFilePath)] = true
		}
	}
	for expectedExtension, found := range expectedExtensions {
		if !found {
			t.Fatalf("expected whole-site imported extensionless asset with extension %s, stored files: %#v", expectedExtension, storedFiles)
		}
	}
}

func TestGrabPageRewritesDynamicImageURLWithQueryAcrossWholeSiteImport(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	imageURL := "http://twochicks.ru/files/products/9ailniwq8ji.1000x.jpg?b1edcdeeffb231b04e127712bd1a9deb"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"http://twochicks.ru/":      {contentType: "text/html", body: `<!doctype html><html><body><a href="/about">About</a><img src="` + imageURL + `"></body></html>`},
			"http://twochicks.ru/about": {contentType: "text/html", body: `<!doctype html><html><body><script>window.productImage="` + imageURL + `";</script></body></html>`},
			imageURL:                    {contentType: "image/jpeg", body: "image-bytes"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	form := url.Values{}
	form.Set("path", "/URI")
	form.Set("source_url", "http://twochicks.ru/")
	form.Set("copy_whole_site", "1")
	form.Set("import_selection_confirmed", "1")
	form.Set("import_resource_url", imageURL)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("whole-site import status = %d, body=%q", response.Code, response.Body.String())
	}

	for _, pagePath := range []string{"/URI", "/URI/about"} {
		var pageHTML string
		if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", pagePath).Scan(&pageHTML); err != nil {
			t.Fatalf("read imported page %s: %v", pagePath, err)
		}
		if strings.Contains(pageHTML, imageURL) || strings.Contains(pageHTML, "?b1edcdeeffb231b04e127712bd1a9deb") {
			t.Fatalf("imported page %s still contains dynamic image URL: %s", pagePath, pageHTML)
		}
		if !strings.Contains(pageHTML, "/URI/p/") || !strings.Contains(pageHTML, ".jpg") {
			t.Fatalf("imported page %s did not use local hashed jpg asset: %s", pagePath, pageHTML)
		}
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("localhost"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	jpgCount := 0
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) == ".jpg" {
			jpgCount++
		}
	}
	if jpgCount != 1 {
		t.Fatalf("stored jpg count = %d, want 1: %#v", jpgCount, storedFiles)
	}
}

func TestGrabPageRedirectsToImportedPageView(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://source.example/page": {contentType: "text/html", body: "<!doctype html><html><body>Imported page</body></html>"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	form := url.Values{}
	form.Set("path", "/docs")
	form.Set("source_url", "https://source.example/page")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("grab status = %d, body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"/docs"`) || strings.Contains(response.Body.String(), `?visual`) {
		t.Fatalf("grab redirect should open imported page view, got %s", response.Body.String())
	}
}

func TestGrabHTTPClientVerifiesTLSCertificates(t *testing.T) {
	transport := newGrabHTTPTransport()
	if transport.TLSClientConfig == nil {
		t.Fatal("grab transport is missing TLS config")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("grab transport skips TLS verification")
	}
	client := newGrabHTTPClient()
	clientTransport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("grab client transport type = %T, want *http.Transport", client.Transport)
	}
	if clientTransport.TLSClientConfig == nil || clientTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("grab client transport skips TLS verification")
	}
}

func TestDownloadGrabSourceHTMLWithResolvedURLFallsBackToHTTP(t *testing.T) {
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Scheme {
			case "https":
				return nil, errors.New("tls: certificate verification failed")
			case "http":
				return &http.Response{
					StatusCode:    http.StatusOK,
					Status:        "200 OK",
					Body:          io.NopCloser(strings.NewReader("<!doctype html><html><body>fallback</body></html>")),
					ContentLength: int64(len("<!doctype html><html><body>fallback</body></html>")),
					Header:        make(http.Header),
					Request:       request,
				}, nil
			default:
				t.Fatalf("unexpected request scheme %q", request.URL.Scheme)
			}
			return nil, nil
		})}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	htmlBytes, resolvedURL, err := downloadGrabSourceHTMLWithResolvedURL("https://source.example/page", grabSourceOptions{})
	if err != nil {
		t.Fatalf("downloadGrabSourceHTMLWithResolvedURL failed: %v", err)
	}
	if !strings.Contains(string(htmlBytes), "<body>fallback</body>") {
		t.Fatalf("downloaded HTML = %q", string(htmlBytes))
	}
	if resolvedURL.String() != "http://source.example/page" {
		t.Fatalf("resolved URL = %q, want http://source.example/page", resolvedURL.String())
	}
}

func TestNormalizeURLRejectsSuspiciousDynamicReferences(t *testing.T) {
	pageURL, parseErr := url.Parse("https://example.com/page")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	spider := newPageSpider("", pageURL, grabResourceMaxDepth, nil, "", grabSourceOptions{})
	testCases := []string{
		"${l}",
		"+e.url+",
		"this.videoId",
		"videoId",
		"/assets/app.js?x=1+2",
	}
	for _, rawRef := range testCases {
		if normalizedURL, blocked := spider.normalizeURL(rawRef, pageURL); !blocked || normalizedURL != "" {
			t.Fatalf("normalizeURL(%q) = (%q, %v), want blocked", rawRef, normalizedURL, blocked)
		}
	}
}

func TestMirrorRemotePageBlanksSameOriginEmbeddedHTMLFrames(t *testing.T) {
	pageRawURL := "https://example.test/imported"
	sourceHTML := `<!doctype html><html><body><iframe src="/imported"></iframe><iframe src="https://www.youtube.com/embed/demo"></iframe></body></html>`

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}

	application := &App{storagePath: t.TempDir(), grabTracker: newGrabProgressTracker()}
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", nil, "")

	if strings.Contains(importedHTML, `src="https://example.test/imported"`) || strings.Contains(importedHTML, `src="/imported"`) {
		t.Fatalf("imported HTML still contains recursive same-origin iframe: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, `src="about:blank"`) {
		t.Fatalf("imported HTML did not blank recursive iframe: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "youtube.com/embed/demo") {
		t.Fatalf("imported HTML lost allowed external embed: %s", importedHTML)
	}
}

func TestMirrorRemotePageRemovesLegacySiteBrushMenuChrome(t *testing.T) {
	pageRawURL := "https://legacy.example/rotorway4"
	sourceHTML := `<!DOCTYPE html>
	<!-- Powered by SiteBrush | http://sitebrush.com/ -->
<html lang="en" id="SiteBrush">
<head>
<script src="https://legacy.example/p/js/jquery/jquery.js" type="text/javascript"></script>
<script type="text/javascript">
$.fn.contextMenu = function(id, options) { return this; };
jQuery(function($) { $('div.contextMenu').hide(); });
</script>
<style type="text/css">
.SiteBrushContextMenu { font-size:14px; }
.ContextMenuCopyright { font-size:10px; }
.SiteBrushMenu { visibility:hidden; }
</style>
<script type="text/javascript">
jQuery(document).ready(function($) { $('#SiteBrush').contextMenu('SiteBrushMenu', {}); });
</script>
</head>
<body>
<div style="visibility:hidden" class="contextMenu SiteBrushMenu" id="SiteBrushMenu">
<ul>
<li id="close" class="SiteBrushContextMenu">&nbsp;<img src="https://legacy.example/p/static/lock.png" /> <a href="https://legacy.example/rotorway4?login" class="SiteBrushContextMenu">Войти</a></li>
<li class="SiteBrushContextMenu ContextMenuCopyright"><a href="http://sitebrush.com" class="SiteBrushContextMenu ContextMenuCopyright">sitebrush</a></li>
</ul>
</div>
<main><img src="https://legacy.example/content.jpg"><script>$(function(){ window.pageReady = true; });</script></main>
</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://legacy.example/p/js/jquery/jquery.js": {contentType: "application/javascript", body: `window.jQuery = function(){}; window.$ = window.jQuery;`},
			"https://legacy.example/content.jpg":           {contentType: "image/jpeg", body: "image"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", grabSourceOptions{})
	if len(previewResources) != 2 {
		t.Fatalf("expected only content resources after legacy menu cleanup, got %d: %#v", len(previewResources), previewResources)
	}
	for _, previewResource := range previewResources {
		if strings.Contains(previewResource.URL, "lock.png") || strings.Contains(previewResource.URL, "?login") {
			t.Fatalf("legacy sitebrush menu resource leaked into preview: %#v", previewResources)
		}
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, rawDB := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("localhost", "/rotorway4", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	for _, forbiddenFragment := range []string{"Powered by SiteBrush", `id="SiteBrushMenu"`, "SiteBrushContextMenu", "ContextMenuCopyright", "rotorway4?login", "jqContextMenu", "$.fn.contextMenu"} {
		if strings.Contains(importedHTML, forbiddenFragment) {
			t.Fatalf("legacy sitebrush chrome fragment %q remained in imported HTML: %s", forbiddenFragment, importedHTML)
		}
	}
	for _, expectedFragment := range []string{"/p/", "window.pageReady = true"} {
		if !strings.Contains(importedHTML, expectedFragment) {
			t.Fatalf("imported HTML missing expected fragment %q: %s", expectedFragment, importedHTML)
		}
	}

	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/rotorway4", "/rotorway4", importedHTML)
	if err != nil {
		t.Fatalf("insert imported page: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/rotorway4/", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("serve imported page status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "SiteBrushMenuBox") {
		t.Fatalf("served imported page does not contain current sitebrush menu: %s", body)
	}
	if strings.Contains(body, `id="SiteBrushMenu"`) || strings.Contains(body, "rotorway4?login") {
		t.Fatalf("served imported page still exposes legacy sitebrush menu: %s", body)
	}
}

func TestParseGrabSourceURLAcceptsCommonURLForms(t *testing.T) {
	testCases := map[string]string{
		"https://example.com/path":   "https://example.com/path",
		"http://example.com/path":    "http://example.com/path",
		"sitebrush.com":              "https://sitebrush.com",
		"example.com/path":           "https://example.com/path",
		"//cdn.example.com/file.css": "https://cdn.example.com/file.css",
	}
	for rawSourceURL, expectedSourceURL := range testCases {
		parsedSourceURL, parseErr := parseGrabSourceURL(rawSourceURL)
		if parseErr != nil {
			t.Fatalf("parseGrabSourceURL(%q) failed: %v", rawSourceURL, parseErr)
		}
		if parsedSourceURL.String() != expectedSourceURL {
			t.Fatalf("parseGrabSourceURL(%q) = %q, want %q", rawSourceURL, parsedSourceURL.String(), expectedSourceURL)
		}
	}
}

func TestParseGrabSourceURLRejectsPrivateNetworks(t *testing.T) {
	for _, rawSourceURL := range []string{"127.0.0.1:8080/admin", "localhost:18080/admin", "http://[::1]/"} {
		if _, parseErr := parseGrabSourceURL(rawSourceURL); parseErr == nil {
			t.Fatalf("parseGrabSourceURL(%q) allowed a private target", rawSourceURL)
		}
	}
}

func TestParseGrabSourceURLUsesHTTPSDefaultWhenServerIPIsProvided(t *testing.T) {
	parsedSourceURL, parseErr := parseGrabSourceURLForServerIP("expired.example/page", "1.1.1.1")
	if parseErr != nil {
		t.Fatalf("parseGrabSourceURLForServerIP failed: %v", parseErr)
	}
	if parsedSourceURL.String() != "https://expired.example/page" {
		t.Fatalf("parsed URL = %q, want https://expired.example/page", parsedSourceURL.String())
	}
}

func TestParseGrabSourceURLUsesSourceIPPortWhenProvided(t *testing.T) {
	parsedSourceURL, parseErr := parseGrabSourceURLForServerIP("expired.example/page", "1.1.1.1:8080")
	if parseErr != nil {
		t.Fatalf("parseGrabSourceURLForServerIP failed: %v", parseErr)
	}
	if parsedSourceURL.String() != "http://expired.example:8080/page" {
		t.Fatalf("parsed URL = %q, want http://expired.example:8080/page", parsedSourceURL.String())
	}
}

func TestParseOptionalGrabSourceIPAcceptsPort(t *testing.T) {
	sourceIP, parseErr := parseOptionalGrabSourceIP("1.1.1.1:8080")
	if parseErr != nil {
		t.Fatalf("parseOptionalGrabSourceIP failed: %v", parseErr)
	}
	if sourceIP != "1.1.1.1:8080" {
		t.Fatalf("sourceIP = %q, want 1.1.1.1:8080", sourceIP)
	}
}

func TestParseOptionalGrabSourceIPRejectsPrivateAddress(t *testing.T) {
	if _, parseErr := parseOptionalGrabSourceIP("127.0.0.1:8080"); parseErr == nil {
		t.Fatal("private source_ip was allowed")
	}
}

func TestLogoutRedirectsToSameURIWithoutLogoutFlag(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	testCases := map[string]string{
		"http://localhost:8080/docs?logout":                "/docs",
		"http://localhost:8080/docs?logout=&files=":        "/docs?files=",
		"http://localhost:8080/overmobile/doc?a=1&logout=": "/overmobile/doc?a=1",
		"http://localhost:8080/?logout=&settings=&x=1":     "/?settings=&x=1",
	}
	for requestURL, expectedLocation := range testCases {
		request := httptest.NewRequest(http.MethodGet, requestURL, nil)
		request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
		response := httptest.NewRecorder()
		application.route(response, request)
		if response.Code != http.StatusFound {
			t.Fatalf("logout status for %s = %d, body=%q", requestURL, response.Code, response.Body.String())
		}
		if location := response.Header().Get("Location"); location != expectedLocation {
			t.Fatalf("logout location for %s = %q, want %q", requestURL, location, expectedLocation)
		}
	}
}

func TestDownloadGrabSourceHTMLCanDialSourceIPWithDomainHost(t *testing.T) {
	if _, downloadErr := downloadGrabSourceHTML("http://expired.example/page", "127.0.0.1"); downloadErr == nil {
		t.Fatal("download with private source IP was allowed")
	}
}

func TestDownloadGrabSourceHTMLUsesSourceIPPort(t *testing.T) {
	if _, downloadErr := downloadGrabSourceHTML("http://expired.example:8080/page", "169.254.169.254:8080"); downloadErr == nil {
		t.Fatal("download with private source IP and port was allowed")
	}
}

func TestMissingPageGrabFormIncludesSourceIPOverride(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing page status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{
		`<link href="/p/static/technical_pages.css" rel="stylesheet">`,
		`<body class="technical-page bg-body">`,
		`<img src="/p/static/sitebrush-app-icon.png" class="section-icon" alt="">`,
		`class="missing-page-secondary-field missing-page-source-ip-field"`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("missing page does not include admin technical-page fragment %q in %s", expectedFragment, body)
		}
	}
	if !strings.Contains(body, `name="source_ip"`) {
		t.Fatalf("missing page import form does not include source_ip field: %s", body)
	}
	if !strings.Contains(body, `name="copy_whole_site"`) {
		t.Fatalf("missing page import form does not include whole-site checkbox: %s", body)
	}
	if !strings.Contains(body, `body: buildGrabRequestBody()`) {
		t.Fatalf("missing page import script does not send urlencoded grab body: %s", body)
	}
	for _, expectedFragment := range []string{
		`let previewRequestInFlight = false;`,
		`function closeProgressStream()`,
		`function buildDownloadProgressModel(previewPayload)`,
		`function summarizeDownloadProgress(progressState)`,
		`downloadProgressModel = buildDownloadProgressModel(previewPayloadForDownload);`,
		`if (grabRequestInFlight || previewRequestInFlight)`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("missing page import script does not include progress fix fragment %q in %s", expectedFragment, body)
		}
	}
}

func TestStatusCapturingResponseWriterSupportsWebSocketHijack(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?grab_ws&token=test", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	baseWriter := &fakeHijackResponseWriter{header: make(http.Header), conn: &fakeHijackConn{}}
	wrappedWriter := &statusCapturingResponseWriter{ResponseWriter: baseWriter, statusCode: http.StatusOK}

	connection, err := upgradeToWebSocket(wrappedWriter, request)
	if err != nil {
		t.Fatalf("upgrade through logging writer failed: %v", err)
	}
	defer connection.Close()

	handshake := baseWriter.conn.String()
	if !strings.Contains(handshake, "HTTP/1.1 101 Switching Protocols") {
		t.Fatalf("handshake missing 101 response: %q", handshake)
	}
	if !strings.Contains(handshake, "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=") {
		t.Fatalf("handshake missing websocket accept header: %q", handshake)
	}
}

func TestPublicTrialPreviewStoreRetainsTerminalJobStatus(t *testing.T) {
	store := newPublicTrialPreviewStore()
	cancelSession, started := store.Start("preview-token")
	if !started || cancelSession == nil {
		t.Fatal("preview job was not started")
	}
	runningStatus, found := store.Status("preview-token")
	if !found || runningStatus.Status != "running" {
		t.Fatalf("running status = %#v, found=%t", runningStatus, found)
	}
	store.Save(publicTrialPreview{
		Token:         "preview-token",
		SourceURL:     "https://example.com/",
		ImportedPages: []wholeSiteImportedPage{{SourceURL: "https://example.com/", LocalPath: "/", HTML: "<h1>Ready</h1>"}},
		PageCount:     1,
	})
	runningPreview, previewFound := store.Get("preview-token")
	if !previewFound || len(runningPreview.ImportedPages) != 1 {
		t.Fatalf("running preview = %#v, found=%t", runningPreview, previewFound)
	}

	response := publicTrialPreviewResponse{Status: "done", SourceURL: "https://example.com/", PageCount: 594}
	store.Complete("preview-token", "done", response)
	terminalStatus, found := store.Status("preview-token")
	if !found || terminalStatus.Status != "done" || terminalStatus.Response.PageCount != 594 {
		t.Fatalf("terminal status = %#v, found=%t", terminalStatus, found)
	}
	if _, restarted := store.Start("preview-token"); restarted {
		t.Fatal("completed preview token started a duplicate job")
	}
}

func TestPublicTrialRunningPreviewIsUsable(t *testing.T) {
	preview := publicTrialPreview{
		SourceURL:     "https://example.com/",
		ImportedPages: []wholeSiteImportedPage{{SourceURL: "https://example.com/", LocalPath: "/", HTML: "<h1>Ready</h1>"}},
		PageCount:     1,
		RequiredBytes: 14,
		TotalBytes:    14,
	}
	response := publicTrialPreviewResponseFromPreview(preview, "running", "https://sitebrush.example/?trial_site_preview_frame", nil)
	if !response.Usable || !response.Refreshing || response.Status != "running" {
		t.Fatalf("running preview response = %#v", response)
	}
}

func TestPublicTrialPreviewResponseRequiresSinglePageFallback(t *testing.T) {
	preview := publicTrialPreview{
		SourceURL:          "https://example.com/",
		ImportedPages:      []wholeSiteImportedPage{{SourceURL: "https://example.com/", LocalPath: "/", HTML: "<h1>Ready</h1>"}},
		PageCount:          1,
		RequiredBytes:      14,
		TotalBytes:         14,
		CopyWholeSite:      true,
		SinglePageRequired: true,
	}
	response := publicTrialPreviewResponseFromPreview(preview, "partial", "", nil)
	if !response.Usable || !response.SinglePageRequired {
		t.Fatalf("single-page fallback response = %#v", response)
	}
}

func TestPublicTrialSinglePagePreviewDoesNotCrawlDocumentLinks(t *testing.T) {
	application, _ := newTestApplication(t)
	sourceURL := "https://source.example/landing"
	imageURL := "https://source.example/hero.png"
	linkedPageURL := "https://source.example/catalog"
	requestedURLs := make(map[string]int)
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestedURLs[request.URL.String()]++
			switch request.URL.String() {
			case sourceURL:
				return fakeGrabResponse{contentType: "text/html", body: `<html><body><a href="/catalog">Catalog</a><img src="/hero.png"></body></html>`}.httpResponse(request), nil
			case imageURL:
				return fakeGrabResponse{contentType: "image/png", body: "image"}.httpResponse(request), nil
			default:
				return fakeGrabResponse{statusCode: http.StatusNotFound, body: "not found"}.httpResponse(request), nil
			}
		})}
	}
	t.Cleanup(func() { newGrabHTTPClient = previousGrabHTTPClient })

	cancelSession, started := application.activePublicTrialPreviewStore().Start("single-page-preview")
	if !started {
		t.Fatal("single-page preview did not start")
	}
	application.runPublicTrialSitePreview("single-page-preview", sourceURL, "", nil, cancelSession, false, grabSourceOptions{})
	status, found := application.activePublicTrialPreviewStore().Status("single-page-preview")
	if !found || status.Status != "done" {
		t.Fatalf("single-page preview status = %#v, found=%t", status, found)
	}
	if status.Response.PageCount != 1 {
		t.Fatalf("single-page preview pages = %d, want 1", status.Response.PageCount)
	}
	if requestedURLs[linkedPageURL] != 0 {
		t.Fatalf("single-page preview requested linked document %q %d times", linkedPageURL, requestedURLs[linkedPageURL])
	}
	if requestedURLs[imageURL] == 0 {
		t.Fatalf("single-page preview did not inspect required image %q", imageURL)
	}
}

func TestPublicTrialFormUsesUnifiedCopyDialog(t *testing.T) {
	scriptBytes, readErr := embeddedWebFiles.ReadFile("web/static/site_copy.js")
	if readErr != nil {
		t.Fatal(readErr)
	}
	script := string(scriptBytes)
	for _, expectedFragment := range []string{
		"openCopySiteModal(unifiedConfiguration)",
		"previewQuery: 'trial_site_preview'",
		"downloadQuery: 'trial_site_create'",
		"webSocketQuery: 'trial_site_ws'",
		"appendHiddenField(formElement, 'unified_copy', '1')",
		"copyWholeSite: false",
		"progressReadyFallbackTimer = window.setTimeout(startRequestOnce, 1000)",
		"if (!readyCallbackWasCalled)",
		"startRequestOnce()",
		"if (previewPayload.single_page_required)",
		"wholeSiteElement.checked = false",
	} {
		if !strings.Contains(script, expectedFragment) {
			t.Fatalf("unified public trial script does not contain %q", expectedFragment)
		}
	}
}

func TestWholeSitePreviewStopsAtFreeByteLimitWithUsableFirstPage(t *testing.T) {
	startHTML := `<!doctype html><html><body><a href="/next">Next</a><img src="/huge.png"></body></html>`
	startURL, parseErr := url.Parse("https://limited.example/")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	requestedURLs := make(chan string, 16)
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestedURLs <- request.URL.String()
			header := make(http.Header)
			switch request.URL.String() {
			case "https://limited.example/huge.png":
				header.Set("Content-Type", "image/png")
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(strings.NewReader("")), ContentLength: 1024, Request: request}, nil
			case "https://limited.example/next":
				header.Set("Content-Type", "text/html")
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(strings.NewReader("<p>next</p>")), ContentLength: 11, Request: request}, nil
			default:
				return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
			}
		})}
	}
	t.Cleanup(func() { newGrabHTTPClient = previousGrabHTTPClient })

	preview := previewWholeRemoteSiteResourcesWithLimit(context.Background(), startURL, startHTML, "/", nil, "", grabSourceOptions{}, int64(len(startHTML)+100))
	if !preview.LimitReached || !preview.Partial {
		t.Fatalf("limited preview = limit reached %t, partial %t", preview.LimitReached, preview.Partial)
	}
	if len(preview.ImportedPages) != 1 || preview.ImportedPages[0].SourceURL != startURL.String() {
		t.Fatalf("limited preview pages = %#v, want usable first page", preview.ImportedPages)
	}
	nextRequests := 0
	for {
		select {
		case requestedURL := <-requestedURLs:
			if requestedURL == "https://limited.example/next" {
				nextRequests++
			}
		default:
			goto requestsDrained
		}
	}
requestsDrained:
	if nextRequests != 0 {
		t.Fatalf("preview continued crawling after free limit: next page requested %d times", nextRequests)
	}
}

func TestPublicTrialScriptUsesCanonicalImportProgress(t *testing.T) {
	scriptBytes, readErr := embeddedWebFiles.ReadFile("web/static/site_copy.js")
	if readErr != nil {
		t.Fatal(readErr)
	}
	script := string(scriptBytes)
	for _, expectedFragment := range []string{
		"function openCopySiteModal(configuration)",
		"new WebSocket(configuredWebSocketEndpoint(configuration, webSocketQuery",
		"previewQuery: 'trial_site_preview'",
		"downloadQuery: 'trial_site_create'",
		"cancelQuery: 'trial_site_preview_cancel'",
		"webSocketQuery: 'trial_site_ws'",
	} {
		if !strings.Contains(script, expectedFragment) {
			t.Fatalf("public trial script does not contain %q", expectedFragment)
		}
	}
	if strings.Contains(script, "EventSource") {
		t.Fatal("copy site progress must use WebSocket networking")
	}
}

func TestExternalSiteImportPrimaryActionsAreGreen(t *testing.T) {
	scriptBytes, readErr := embeddedWebFiles.ReadFile("web/static/site_copy.js")
	if readErr != nil {
		t.Fatal(readErr)
	}
	script := string(scriptBytes)
	for _, expectedFragment := range []string{
		"SiteBrushCopySiteButton SiteBrushCopySiteContinueButton SiteBrushCopySiteHidden",
		".SiteBrushCopySiteContinueButton{border-color:#2fbf71!important;background:#198754!important",
		".SiteBrushCopySiteContinueButton:hover{border-color:#48d589!important;background:#157347!important",
		"@media (max-width:640px){.SiteBrushCopySiteOverlay",
		"background:#1f362d!important;color:#fff!important",
		"function setCopySiteStatus(statusElement, statusText, statusKind)",
		"setCopySiteStatus(statusElement, previewError.message",
		"cancelButtonElement.classList.toggle('SiteBrushCopySiteContinueButton', finishImportMode)",
		"continueButtonElement.classList.toggle('SiteBrushCopySiteContinueButton', primaryAction)",
		"continueButtonElement.textContent = textFromConfig(configuration, 'retryRemaining', 'Retry remaining');\n      setContinueButtonPrimaryAction(false)",
	} {
		if !strings.Contains(script, expectedFragment) {
			t.Fatalf("external site import script does not contain %q", expectedFragment)
		}
	}

	templateBytes, readErr := embeddedWebFiles.ReadFile("web/missing.html")
	if readErr != nil {
		t.Fatal(readErr)
	}
	template := string(templateBytes)
	for _, expectedFragment := range []string{
		`/p/static/technical_pages.css?v={{.CompileVersion}}`,
		"btn btn-success sitebrush-import-primary-action",
		"cancelButtonElement.classList.toggle('btn-success', finishImportMode)",
		"cancelButtonElement.classList.toggle('btn-outline-secondary', !finishImportMode)",
		"cancelButtonElement.classList.toggle('sitebrush-import-primary-action', finishImportMode)",
		"continueButtonElement.classList.toggle('sitebrush-import-primary-action', primaryAction)",
		"continueButtonElement.textContent = uiText.retryRemaining || continueButtonOriginalText;\n    setContinueButtonPrimaryAction(false)",
		"setFinishImportButtonMode(true)",
	} {
		if !strings.Contains(template, expectedFragment) {
			t.Fatalf("external site import template does not contain %q", expectedFragment)
		}
	}

	styleBytes, readErr := embeddedWebFiles.ReadFile("web/static/technical_pages.css")
	if readErr != nil {
		t.Fatal(readErr)
	}
	style := string(styleBytes)
	for _, expectedFragment := range []string{
		".technical-page #grabProgressModal button.sitebrush-import-primary-action",
		"background: #198754 !important",
		"background: #157347 !important",
	} {
		if !strings.Contains(style, expectedFragment) {
			t.Fatalf("external site import stylesheet does not contain %q", expectedFragment)
		}
	}
}

func TestPublicTrialEmbedKeepsWidgetLiveAndVersioned(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://sitebrush.example/?expenses", nil)
	embedHTML := publicTrialSignupEmbedHTML(request, translationsForLanguageCode("ru"))
	if !strings.Contains(embedHTML, `data-sitebrush-live-asset`) {
		t.Fatalf("public trial embed does not preserve the live widget: %s", embedHTML)
	}
	if !strings.Contains(embedHTML, `src="https://sitebrush.example/p/static/site_copy.js?v=`+publicTrialWidgetScriptVersion()+`"`) {
		t.Fatalf("public trial embed does not version the widget: %s", embedHTML)
	}
	for _, localizedText := range []string{"Введите сайт, на котором надо запустить SiteBrush:", "Адрес сайта", "Проверить сайт", `"texts"`} {
		if !strings.Contains(embedHTML, localizedText) {
			t.Fatalf("public trial embed does not contain localized fallback %q: %s", localizedText, embedHTML)
		}
	}
}

func TestPublicTrialInterfaceIsCompleteForEveryLanguage(t *testing.T) {
	requiredKeys := []string{
		"billing_registration_allow_test_drive",
		"public_trial_embed_title",
		"public_trial_embed_hint",
		"public_trial_setup_hint",
		"public_trial_form_title",
		"public_trial_field_label",
		"public_trial_check_button",
		"public_trial_copy_first_page",
		"public_trial_oversize_result",
		"public_trial_partial_ready",
		"public_trial_partial_timeout",
		"public_trial_reconnecting",
		"public_trial_single_page_required",
		"public_trial_usable_while_checking",
		"public_trial_disabled_owner_domain",
		"public_trial_disabled_registration",
		"public_trial_disabled_permission",
		"public_trial_disabled_external_ip",
		"public_trial_disabled_wildcard_missing",
		"public_trial_disabled_wildcard_mismatch",
	}
	for languageCode, translations := range translationCatalog {
		for _, translationKey := range requiredKeys {
			if strings.TrimSpace(translations[translationKey]) == "" {
				t.Fatalf("language %s is missing %s", languageCode, translationKey)
			}
		}
	}
}

func TestSiteRequestInterfaceIsCompleteForEveryLanguage(t *testing.T) {
	requiredKeys := []string{
		"setup_request_intro",
		"setup_request_phone",
		"setup_request_details",
		"setup_request_understood",
		"setup_request_no_plans",
		"setup_request_submit",
	}
	for languageCode, translations := range translationCatalog {
		for _, translationKey := range requiredKeys {
			if strings.TrimSpace(translations[translationKey]) == "" {
				t.Fatalf("language %s is missing %s", languageCode, translationKey)
			}
		}
	}
}

func TestProfileEmailDeliveryInterfaceIsCompleteForEveryLanguage(t *testing.T) {
	requiredKeys := []string{
		"profile_password_code_status_not_sent",
		"profile_email_delivery_details_success",
		"profile_email_delivery_details_error",
		"profile_email_delivery_modal_code_label",
		"profile_email_delivery_modal_close",
		"profile_email_delivery_success_title",
		"profile_email_delivery_success_summary",
		"profile_email_delivery_success_code",
		"profile_email_delivery_success_description",
		"profile_email_delivery_success_next_title",
		"profile_email_delivery_success_next_text",
		"profile_email_delivery_error_title",
		"profile_email_delivery_error_summary",
		"profile_email_delivery_code_unknown",
		"profile_email_delivery_generic_description",
		"profile_email_delivery_fix_title",
		"profile_email_delivery_generic_fix",
	}
	for languageCode, translations := range translationCatalog {
		for _, translationKey := range requiredKeys {
			if strings.TrimSpace(translations[translationKey]) == "" {
				t.Fatalf("language %s is missing %s", languageCode, translationKey)
			}
		}
	}
}

func TestPublicTrialWildcardRequiresThreeRandomDomainsOnExternalIP(t *testing.T) {
	previousExternalIPLookup := lookupServerExternalIP
	previousIPLookup := lookupIPRecords
	t.Cleanup(func() {
		lookupServerExternalIP = previousExternalIPLookup
		lookupIPRecords = previousIPLookup
	})

	lookupServerExternalIP = func(context.Context) (string, error) { return "203.0.113.10", nil }
	lookedUpDomains := make(chan string, publicTrialWildcardProbeCount)
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		lookedUpDomains <- domain
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	availability := checkPublicTrialWildcard("example.com")
	if !availability.Enabled || availability.ExternalIP != "203.0.113.10" || availability.WildcardDomain != "*.example.com" {
		t.Fatalf("availability = %#v", availability)
	}
	domainSet := make(map[string]struct{}, publicTrialWildcardProbeCount)
	for probeIndex := 0; probeIndex < publicTrialWildcardProbeCount; probeIndex++ {
		probeDomain := <-lookedUpDomains
		if !strings.HasPrefix(probeDomain, "sitebrush-trial-check-") || !strings.HasSuffix(probeDomain, ".example.com") {
			t.Fatalf("unexpected wildcard probe domain %q", probeDomain)
		}
		domainSet[probeDomain] = struct{}{}
	}
	if len(domainSet) != publicTrialWildcardProbeCount {
		t.Fatalf("wildcard probes are not unique: %#v", domainSet)
	}

	lookupResponses := make(chan []net.IP, publicTrialWildcardProbeCount)
	lookupResponses <- []net.IP{net.ParseIP("203.0.113.10")}
	lookupResponses <- []net.IP{net.ParseIP("203.0.113.10")}
	lookupResponses <- []net.IP{net.ParseIP("198.51.100.20")}
	lookupIPRecords = func(string) ([]net.IP, error) { return <-lookupResponses, nil }
	availability = checkPublicTrialWildcard("example.com")
	if availability.Enabled || availability.ReasonKey != "public_trial_disabled_wildcard_mismatch" {
		t.Fatalf("mismatched wildcard availability = %#v", availability)
	}

	lookupServerExternalIP = func(context.Context) (string, error) { return "192.168.1.20", nil }
	availability = checkPublicTrialWildcard("example.com")
	if availability.Enabled || availability.ReasonKey != "public_trial_disabled_external_ip" {
		t.Fatalf("private external IP availability = %#v", availability)
	}
}

func TestPublicTrialAvailabilityWorkerCachesAndInvalidatesDNSChecks(t *testing.T) {
	previousExternalIPLookup := lookupServerExternalIP
	previousIPLookup := lookupIPRecords
	t.Cleanup(func() {
		lookupServerExternalIP = previousExternalIPLookup
		lookupIPRecords = previousIPLookup
	})

	externalIPLookups := make(chan struct{}, 4)
	domainLookups := make(chan string, publicTrialWildcardProbeCount*3)
	lookupServerExternalIP = func(context.Context) (string, error) {
		externalIPLookups <- struct{}{}
		return "203.0.113.10", nil
	}
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		domainLookups <- domain
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}

	stop := make(chan struct{})
	application := &App{publicTrialAvailability: startPublicTrialAvailabilityWorker(stop)}
	t.Cleanup(func() { close(stop) })
	disabledAvailability := application.publicTrialAvailabilityForSettings(context.Background(), "example.com", true, false)
	if disabledAvailability.Enabled || disabledAvailability.ReasonKey != "public_trial_disabled_permission" {
		t.Fatalf("availability without explicit permission = %#v", disabledAvailability)
	}
	if len(externalIPLookups) != 0 || len(domainLookups) != 0 {
		t.Fatalf("test drive without explicit permission performed network checks")
	}
	for requestIndex := 0; requestIndex < 2; requestIndex++ {
		availability := application.publicTrialAvailabilityForSettings(context.Background(), "example.com", true, true)
		if !availability.Enabled {
			t.Fatalf("cached availability %d = %#v", requestIndex, availability)
		}
	}
	if len(externalIPLookups) != 1 || len(domainLookups) != publicTrialWildcardProbeCount {
		t.Fatalf("cache did not coalesce probes: external=%d dns=%d", len(externalIPLookups), len(domainLookups))
	}

	application.invalidatePublicTrialAvailability()
	availability := application.publicTrialAvailabilityForSettings(context.Background(), "example.com", true, true)
	if !availability.Enabled {
		t.Fatalf("availability after invalidation = %#v", availability)
	}
	if len(externalIPLookups) != 2 || len(domainLookups) != publicTrialWildcardProbeCount*2 {
		t.Fatalf("invalidation did not refresh probes: external=%d dns=%d", len(externalIPLookups), len(domainLookups))
	}
}

func TestPublicTrialEndpointsAllowCredentialFreeCrossOriginEmbedding(t *testing.T) {
	previousExternalIPLookup := lookupServerExternalIP
	previousIPLookup := lookupIPRecords
	lookupServerExternalIP = func(context.Context) (string, error) { return "203.0.113.10", nil }
	lookupIPRecords = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil }
	t.Cleanup(func() {
		lookupServerExternalIP = previousExternalIPLookup
		lookupIPRecords = previousIPLookup
	})
	application := newRouterTestApplication(t)
	controlDatabase := setupBillingOwnerForTest(t, application, "sitebrush.example", "owner@sitebrush.example", true)
	if err := (hostingandsupport.Store{DB: controlDatabase}).SaveRegistrationSettings(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	if err := controlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := startServerControlDatabaseDispatcher(application.serverControlDBPath(), false)
	if err != nil {
		t.Fatal(err)
	}
	application.controlDatabase = dispatcher
	t.Cleanup(dispatcher.Close)

	request := httptest.NewRequest(http.MethodOptions, "https://sitebrush.example/?trial_site_preview", nil)
	request.Header.Set("Origin", "https://embedded.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request = request.WithContext(contextWithDomain(request.Context(), "sitebrush.example"))
	response := httptest.NewRecorder()
	application.route(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, body=%q", response.Code, response.Body.String())
	}
	if allowOrigin := response.Header().Get("Access-Control-Allow-Origin"); allowOrigin != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", allowOrigin)
	}
	if allowMethods := response.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(allowMethods, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods = %q", allowMethods)
	}
	if allowCredentials := response.Header().Get("Access-Control-Allow-Credentials"); allowCredentials != "" {
		t.Fatalf("credentialed cross-origin trial unexpectedly enabled: %q", allowCredentials)
	}
	getRequest := httptest.NewRequest(http.MethodGet, "https://sitebrush.example/?trial_site_texts", nil)
	getRequest.Header.Set("Origin", "https://embedded.example")
	getRequest.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	getRequest = getRequest.WithContext(contextWithDomain(getRequest.Context(), "sitebrush.example"))
	getResponse := httptest.NewRecorder()
	application.route(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("cross-origin texts status = %d, body=%q", getResponse.Code, getResponse.Body.String())
	}
	if allowOrigin := getResponse.Header().Get("Access-Control-Allow-Origin"); allowOrigin != "*" {
		t.Fatalf("cross-origin texts Access-Control-Allow-Origin = %q, want *", allowOrigin)
	}
	if !strings.Contains(getResponse.Body.String(), "Проверить сайт") {
		t.Fatalf("cross-origin texts do not follow the visitor language: %s", getResponse.Body.String())
	}

	nonTrialRequest := httptest.NewRequest(http.MethodGet, "https://sitebrush.example/", nil)
	nonTrialResponse := httptest.NewRecorder()
	if application.preparePublicTrialEndpoint(nonTrialResponse, nonTrialRequest, publicTrialEndpointFromRequest(nonTrialRequest)) {
		t.Fatal("ordinary page was handled as a public trial endpoint")
	}
	if allowOrigin := nonTrialResponse.Header().Get("Access-Control-Allow-Origin"); allowOrigin != "" {
		t.Fatalf("ordinary page received trial CORS header %q", allowOrigin)
	}
}

func TestExpiredPublicTrialCleanupKeepsRegisteredSiteAndDeletesAnonymousSite(t *testing.T) {
	application := newRouterTestApplication(t)
	controlDatabase := setupBillingOwnerForTest(t, application, "sitebrush.example", "owner@sitebrush.example", true)
	store := hostingandsupport.Store{DB: controlDatabase}
	plan, found := store.DefaultPlan(context.Background())
	if !found {
		plans := store.Plans(context.Background())
		if len(plans) == 0 {
			t.Fatal("public trial cleanup test requires a service plan")
		}
		plan = plans[0]
	}
	oldTimestamp := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	for _, trialDomain := range []string{"anonymous.sitebrush.example", "registered.sitebrush.example"} {
		if err := application.createManagedSiteWithoutAdmin(context.Background(), trialDomain, defaultDomainStorageLimitBytes); err != nil {
			t.Fatalf("create %s: %v", trialDomain, err)
		}
		if err := store.AssignSite(context.Background(), trialDomain, plan.ID, "trial"); err != nil {
			t.Fatalf("assign %s: %v", trialDomain, err)
		}
		if _, err := controlDatabase.ExecContext(context.Background(), `UPDATE site_service_assignments SET updated_at=? WHERE domain=?`, oldTimestamp, trialDomain); err != nil {
			t.Fatalf("age %s: %v", trialDomain, err)
		}
	}
	if err := controlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := startServerControlDatabaseDispatcher(application.serverControlDBPath(), false)
	if err != nil {
		t.Fatal(err)
	}
	application.controlDatabase = dispatcher
	t.Cleanup(dispatcher.Close)

	registeredContext := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), "registered.sitebrush.example"))
	if _, err := application.db.ExecContext(registeredContext, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "registered.sitebrush.example", "admin@example.com", "password"); err != nil {
		t.Fatal(err)
	}
	application.cleanupExpiredPublicTrialSite(context.Background(), "anonymous.sitebrush.example")
	application.cleanupExpiredPublicTrialSite(context.Background(), "registered.sitebrush.example")

	anonymousPath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), "anonymous.sitebrush.example.db")
	if _, err := os.Stat(anonymousPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anonymous expired trial still exists: %v", err)
	}
	registeredPath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), "registered.sitebrush.example.db")
	if _, err := os.Stat(registeredPath); err != nil {
		t.Fatalf("registered trial was deleted: %v", err)
	}
	if err := application.withServerControlDatabaseRead(context.Background(), "verify-trial-cleanup", func(database *sql.DB) error {
		var anonymousCount int
		if queryErr := database.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM site_service_assignments WHERE domain=?`, "anonymous.sitebrush.example").Scan(&anonymousCount); queryErr != nil {
			return queryErr
		}
		if anonymousCount != 0 {
			return fmt.Errorf("anonymous assignment count = %d, want 0", anonymousCount)
		}
		var registeredCount int
		if queryErr := database.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM site_service_assignments WHERE domain=?`, "registered.sitebrush.example").Scan(&registeredCount); queryErr != nil {
			return queryErr
		}
		if registeredCount != 1 {
			return fmt.Errorf("registered assignment count = %d, want 1", registeredCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSimplifiedExpensesShowsTestDriveUnderRegistrationAfterExplicitPermission(t *testing.T) {
	templateBytes, err := fs.ReadFile(embeddedWebFiles, "web/expenses.html")
	if err != nil {
		t.Fatal(err)
	}
	parsedTemplate, err := template.New("expenses.html").Parse(string(templateBytes))
	if err != nil {
		t.Fatal(err)
	}
	view := map[string]any{
		"Domain":                     "sitebrush.example",
		"Title":                      "Server expenses",
		"T":                          translationsForLanguageCode("en"),
		"SimplifiedExpenses":         true,
		"ExpenseServers":             []simplifiedExpenseServerView{},
		"ShowCentralRegistry":        false,
		"DemoSettings":               demo.Settings{},
		"DemoStatus":                 demoSiteStatusView{},
		"DemoCopyScopeLabel":         "Content to download",
		"DemoCopyPageLabel":          "Only the specified page",
		"DemoSnapshotLabel":          "Current demo copy",
		"DemoSnapshotMissingLabel":   "The copy has not been created yet",
		"DemoLastRestoredLabel":      "Last content reset",
		"DemoNeverRestoredLabel":     "No resets yet",
		"AutoRegistrationEnabled":    false,
		"PublicTrialEnabled":         false,
		"PublicTrialAvailable":       false,
		"PublicTrialUnavailableText": "Enable automatic registration and wildcard DNS.",
		"PublicTrialEmbedHTML":       "<form data-sitebrush-public-trial-form></form>",
	}
	var rendered bytes.Buffer
	if err := parsedTemplate.Execute(&rendered, view); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expectedFragment := range []string{
		`data-hosting-installation-tab="settings"`,
		`data-site-settings-tab="general"`,
		`data-site-settings-tab="demo"`,
		`data-site-settings-panel="general"`,
		`data-site-settings-panel="demo"`,
		`name="auto_registration_enabled"`,
		`name="public_trial_enabled"`,
		`name="demo_site_enabled"`,
		`function selectSiteSettingsTab(tabName)`,
		`selectSiteSettingsTab(tabButtonElement.dataset.siteSettingsTab)`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("simplified settings missing %q", expectedFragment)
		}
	}
	if strings.Contains(body, `data-public-trial-disabled-section`) || strings.Contains(body, `id="publicTrialEmbedHTML"`) || strings.Contains(body, `id="publicTrialPreviewModal"`) {
		t.Fatalf("test drive without explicit permission rendered DNS instructions, form or preview: %s", body)
	}

	view["AutoRegistrationEnabled"] = true
	view["PublicTrialEnabled"] = true
	var unavailableRendered bytes.Buffer
	if err := parsedTemplate.Execute(&unavailableRendered, view); err != nil {
		t.Fatal(err)
	}
	unavailableBody := unavailableRendered.String()
	if !strings.Contains(unavailableBody, `data-public-trial-disabled-section`) || strings.Contains(unavailableBody, `id="publicTrialEmbedHTML"`) {
		t.Fatalf("permitted test drive without DNS did not render only its DNS instructions: %s", unavailableBody)
	}

	view["PublicTrialAvailable"] = true
	var availableRendered bytes.Buffer
	if err := parsedTemplate.Execute(&availableRendered, view); err != nil {
		t.Fatal(err)
	}
	availableBody := availableRendered.String()
	if !strings.Contains(availableBody, `id="publicTrialEmbedHTML"`) || !strings.Contains(availableBody, `id="publicTrialPreviewModal"`) || strings.Contains(availableBody, `data-public-trial-disabled-section`) {
		t.Fatalf("enabled test drive did not render only its active controls: %s", availableBody)
	}
}

func TestRussianCertificateRenewalControlsAreTranslated(t *testing.T) {
	translations := translationsForLanguageCode("ru")
	for translationKey, expectedText := range map[string]string{
		"domain_settings_ssl_renew_now":         "Продлить сертификат сейчас",
		"domain_settings_ssl_renew_connecting":  "Запускаем продление сертификата...",
		"domain_settings_ssl_renewed":           "Сертификат продлён.",
		"domain_settings_ssl_connection_closed": "Соединение закрылось до завершения продления сертификата.",
	} {
		if translations[translationKey] != expectedText {
			t.Fatalf("%s = %q, want %q", translationKey, translations[translationKey], expectedText)
		}
	}
}

func TestLegacyPublicTrialWidgetAssetServesCurrentScript(t *testing.T) {
	scriptBytes, readErr := embeddedWebFiles.ReadFile("web/static/site_copy.js")
	if readErr != nil {
		t.Fatal(readErr)
	}
	application := &App{embeddedStaticAssets: map[string]embeddedStaticAsset{
		"site_copy.js": {body: scriptBytes, contentType: "text/javascript; charset=utf-8"},
	}}
	request := httptest.NewRequest(http.MethodGet, "https://sitebrush.com/p/"+legacyPublicTrialWidgetAssetName, nil)
	request = request.WithContext(contextWithDomain(request.Context(), "sitebrush.com"))
	response := httptest.NewRecorder()
	application.servePublicAsset(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy widget status = %d, body=%q", response.Code, response.Body.String())
	}
	if !bytes.Equal(response.Body.Bytes(), scriptBytes) {
		t.Fatal("legacy widget URL did not serve the current embedded script")
	}
	if cacheControl := response.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("legacy widget Cache-Control = %q", cacheControl)
	}
}

func TestGuestPageRewritesLegacyPublicTrialWidgetReference(t *testing.T) {
	legacyHTML := `<html><body><script src="/p/` + legacyPublicTrialWidgetAssetName + `"></script></body></html>`
	rewrittenHTML := string(buildGuestStaticHTMLBody([]byte(legacyHTML), "/", "sitebrush.com", "en"))
	if strings.Contains(rewrittenHTML, legacyPublicTrialWidgetAssetName) {
		t.Fatalf("guest page keeps legacy trial widget: %s", rewrittenHTML)
	}
	if !strings.Contains(rewrittenHTML, `/p/static/site_copy.js?v=`+publicTrialWidgetScriptVersion()) {
		t.Fatalf("guest page does not load current trial widget: %s", rewrittenHTML)
	}
}

func TestStatusCapturingResponseWriterSupportsFlush(t *testing.T) {
	baseWriter := httptest.NewRecorder()
	wrappedWriter := &statusCapturingResponseWriter{ResponseWriter: baseWriter, statusCode: http.StatusOK}
	flusher, ok := any(wrappedWriter).(http.Flusher)
	if !ok {
		t.Fatal("logging response writer does not expose http.Flusher")
	}

	flusher.Flush()
	if !baseWriter.Flushed {
		t.Fatal("flush was not forwarded to the wrapped response writer")
	}
}

func TestPublicImportedAssetsServeFromCanonicalAndDomainPrefixedPaths(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	canonicalDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "asset.png"), []byte("canonical"), 0o644); err != nil {
		t.Fatal(err)
	}

	canonicalRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/asset.png", nil)
	canonicalResponse := httptest.NewRecorder()
	application.servePublicAsset(canonicalResponse, canonicalRequest)
	if canonicalResponse.Code != http.StatusOK {
		t.Fatalf("canonical asset status = %d, want 200", canonicalResponse.Code)
	}
	if canonicalResponse.Body.String() != "canonical" {
		t.Fatalf("canonical asset body = %q", canonicalResponse.Body.String())
	}

	legacyDir := filepath.Join(canonicalDir, "p")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "legacy.png"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/localhost/p/legacy.png", nil)
	legacyResponse := httptest.NewRecorder()
	application.route(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusOK {
		t.Fatalf("legacy asset status = %d, want 200", legacyResponse.Code)
	}
	if legacyResponse.Body.String() != "legacy" {
		t.Fatalf("legacy asset body = %q", legacyResponse.Body.String())
	}
}

func TestGuestStaticRouteServesPublishedFileWithoutDatabase(t *testing.T) {
	storagePath := t.TempDir()
	application := &App{db: panicSQLExecutor{t: t}, storagePath: storagePath}
	staticFilePath := filepath.Join(application.domainStaticDir("localhost"), "en.html")
	if err := os.MkdirAll(filepath.Dir(staticFilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staticFilePath, []byte("<html><body>fast</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/en/", nil)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{"<html><body>fast", "initializeSitebrushContextMenuForGuests", "href='?login'", "/p/static/login.png"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("guest static body missing %q in %s", expectedFragment, body)
		}
	}
	for _, unexpectedFragment := range []string{"href='?visual'", "href='?analytics'", "SiteBrushMenuStorageUsage"} {
		if strings.Contains(body, unexpectedFragment) {
			t.Fatalf("guest static body contains admin fragment %q in %s", unexpectedFragment, body)
		}
	}
	if response.Header().Get("X-Sitebrush-Source") != "static" {
		t.Fatalf("source header = %q, want static", response.Header().Get("X-Sitebrush-Source"))
	}
}

func TestGuestContextMenuStandardMenuHintIsMouseOnlyAndTranslated(t *testing.T) {
	for languageCode, translations := range translationCatalog {
		if strings.TrimSpace(translations["menu_standard_context_hint"]) == "" {
			t.Fatalf("missing standard context-menu hint translation for %s", languageCode)
		}
	}

	body := buildGuestContextMenuScriptForLanguage("/", "localhost", "en")
	for _, expectedFragment := range []string{
		"Browser standard menu: Ctrl + right mouse click.",
		"buildSitebrushGuestMenuEntries(sitebrushContextMenuWasOpenedByMouse(browserEvent))",
		"buildSitebrushGuestMenuEntries(false)",
		"function sitebrushContextMenuWasOpenedByMouse(browserEvent)",
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("guest context menu missing %q in %s", expectedFragment, body)
		}
	}
}

func TestSimplifiedExpenseLabelsExistInEveryLanguage(t *testing.T) {
	requiredKeys := []string{
		"expenses_server_select",
		"expenses_monthly_server_price",
		"expenses_automatic_price_per_gb",
		"expenses_gb_short",
		"expenses_purpose_simple",
		"expenses_disk_unknown",
		"expenses_paid_capacity",
		"expenses_allocated_month",
		"expenses_paid_and_uncovered",
		"expenses_billing_capacity",
		"expenses_capacity_reserve_hint",
		"expenses_capacity_exceeded",
		"expenses_clients_simple_hint",
		"expenses_client_share",
	}
	for languageCode, translations := range translationCatalog {
		for _, requiredKey := range requiredKeys {
			if strings.TrimSpace(translations[requiredKey]) == "" {
				t.Fatalf("missing %s translation for %s", requiredKey, languageCode)
			}
		}
	}
}

func TestGuestStaticAnalyticsAvoidsDatabaseAndEnqueuesEvent(t *testing.T) {
	storagePath := t.TempDir()
	application := &App{db: panicSQLExecutor{t: t}, storagePath: storagePath, analyticsEvents: make(chan siteAnalyticsEvent, 1)}
	staticFilePath := filepath.Join(application.domainStaticDir("localhost"), "index.html")
	if err := os.MkdirAll(filepath.Dir(staticFilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staticFilePath, []byte("<html><body>cached</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	response := httptest.NewRecorder()
	application.analyticsMiddleware(http.HandlerFunc(application.route)).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", response.Code, response.Body.String())
	}
	select {
	case event := <-application.analyticsEvents:
		if event.ContentSource != "static" {
			t.Fatalf("content source = %q, want static", event.ContentSource)
		}
		if event.Domain != "localhost" {
			t.Fatalf("domain = %q, want localhost", event.Domain)
		}
	default:
		t.Fatal("guest static request did not enqueue analytics event")
	}
}

func TestFilesPageUsesPublicAssetPrefix(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "55a5.jpg"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?files", nil)
	files, err := application.listManagedFiles(request.Context(), request, "/")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("listed files = %#v, want one file", files)
	}
	files[0].AssetPath = "/p/" + files[0].Name
	if files[0].AssetPath != "/p/55a5.jpg" {
		t.Fatalf("asset path = %q, want /p/55a5.jpg", files[0].AssetPath)
	}
}

func TestManagedFilesVisibleForCurrentURIAndDescendants(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, fileName := range []string{"docs.png", "child.png", "other.png", "legacy.png"} {
		if err := os.WriteFile(filepath.Join(domainDir, fileName), []byte(fileName), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	application.upsertFileMetadata(context.Background(), "localhost", "docs.png", "/docs", 8, "image/png", "test")
	application.upsertFileMetadata(context.Background(), "localhost", "child.png", "/docs/child", 9, "image/png", "test")
	application.upsertFileMetadata(context.Background(), "localhost", "other.png", "/other", 9, "image/png", "test")

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	files, err := application.listManagedFiles(request.Context(), request, "/docs")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}

	names := make(map[string]bool)
	for _, file := range files {
		names[file.Name] = true
	}
	if !names["docs.png"] || !names["child.png"] {
		t.Fatalf("expected docs branch files, got %#v", names)
	}
	if names["other.png"] || names["legacy.png"] {
		t.Fatalf("unexpected out-of-scope files, got %#v", names)
	}
}

func TestFilesPageDoesNotAutoLoadImageAssets(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "imported.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	application.upsertFileMetadata(context.Background(), "localhost", "imported.png", "/docs", 3, "image/png", "import")

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("files page status = %d, body=%q", response.Code, response.Body.String())
	}

	body := response.Body.String()
	if strings.Contains(body, `<img class="file-thumb" src="/p/imported.png"`) {
		t.Fatalf("files page still auto-loads imported image asset: %s", body)
	}
	for _, expectedFragment := range []string{`class="file-preview-trigger"`, `data-preview-src="/p/imported.png"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("files page missing %q in %s", expectedFragment, body)
		}
	}
	for _, expectedFragment := range []string{`copy-link-group`, `width:clamp(280px, 42vw, 680px)`, `height:24px`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("files page missing compact adaptive link UI %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, `files_download_count`) || strings.Contains(body, `Скачиваний:`) || strings.Contains(body, `Downloads:`) {
		t.Fatalf("files page still renders download counters: %s", body)
	}
}

func TestFileManagerMobileLayoutUsesCardsWithoutHorizontalScrolling(t *testing.T) {
	templateBytes, err := fs.ReadFile(embeddedWebFiles, "web/files.html")
	if err != nil {
		t.Fatal(err)
	}
	templateText := string(templateBytes)
	for _, expectedFragment := range []string{
		`@media (max-width: 900px)`,
		`.file-list-panel .table-responsive { overflow:visible;`,
		`.file-list-panel tr[data-file-row] { display:grid;`,
		`grid-template-areas:"preview file file" "size size date" "access access access" "actions actions actions"`,
		`data-label="{{index $.T "files_col_size"}}"`,
		`data-label="{{index $.T "files_col_access"}}"`,
		`class="file-access-details" data-file-access-details`,
		`accessDetailsElement.open = !compactFileLayout`,
		`background:#fafbfb`,
		`background:#24282a`,
		`class="file-delete-form"`,
	} {
		if !strings.Contains(templateText, expectedFragment) {
			t.Fatalf("mobile file manager layout is missing %q", expectedFragment)
		}
	}
}

func TestAssetServingCountsDownloadsAndTokenUse(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "token.png"), []byte("token image"), 0o644); err != nil {
		t.Fatal(err)
	}
	application.upsertFileMetadata(context.Background(), "localhost", "token.png", "/docs", 11, "image/png", "test")
	_, err = rawDB.Exec(`INSERT INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count) VALUES(?,?,?,?,?,?,?)`, "localhost", "token.png", "token", "abc", "", 0, 0)
	if err != nil {
		t.Fatalf("insert token rule: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/token.png?token=abc", nil)
	response := httptest.NewRecorder()
	application.servePublicAsset(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("token asset status = %d, want 200", response.Code)
	}

	var downloadCount int64
	if err := rawDB.QueryRow(`SELECT download_count FROM file_metadata WHERE domain=? AND file_name=?`, "localhost", "token.png").Scan(&downloadCount); err != nil {
		t.Fatalf("read download count: %v", err)
	}
	if downloadCount != 1 {
		t.Fatalf("download count = %d, want 1", downloadCount)
	}

	var tokenUseCount int64
	if err := rawDB.QueryRow(`SELECT token_use_count FROM file_access_rules WHERE domain=? AND file_name=?`, "localhost", "token.png").Scan(&tokenUseCount); err != nil {
		t.Fatalf("read token count: %v", err)
	}
	if tokenUseCount != 1 {
		t.Fatalf("token use count = %d, want 1", tokenUseCount)
	}
}

func TestPublicAssetServingDoesNotWriteDownloadCountOnHotPath(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "public.svg"), []byte("<svg></svg>"), 0o644); err != nil {
		t.Fatal(err)
	}
	application.upsertFileMetadata(context.Background(), "localhost", "public.svg", "/docs", 11, "image/svg+xml", "test")

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/public.svg", nil)
	response := httptest.NewRecorder()
	application.servePublicAsset(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("public asset status = %d, want 200", response.Code)
	}

	var downloadCount int64
	if err := rawDB.QueryRow(`SELECT download_count FROM file_metadata WHERE domain=? AND file_name=?`, "localhost", "public.svg").Scan(&downloadCount); err != nil {
		t.Fatalf("read download count: %v", err)
	}
	if downloadCount != 0 {
		t.Fatalf("download count = %d, want public hot path to avoid database writes", downloadCount)
	}
}

func TestUploadFilesStoresFilesForCurrentURI(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	fileWriter, err := multipartWriter.CreateFormFile("upload_files", "manual.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write([]byte("manual upload")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.WriteField("action", "upload"); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?files", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()

	application.uploadFiles(response, request, "/docs")
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%q", response.Code, response.Body.String())
	}

	storedPath := filepath.Join(application.domainFilesDirForDomain("localhost"), "manual.txt")
	storedBytes, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(storedBytes) != "manual upload" {
		t.Fatalf("stored file = %q", string(storedBytes))
	}

	var pagePath string
	if err := rawDB.QueryRow(`SELECT page_path FROM file_metadata WHERE domain=? AND file_name=?`, "localhost", "manual.txt").Scan(&pagePath); err != nil {
		t.Fatalf("read upload metadata: %v", err)
	}
	if pagePath != "/docs" {
		t.Fatalf("uploaded page path = %q, want /docs", pagePath)
	}
	if !strings.Contains(response.Body.String(), "manual.txt") {
		t.Fatalf("upload response does not include filename: %q", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "/p/manual.txt") {
		t.Fatalf("upload response does not include public path: %q", response.Body.String())
	}
}

func TestFilesPageUploadButtonOpensPickerAndSelectedFilesUploadImmediately(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("files status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	expectedSnippets := []string{
		`const currentFilesPath = "/docs";`,
		"requestFileSelection();",
		"uploadInputElement.addEventListener('change'",
		"uploadSelectedFiles(Array.from(uploadInputElement.files));",
		"request.open('POST', currentFilesPath + '?files');",
		"currentFilesPath + '?native_pick_files'",
	}
	for _, expectedSnippet := range expectedSnippets {
		if !strings.Contains(body, expectedSnippet) {
			t.Fatalf("files page does not contain %q", expectedSnippet)
		}
	}
	if strings.Contains(body, "new FormData(uploadFormElement)") {
		t.Fatalf("files page still posts the whole form instead of selected file list")
	}
}

func TestUploadFilesRejectsWhenDomainStorageLimitIsExceeded(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT OR REPLACE INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"localhost", 0, 0, 0, 0, 0, 5, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed storage usage: %v", err)
	}

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	fileWriter, err := multipartWriter.CreateFormFile("upload_files", "manual.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write([]byte("manual upload")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.WriteField("action", "upload"); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?files", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()

	application.uploadFiles(response, request, "/docs")
	if response.Code != http.StatusInsufficientStorage {
		t.Fatalf("upload status = %d, body=%q", response.Code, response.Body.String())
	}
	if _, statErr := os.Stat(filepath.Join(application.domainFilesDirForDomain("localhost"), "manual.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file was stored despite storage limit: %v", statErr)
	}
}

func TestStorageLimitErrorShowsCurrentOperationAndProjectedUsage(t *testing.T) {
	application, rawDB := newTestApplication(t)
	currentHTML := strings.Repeat("A", 80)
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/", "/", currentHTML); err != nil {
		t.Fatal(err)
	}
	application.ensureDomainStorageUsageRow(context.Background(), "localhost")
	if _, err := rawDB.Exec(`UPDATE domain_storage_usage SET limit_bytes=? WHERE domain=?`, 100, "localhost"); err != nil {
		t.Fatal(err)
	}

	storageErr := application.applyDomainStorageDelta(context.Background(), "localhost", 0, 0, 30, 0, 0)
	if storageErr == nil {
		t.Fatal("storage update unexpectedly fitted into the quota")
	}
	for _, expectedFragment := range []string{
		"storage limit reached:",
		"80 B used",
		"30 B required by this operation",
		"110 B projected",
		"100 B limit",
	} {
		if !strings.Contains(storageErr.Error(), expectedFragment) {
			t.Fatalf("storage error %q does not contain %q", storageErr.Error(), expectedFragment)
		}
	}
}

func TestVisualEditorUsesLocalJoditAssetsAndServerImageUpload(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "<p>docs</p>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?visual", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("visual editor status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`href="/p/static/jodit.min.css"`, `src="/p/static/jodit.min.js"`, "/p/static/files.png", "/p/static/save-page.png", "/p/static/exit-editor.png", "chooseAndUploadFiles", "document.body.appendChild(fileInputElement)", "fallbackHashFileName", "sitebrush.visualUploadResizeMode", `value="600"`, `value="800"`, `value="1200"`, `value="2000"`, "width=", "height=", "currentPagePath + '?files'", "currentPagePath + '?native_pick_files'", "window.location.href = currentPagePath", "parseVisualEditorDocument", "visualEditorStylesheetLinks", "syncVisualEditorTheme", "resizeVisualEditorWorkspace", "storedVisualEditorHTML", "iframe: true", "iframeCSSLinks: visualEditorStylesheetLinks(initialHtmlContent)"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("visual editor missing %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "visualEditorPreviewPane") || strings.Contains(body, "editor-preview-pane") {
		t.Fatalf("visual editor should not contain text-editor preview tabs: %s", body)
	}
	if strings.Contains(body, "cdn.jsdelivr.net") {
		t.Fatalf("visual editor still references CDN: %s", body)
	}
}

func TestRawTextEditorHasDraftPreviewAndHistory(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "<h1>docs</h1>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?text", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("raw editor status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`href="/p/static/codemirror/codemirror.min.css"`, `src="/p/static/codemirror/codemirror.min.js"`, `src="/p/static/codemirror/htmlmixed.min.js"`, `src="/p/static/codemirror/css.min.js"`, `src="/p/static/codemirror/javascript.min.js"`, `src="/p/static/codemirror/xml.min.js"`, "rawEditorEditTab", "rawEditorPreviewTab", "rawPreviewFrame", "rawEditorPreviewHTML", "setRawEditorView", "initializeRawEditorCodeMirror", "rawEditorModeName", "newlineAndIndent", "indentSelection('add')", "undoRawEditorDraft", "redoRawEditorDraft", "pushRawEditorHistory", "rawEditorTextArea", ">Редактор<", ">Предпросмотр<", "История черновика"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("raw editor missing %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "cdn.jsdelivr.net") || strings.Contains(body, "cdnjs.cloudflare.com") {
		t.Fatalf("raw editor still references CDN: %s", body)
	}
}

func TestNormalizeDomainNameAcceptsBareDomainsAndRejectsInvalidNames(t *testing.T) {
	testCases := map[string]string{
		"sitebrush.com":                  "sitebrush.com",
		"https://www.sitebrush.com/path": "www.sitebrush.com",
		"www.sitebrush.com:443":          "www.sitebrush.com",
		" localhost ":                    "",
		"127.0.0.1":                      "",
		"bad domain.com":                 "",
	}
	for rawDomain, expectedDomain := range testCases {
		actualDomain := normalizeDomainName(rawDomain)
		if actualDomain != expectedDomain {
			t.Fatalf("normalizeDomainName(%q) = %q, want %q", rawDomain, actualDomain, expectedDomain)
		}
	}
}

func TestDomainFromRequestCanonicalizesLoopbackHosts(t *testing.T) {
	testCases := map[string]string{
		"localhost:8080": "localhost",
		"127.0.0.1:8080": "localhost",
		"[::1]:8080":     "localhost",
		"EXAMPLE.com:80": "example.com",
	}
	for hostHeader, expectedDomain := range testCases {
		request := httptest.NewRequest(http.MethodGet, "http://"+hostHeader+"/", nil)
		actualDomain := domainFromRequest(request)
		if actualDomain != expectedDomain {
			t.Fatalf("domainFromRequest(%q) = %q, want %q", hostHeader, actualDomain, expectedDomain)
		}
	}
}

func TestMigrateMergesLegacyLoopbackDomainsIntoLocalhost(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "127.0.0.1", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert loopback user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "127.0.0.1", "/docs", "/docs", "<p>docs</p>")
	if err != nil {
		t.Fatalf("insert loopback page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO domain_states(domain,is_frozen) VALUES(?,?)`, "127.0.0.1", 1)
	if err != nil {
		t.Fatalf("insert loopback domain state: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at) VALUES(?,?,?,?,?,?,?)`,
		"127.0.0.1", "example.com", "token", 1, 1, 0, "")
	if err != nil {
		t.Fatalf("insert loopback alias: %v", err)
	}

	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}

	var migratedUsers int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain='localhost' AND email='admin@example.com'`).Scan(&migratedUsers); err != nil {
		t.Fatalf("count migrated users: %v", err)
	}
	if migratedUsers != 1 {
		t.Fatalf("migrated users = %d, want 1", migratedUsers)
	}
	var legacyUsers int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain='127.0.0.1' AND email='admin@example.com'`).Scan(&legacyUsers); err != nil {
		t.Fatalf("count legacy users: %v", err)
	}
	if legacyUsers != 0 {
		t.Fatalf("legacy users = %d, want 0", legacyUsers)
	}

	var pageDomain string
	if err := rawDB.QueryRow(`SELECT domain FROM pages WHERE path='/docs'`).Scan(&pageDomain); err != nil {
		t.Fatalf("select migrated page: %v", err)
	}
	if pageDomain != "localhost" {
		t.Fatalf("migrated page domain = %q, want localhost", pageDomain)
	}

	var aliasPrimaryDomain string
	if err := rawDB.QueryRow(`SELECT primary_domain FROM domain_aliases WHERE alias_domain='example.com'`).Scan(&aliasPrimaryDomain); err != nil {
		t.Fatalf("select migrated alias: %v", err)
	}
	if aliasPrimaryDomain != "localhost" {
		t.Fatalf("migrated alias primary_domain = %q, want localhost", aliasPrimaryDomain)
	}
}

func TestDomainAliasesRequireDNSVerificationBeforeResolving(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	previousTXTLookup := lookupTXTRecords
	previousIPLookup := lookupIPRecords
	defer func() {
		lookupTXTRecords = previousTXTLookup
		lookupIPRecords = previousIPLookup
	}()

	lookupTXTRecords = func(string) ([]string, error) {
		return nil, os.ErrNotExist
	}
	lookupIPRecords = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}

	addForm := url.Values{}
	addForm.Set("action", "add_alias")
	addForm.Set("alias_domain", "example.com")
	addRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(addForm.Encode()))
	addRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(addRequest.Context(), addRequest, "localhost", "203.0.113.10")

	inactiveRequest := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if domain := application.siteDomain(inactiveRequest.Context(), inactiveRequest); domain != "example.com" {
		t.Fatalf("inactive alias resolved to %q, want request domain", domain)
	}

	var verificationToken string
	if err := rawDB.QueryRow(`SELECT verification_token FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, "localhost", "example.com").Scan(&verificationToken); err != nil {
		t.Fatalf("read verification token: %v", err)
	}
	lookupTXTRecords = func(string) ([]string, error) {
		return []string{"sitebrush=" + verificationToken}, nil
	}

	checkForm := url.Values{}
	checkForm.Set("action", "check_alias")
	checkForm.Set("alias_domain", "example.com")
	checkRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(checkForm.Encode()))
	checkRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(checkRequest.Context(), checkRequest, "localhost", "203.0.113.10")

	activeRequest := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if domain := application.siteDomain(activeRequest.Context(), activeRequest); domain != "localhost" {
		t.Fatalf("active alias resolved to %q, want primary domain", domain)
	}

	selectForm := url.Values{}
	selectForm.Set("action", "select_alias")
	selectForm.Set("alias_domain", "example.com")
	selectRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(selectForm.Encode()))
	selectRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(selectRequest.Context(), selectRequest, "localhost", "203.0.113.10")

	var selectedCount int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=? AND alias_domain=? AND is_selected=1`, "localhost", "example.com").Scan(&selectedCount); err != nil {
		t.Fatalf("read selected alias: %v", err)
	}
	if selectedCount != 1 {
		t.Fatalf("selected aliases = %d, want 1", selectedCount)
	}
}

func TestPerSiteDBRouterRoutesActiveAliasRequestsToPrimarySiteDatabase(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, domain string) error {
		application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
		return application.migrate(contextWithDomain(migrationCtx, domain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)

	primaryDomain := "twochicks.sitebrush.com"
	primaryContext := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), primaryDomain))
	if _, err := router.ExecContext(primaryContext, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, primaryDomain, "admin@twochicks.test", "hash"); err != nil {
		t.Fatalf("insert primary admin: %v", err)
	}
	if _, err := router.ExecContext(primaryContext, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, primaryDomain, "/", "Home", "<h1>Home</h1>"); err != nil {
		t.Fatalf("insert primary page: %v", err)
	}
	if _, err := router.ExecContext(primaryContext, `INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at) VALUES(?,?,?,?,?,?,?)`,
		primaryDomain, "twochicks.ru", "token", 1, 1, 1, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert active alias: %v", err)
	}

	application := &App{db: router, siteDatabaseRouter: router, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	certificateDomains := application.listAutomaticSSLDomainCandidates(context.Background())
	if !slices.Contains(certificateDomains, primaryDomain) || !slices.Contains(certificateDomains, "twochicks.ru") {
		t.Fatalf("automatic SSL domains = %v, want primary and alias from site database", certificateDomains)
	}
	request := httptest.NewRequest(http.MethodGet, "http://twochicks.ru/", nil)
	request = request.WithContext(contextWithDomain(request.Context(), "twochicks.ru"))
	if domain := application.siteDomain(request.Context(), request); domain != primaryDomain {
		t.Fatalf("siteDomain for active alias = %q, want %q", domain, primaryDomain)
	}
	page, err := application.findPage(request.Context(), primaryDomain, "/")
	if err != nil {
		t.Fatalf("find primary page through alias context: %v", err)
	}
	if page.HTML != "<h1>Home</h1>" {
		t.Fatalf("alias page HTML = %q, want primary content", page.HTML)
	}
}

func TestPerSiteDBRouterSerializesConcurrentWritesToOneSiteDatabase(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, domain string) error {
		application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
		return application.migrate(contextWithDomain(migrationCtx, domain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)

	domain := "serial.example"
	domainContext := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), domain))
	siteDatabase, err := router.databaseForContext(domainContext)
	if err != nil {
		t.Fatalf("open site database: %v", err)
	}
	if got := siteDatabase.rawDatabase.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("site database max open connections = %d, want 1", got)
	}
	if got := len(siteDatabase.readDatabases); got != siteDatabaseReaderCount {
		t.Fatalf("site database readers = %d, want %d", got, siteDatabaseReaderCount)
	}
	for readerIndex, readDatabase := range siteDatabase.readDatabases {
		if got := readDatabase.Stats().MaxOpenConnections; got != 1 {
			t.Fatalf("site database reader %d max open connections = %d, want 1", readerIndex, got)
		}
	}

	writeCount := 96
	writeResults := make(chan error, writeCount)
	for writeIndex := 0; writeIndex < writeCount; writeIndex++ {
		go func(index int) {
			pagePath := fmt.Sprintf("/page-%03d", index)
			_, execErr := router.ExecContext(domainContext, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pagePath, "<p>ok</p>")
			writeResults <- execErr
		}(writeIndex)
	}
	for writeIndex := 0; writeIndex < writeCount; writeIndex++ {
		if execErr := <-writeResults; execErr != nil {
			t.Fatalf("concurrent write %d failed: %v", writeIndex, execErr)
		}
	}

	var storedPages int
	if err := router.QueryRowContext(domainContext, `SELECT COUNT(1) FROM pages WHERE domain=?`, domain).Scan(&storedPages); err != nil {
		t.Fatalf("count stored pages: %v", err)
	}
	if storedPages != writeCount {
		t.Fatalf("stored pages = %d, want %d", storedPages, writeCount)
	}
}

func TestPerSiteDBRouterMigratesUnopenedDatabaseDuringAliasScan(t *testing.T) {
	storagePath := t.TempDir()
	dbPath := filepath.Join(storagePath, defaultDBPath)
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	primaryDomain := "primary.example"
	aliasDomain := "www.primary.example"
	primaryDatabasePath := filepath.Join(siteDatabaseDir, domainStorageName(primaryDomain)+".db")
	if err := ensureParentDir(primaryDatabasePath); err != nil {
		t.Fatal(err)
	}
	rawPrimaryDB, err := sql.Open("sqlite", "file:"+primaryDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawPrimaryDB.Exec(`CREATE TABLE domain_aliases(primary_domain TEXT,alias_domain TEXT UNIQUE,is_verified INTEGER,dns_a_ok INTEGER)`); err != nil {
		t.Fatalf("create old alias table: %v", err)
	}
	if _, err := rawPrimaryDB.Exec(`INSERT INTO domain_aliases(primary_domain,alias_domain,is_verified,dns_a_ok) VALUES(?,?,1,1)`, primaryDomain, aliasDomain); err != nil {
		t.Fatalf("insert old alias: %v", err)
	}
	_ = rawPrimaryDB.Close()

	router := newPerSiteDBRouter(siteDatabaseDir, "localhost", func(migrationCtx context.Context, rawDB *sql.DB, domain string) error {
		application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
		return application.migrate(contextWithDomain(migrationCtx, domain))
	}, false)
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Fatalf("close site database router: %v", err)
		}
	})
	waitSiteDBRouterStartup(t, router)

	siteDatabase, err := router.databaseForContext(contextWithDomain(context.Background(), aliasDomain))
	if err != nil {
		t.Fatalf("route alias database: %v", err)
	}
	if siteDatabase.path != primaryDatabasePath {
		t.Fatalf("alias routed to %q, want primary database %q", siteDatabase.path, primaryDatabasePath)
	}
	var verificationToken string
	if err := router.QueryRowContext(contextWithDomain(context.Background(), aliasDomain), `SELECT COALESCE(verification_token,'') FROM domain_aliases WHERE alias_domain=?`, aliasDomain).Scan(&verificationToken); err != nil {
		t.Fatalf("read migrated alias column: %v", err)
	}
}

func TestAuthoritativeDNSLookupFollowsDelegationWithoutRecursiveResolver(t *testing.T) {
	previousExchangeDNSMessage := exchangeDNSMessage
	defer func() {
		exchangeDNSMessage = previousExchangeDNSMessage
	}()

	queriedNameServers := make(chan string, 32)
	exchangeDNSMessage = func(_ context.Context, nameServerIP string, domainName dnsmessage.Name, recordType dnsmessage.Type) (dnsmessage.Message, error) {
		queriedNameServers <- nameServerIP
		switch nameServerIP {
		case authoritativeDNSRootServers[0]:
			return dnsmessage.Message{
				Header: dnsmessage.Header{RCode: dnsmessage.RCodeSuccess},
				Authorities: []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: mustDNSNameForTest("example.com."), Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET},
					Body:   &dnsmessage.NSResource{NS: mustDNSNameForTest("ns1.example.net.")},
				}},
				Additionals: []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: mustDNSNameForTest("ns1.example.net."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
					Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 53}},
				}},
			}, nil
		case "192.0.2.53":
			if domainName.String() != "alias.example.com." || recordType != dnsmessage.TypeTXT {
				return dnsmessage.Message{}, os.ErrInvalid
			}
			return dnsmessage.Message{
				Header: dnsmessage.Header{RCode: dnsmessage.RCodeSuccess},
				Answers: []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: mustDNSNameForTest("alias.example.com."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET},
					Body:   &dnsmessage.TXTResource{TXT: []string{"sitebrush=token"}},
				}},
			}, nil
		default:
			return dnsmessage.Message{}, os.ErrNotExist
		}
	}

	txtRecords, err := lookupAuthoritativeTXTRecords("alias.example.com")
	if err != nil {
		t.Fatalf("lookupAuthoritativeTXTRecords: %v", err)
	}
	if len(txtRecords) != 1 || txtRecords[0] != "sitebrush=token" {
		t.Fatalf("TXT records = %#v, want sitebrush token", txtRecords)
	}
	sawRootServer := false
	sawAuthoritativeServer := false
	for len(queriedNameServers) > 0 {
		queriedNameServer := <-queriedNameServers
		if queriedNameServer == authoritativeDNSRootServers[0] {
			sawRootServer = true
		}
		if queriedNameServer == "192.0.2.53" {
			sawAuthoritativeServer = true
		}
	}
	if !sawRootServer || !sawAuthoritativeServer {
		t.Fatalf("authoritative lookup did not query root and delegated server")
	}
}

func TestDomainAliasLimitIsTenDomains(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for aliasIndex := 0; aliasIndex < 11; aliasIndex++ {
		addForm := url.Values{}
		addForm.Set("action", "add_alias")
		addForm.Set("alias_domain", "alias"+strconv.Itoa(aliasIndex)+".example.com")
		addRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(addForm.Encode()))
		addRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		application.handleDomainSettingsPost(addRequest.Context(), addRequest, "localhost", "")
	}

	if aliasCount := application.domainAliasCount(context.Background(), "localhost"); aliasCount != 10 {
		t.Fatalf("alias count = %d, want 10", aliasCount)
	}
}

func TestAutomaticSSLDefaultsOnForResolvingDomainAndHonorsManualOff(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.automaticSSLAvailable = true
	previousIPLookup := lookupIPRecords
	previousInterfaceLookup := lookupServerInterfaceIPs
	defer func() {
		lookupIPRecords = previousIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	}()
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, os.ErrNotExist
	}

	_, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "example.com", "/", "Home", "<h1>Home</h1>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}

	setting := application.refreshDomainAutomaticSSL(context.Background(), "example.com", []net.IP{net.ParseIP("203.0.113.10")})
	if !setting.Enabled {
		t.Fatalf("auto ssl enabled = false, want true")
	}
	if !application.domainAutomaticSSLEnabled(context.Background(), "example.com") {
		t.Fatal("domainAutomaticSSLEnabled = false, want true")
	}

	application.setDomainAutomaticSSLManual(context.Background(), "example.com", false)
	setting = application.refreshDomainAutomaticSSL(context.Background(), "example.com", []net.IP{net.ParseIP("203.0.113.10")})
	if setting.Enabled || !setting.ManuallyDisabled {
		t.Fatalf("manual off setting = %+v, want disabled and manually disabled", setting)
	}

	application.setDomainAutomaticSSLManual(context.Background(), "example.com", true)
	setting = application.refreshDomainAutomaticSSL(context.Background(), "example.com", []net.IP{net.ParseIP("198.51.100.25")})
	if setting.Enabled {
		t.Fatalf("auto ssl remained enabled for non-resolving domain: %+v", setting)
	}

	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	setting = application.refreshDomainAutomaticSSL(context.Background(), "example.com", serverIPCandidatesWithExternalIP("198.51.100.25"))
	if !setting.Enabled {
		t.Fatalf("auto ssl did not use matching interface IP when external service returned another IP: %+v", setting)
	}
}

func TestAutoCertHostPolicyRequiresAutomaticSSLSettingAndPorts(t *testing.T) {
	application, _ := newTestApplication(t)
	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	defer func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	}()
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, os.ErrNotExist
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}
	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		return nil, nil
	}

	application.automaticSSLAvailable = false
	application.setDomainAutomaticSSLManual(context.Background(), "example.com", true)
	if err := application.autoCertHostPolicy(context.Background(), "example.com"); err == nil {
		t.Fatal("autoCertHostPolicy succeeded without 80/443 availability")
	}

	application.automaticSSLAvailable = true
	if err := application.autoCertHostPolicy(context.Background(), "example.com"); err != nil {
		t.Fatalf("autoCertHostPolicy with enabled domain: %v", err)
	}

	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "example.com" {
			return []net.IP{net.ParseIP("198.51.100.25")}, nil
		}
		return nil, os.ErrNotExist
	}
	if err := application.autoCertHostPolicy(context.Background(), "example.com"); err == nil {
		t.Fatal("autoCertHostPolicy accepted stale SSL setting after live DNS mismatch")
	}

	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, os.ErrNotExist
	}
	application.setDomainAutomaticSSLManual(context.Background(), "example.com", false)
	if err := application.autoCertHostPolicy(context.Background(), "example.com"); err == nil {
		t.Fatal("autoCertHostPolicy succeeded after manual SSL disable")
	}
}

func TestAutoCertHostPolicyAllowsExistingCachedCertificateForUnlistedDomain(t *testing.T) {
	application, _ := newTestApplication(t)
	application.automaticSSLAvailable = true
	writeCachedAutoCertForTest(t, application, "cached.example.com", time.Now().Add(-time.Hour), time.Now().Add(automaticSSLCertificateRenewBefore+time.Hour))

	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	defer func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	}()
	lookupIPRecords = func(string) ([]net.IP, error) {
		t.Fatal("cached certificate host policy should not query DNS")
		return nil, os.ErrInvalid
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		t.Fatal("cached certificate host policy should not detect external IP")
		return "", os.ErrInvalid
	}
	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		t.Fatal("cached certificate host policy should not inspect interface IPs")
		return nil, os.ErrInvalid
	}

	if err := application.autoCertHostPolicy(context.Background(), "cached.example.com"); err != nil {
		t.Fatalf("autoCertHostPolicy rejected cached certificate domain: %v", err)
	}

	application.setDomainAutomaticSSLManual(context.Background(), "cached.example.com", false)
	if err := application.autoCertHostPolicy(context.Background(), "cached.example.com"); err == nil {
		t.Fatal("autoCertHostPolicy accepted manually disabled cached certificate domain")
	}
}

func TestAutoCertCertificateMemoryCachePreloadsDiskCertificate(t *testing.T) {
	application, _ := newTestApplication(t)
	certificateDomain := "memory-cache.example.com"
	writeCachedAutoCertForTest(t, application, certificateDomain, time.Now().Add(-time.Hour), time.Now().Add(automaticSSLCertificateRenewBefore+time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	application.autoCertCertificateCache = startAutoCertCertificateMemoryCache(ctx, filepath.Join(application.storageRootDir(), "letsencrypt"))
	if _, _, ok := application.autoCertCachedCertificate(certificateDomain, time.Now(), automaticSSLCertificateRenewBefore); !ok {
		t.Fatal("memory certificate cache did not load disk certificate")
	}
	if removeErr := os.Remove(filepath.Join(application.storageRootDir(), "letsencrypt", certificateDomain)); removeErr != nil {
		t.Fatalf("remove disk certificate after preload: %v", removeErr)
	}

	certificate, expiresAt, ok := application.autoCertCachedCertificate(certificateDomain, time.Now(), automaticSSLCertificateRenewBefore)
	if !ok {
		t.Fatal("memory certificate cache did not serve preloaded certificate after disk removal")
	}
	if certificate == nil || expiresAt.IsZero() {
		t.Fatalf("certificate=%#v expiresAt=%s", certificate, expiresAt)
	}
}

func TestAutoCertDiskCacheRejectsPathTraversalDomains(t *testing.T) {
	application, _ := newTestApplication(t)
	for _, untrustedDomain := range []string{
		"../../outside.example.com",
		"/etc/passwd.example.com",
		`..\..\outside.example.com`,
		"valid.example.com/../../../outside.example.com",
	} {
		if _, _, found := application.autoCertCachedCertificate(untrustedDomain, time.Now(), 0); found {
			t.Fatalf("disk certificate cache accepted untrusted domain %q", untrustedDomain)
		}
	}
}

func TestAutoCertDiskCacheRejectsSymlinkedCertificate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests require privileges on Windows")
	}
	application, _ := newTestApplication(t)
	certificateDomain := "symlink-cache.example.com"
	certificateCacheDirectory := filepath.Join(application.storageRootDir(), "letsencrypt")
	if err := os.MkdirAll(certificateCacheDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideCertificatePath := filepath.Join(t.TempDir(), "outside-certificate.pem")
	certificatePEM := cachedAutoCertPEMForTest(t, certificateDomain, time.Now().Add(-time.Hour), time.Now().Add(30*24*time.Hour))
	if err := os.WriteFile(outsideCertificatePath, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideCertificatePath, filepath.Join(certificateCacheDirectory, certificateDomain)); err != nil {
		t.Fatal(err)
	}

	if _, _, found := application.autoCertCachedCertificate(certificateDomain, time.Now(), 0); found {
		t.Fatal("disk certificate cache followed a symlinked certificate outside its root")
	}
}

func TestAutomaticSSLCertificateCacheNameMatchesOnlyDomainFiles(t *testing.T) {
	certificateDomain := "cache.example.com"
	for _, acceptedCacheName := range []string{certificateDomain, certificateDomain + "+rsa"} {
		if !automaticSSLCertificateCacheNameMatchesDomain(acceptedCacheName, certificateDomain) {
			t.Fatalf("certificate cache name %q was rejected", acceptedCacheName)
		}
	}
	for _, rejectedCacheName := range []string{"../" + certificateDomain, certificateDomain + "/key", certificateDomain + "-other"} {
		if automaticSSLCertificateCacheNameMatchesDomain(rejectedCacheName, certificateDomain) {
			t.Fatalf("certificate cache name %q was accepted", rejectedCacheName)
		}
	}
}

func TestAutomaticSSLStoredCertificateAppliesWithoutRestart(t *testing.T) {
	application, _ := newTestApplication(t)
	certificateDomain := "renewed.example.com"
	certificateCacheDirectory := filepath.Join(application.storageRootDir(), "letsencrypt")
	if err := os.MkdirAll(certificateCacheDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldCertificatePEM := cachedAutoCertPEMForTest(t, certificateDomain, now.Add(-24*time.Hour), now.Add(5*24*time.Hour))
	if err := os.WriteFile(filepath.Join(certificateCacheDirectory, certificateDomain), oldCertificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cacheContext, cancelCache := context.WithCancel(context.Background())
	defer cancelCache()
	application.autoCertCertificateCache = startAutoCertCertificateMemoryCache(cacheContext, certificateCacheDirectory)
	oldCertificate, oldExpiresAt, found := application.autoCertCachedCertificateFromMemory(certificateDomain, now, 0)
	if !found || oldCertificate == nil {
		t.Fatal("old certificate was not preloaded")
	}

	newCertificatePEM := cachedAutoCertPEMForTest(t, certificateDomain, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	_, newParsedCertificate, newParsedExpiresAt, certificateEntry := automaticSSLCertificateCacheEntry(certificateDomain, newCertificatePEM, now)
	if !certificateEntry || newParsedCertificate == nil {
		t.Fatal("renewed certificate did not parse")
	}
	if err := os.WriteFile(filepath.Join(certificateCacheDirectory, certificateDomain), newCertificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := application.storeAutoCertCachedCertificate(context.Background(), certificateDomain, newParsedCertificate, newParsedExpiresAt); err != nil {
		t.Fatalf("publish renewed certificate: %v", err)
	}
	newCertificate, newExpiresAt, found := application.autoCertCachedCertificateFromMemory(certificateDomain, now, 0)
	if !found || newCertificate == nil {
		t.Fatal("renewed certificate was not published")
	}
	if !newExpiresAt.After(oldExpiresAt) {
		t.Fatalf("renewed expiry = %s, old expiry = %s", newExpiresAt, oldExpiresAt)
	}
	if bytes.Equal(newCertificate.Certificate[0], oldCertificate.Certificate[0]) {
		t.Fatal("live certificate did not change after durable cache put")
	}

	_, fallbackCertificate, err := generateAutomaticSSLFallbackCertificate(now)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := application.autoCertTLSConfig(fallbackCertificate)
	servedCertificate, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: certificateDomain})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(servedCertificate.Certificate[0], newCertificate.Certificate[0]) {
		t.Fatal("TLS did not serve the renewed certificate")
	}
}

func TestAutoCertRenewalCheckKeepsStillValidCertificateAvailable(t *testing.T) {
	application, _ := newTestApplication(t)
	certificateDomain := "expiring-cache.example.com"
	now := time.Now()
	writeCachedAutoCertForTest(t, application, certificateDomain, now.Add(-time.Hour), now.Add(24*time.Hour))
	cacheContext, cancelCache := context.WithCancel(context.Background())
	defer cancelCache()
	application.autoCertCertificateCache = startAutoCertCertificateMemoryCache(cacheContext, filepath.Join(application.storageRootDir(), "letsencrypt"))
	if _, _, reusable := application.autoCertCachedCertificateFromMemory(certificateDomain, now, automaticSSLCertificateRenewBefore); reusable {
		t.Fatal("expiring certificate was considered reusable without renewal")
	}
	if certificate, _, valid := application.autoCertCachedCertificateFromMemory(certificateDomain, now, 0); !valid || certificate == nil {
		t.Fatal("renewal check removed the still-valid certificate")
	}
}

func TestNextAutomaticSSLRenewalAuditTimeUsesNextLocalDay(t *testing.T) {
	location := time.FixedZone("test", 3*60*60)
	now := time.Date(2026, 8, 8, 17, 30, 0, 0, location)
	nextDayStart := time.Date(2026, 8, 9, 0, 0, 0, 0, location)
	followingDayStart := time.Date(2026, 8, 10, 0, 0, 0, 0, location)

	auditAt, err := nextAutomaticSSLRenewalAuditTime(now, bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if auditAt.Before(nextDayStart) || !auditAt.Before(followingDayStart) {
		t.Fatalf("audit time = %s, want next local calendar day", auditAt)
	}
}

func TestAutomaticSSLRenewalAuditRunsAtStartupAndStops(t *testing.T) {
	application, _ := newTestApplication(t)
	certificateDomain := "startup-renewal.example.com"
	now := time.Now()
	writeCachedAutoCertForTest(t, application, certificateDomain, now.Add(-time.Hour), now.Add(24*time.Hour))
	cacheContext, cancelCache := context.WithCancel(context.Background())
	defer cancelCache()
	application.autoCertCertificateCache = startAutoCertCertificateMemoryCache(cacheContext, filepath.Join(application.storageRootDir(), "letsencrypt"))
	stop := make(chan struct{})
	requests := make(chan automaticSSLRequest, 1)
	done := make(chan struct{})
	go func() {
		application.runAutomaticSSLRenewalAudits(stop, requests, time.Now, bytes.NewReader(make([]byte, 16)))
		close(done)
	}()
	select {
	case request := <-requests:
		if request.action != "renewal_audit" || request.domain != certificateDomain {
			t.Fatalf("startup renewal request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("startup renewal audit did not run")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("renewal audit did not stop")
	}
}

func TestAutomaticSSLFallbackCertificatePersistsWithPrivatePermissions(t *testing.T) {
	application, _ := newTestApplication(t)
	firstCertificate, err := application.loadOrCreateAutomaticSSLFallbackCertificate(time.Now())
	if err != nil {
		t.Fatalf("create fallback certificate: %v", err)
	}
	secondCertificate, err := application.loadOrCreateAutomaticSSLFallbackCertificate(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("load fallback certificate: %v", err)
	}
	if !bytes.Equal(firstCertificate.Certificate[0], secondCertificate.Certificate[0]) {
		t.Fatal("fallback certificate changed instead of loading the persisted certificate")
	}
	certificatePath := filepath.Join(application.storageRootDir(), "keys", "tls", "sitebrush-fallback.pem")
	fileInfo, err := os.Stat(certificatePath)
	if err != nil {
		t.Fatalf("stat fallback certificate: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("fallback certificate permissions = %o, want 600", fileInfo.Mode().Perm())
	}
}

func TestAutoCertTLSConfigReturnsFallbackAndOnlyObservesDomain(t *testing.T) {
	fallbackPEM, fallbackCertificate, err := generateAutomaticSSLFallbackCertificate(time.Now())
	if err != nil || len(fallbackPEM) == 0 {
		t.Fatalf("generate fallback certificate: bytes=%d err=%v", len(fallbackPEM), err)
	}
	application := &App{automaticSSL: make(chan automaticSSLRequest, 1)}
	tlsConfig := application.autoCertTLSConfig(fallbackCertificate)
	certificate, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "lcs-robs.adobe.io"})
	if err != nil {
		t.Fatalf("get fallback certificate: %v", err)
	}
	if certificate != fallbackCertificate {
		t.Fatal("ordinary TLS handshake did not return the shared fallback certificate")
	}
	select {
	case request := <-application.automaticSSL:
		if request.action != "observe" || request.domain != "lcs-robs.adobe.io" {
			t.Fatalf("automatic SSL observation = %+v", request)
		}
	default:
		t.Fatal("ordinary TLS handshake did not enqueue a background observation")
	}
}

func TestAutoCertTLSConfigPrefersPreparedPublicCertificate(t *testing.T) {
	_, fallbackCertificate, err := generateAutomaticSSLFallbackCertificate(time.Now())
	if err != nil {
		t.Fatalf("generate fallback certificate: %v", err)
	}
	_, preparedCertificate, err := generateAutomaticSSLFallbackCertificate(time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("generate prepared certificate: %v", err)
	}
	preparedExpiresAt, err := tlsCertificateExpiresAt(preparedCertificate)
	if err != nil {
		t.Fatalf("read prepared certificate expiry: %v", err)
	}
	certificateCacheDirectory := t.TempDir()
	cacheContext, cancelCache := context.WithCancel(context.Background())
	defer cancelCache()
	application := &App{
		autoCertCertificateCache: startAutoCertCertificateMemoryCache(cacheContext, certificateCacheDirectory),
		automaticSSL:             make(chan automaticSSLRequest, 1),
	}
	application.rememberAutoCertCachedCertificate("example.com", preparedCertificate, preparedExpiresAt)
	if _, _, found := application.autoCertCachedCertificateFromMemory("example.com", time.Now(), 0); !found {
		t.Fatal("prepared certificate was not stored in memory")
	}

	tlsConfig := application.autoCertTLSConfig(fallbackCertificate)
	certificate, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
	if err != nil {
		t.Fatalf("get prepared certificate: %v", err)
	}
	if certificate != preparedCertificate {
		t.Fatal("TLS handshake did not prefer the prepared certificate")
	}
	select {
	case request := <-application.automaticSSL:
		t.Fatalf("prepared certificate unexpectedly enqueued observation: %+v", request)
	default:
	}
}

func TestPrepareAutomaticSSLDomainIgnoresUnmanagedSNIWithoutDNS(t *testing.T) {
	application, _ := newTestApplication(t)
	application.automaticSSLAvailable = true
	previousIPLookup := lookupIPRecords
	defer func() {
		lookupIPRecords = previousIPLookup
	}()
	lookupIPRecords = func(string) ([]net.IP, error) {
		t.Fatal("unmanaged TLS SNI initiated an authoritative DNS lookup")
		return nil, os.ErrInvalid
	}

	result := application.prepareAutomaticSSLDomain(nil, "lcs-cops.adobe.io", []net.IP{net.ParseIP("8.8.8.8")})
	if result.state != "ignored" || result.err != nil {
		t.Fatalf("unmanaged automatic SSL result = %+v, want ignored without error", result)
	}
}

func TestPrepareAutomaticSSLDomainDoesNotIssueCertificateWhenDNSDiffersFromExternalIP(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.automaticSSLAvailable = true
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "customer.example.com", "/", "Home", "<h1>Home</h1>"); err != nil {
		t.Fatalf("insert managed page: %v", err)
	}
	previousIPLookup := lookupIPRecords
	defer func() {
		lookupIPRecords = previousIPLookup
	}()
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain != "customer.example.com" {
			t.Fatalf("unexpected DNS lookup for %q", domain)
		}
		return []net.IP{net.ParseIP("1.1.1.1")}, nil
	}
	certificateManager := automaticSSLIssuerFunc(func(context.Context, string) channelacme.IssueResult {
		t.Fatal("DNS mismatch reached certificate issuance")
		return channelacme.IssueResult{Err: errors.New("unexpected certificate issuance")}
	})

	result := application.prepareAutomaticSSLDomain(certificateManager, "customer.example.com", []net.IP{net.ParseIP("8.8.8.8")})
	if result.state != "waiting_dns" || result.err == nil {
		t.Fatalf("automatic SSL mismatch result = %+v, want waiting_dns with reason", result)
	}
}

func TestForcedAutomaticSSLPreparationDoesNotReuseValidCachedCertificate(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.automaticSSLAvailable = true
	certificateDomain := "renew.sitebrush.org"
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, certificateDomain, "/", "Home", "<h1>Home</h1>"); err != nil {
		t.Fatalf("insert managed page: %v", err)
	}
	application.setDomainAutomaticSSLManual(contextWithDomain(context.Background(), certificateDomain), certificateDomain, true)

	writeCachedAutoCertForTest(t, application, certificateDomain, time.Now().Add(-time.Hour), time.Now().Add(88*24*time.Hour))
	cacheContext, cancelCache := context.WithCancel(context.Background())
	defer cancelCache()
	application.autoCertCertificateCache = startAutoCertCertificateMemoryCache(cacheContext, filepath.Join(application.storageRootDir(), "letsencrypt"))

	previousIPLookup := lookupIPRecords
	defer func() { lookupIPRecords = previousIPLookup }()
	serverIP := net.ParseIP("8.8.8.8")
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain != certificateDomain {
			t.Fatalf("unexpected DNS lookup for %q", domain)
		}
		return []net.IP{serverIP}, nil
	}

	_, renewedCertificate, err := generateAutomaticSSLFallbackCertificate(time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("generate renewed certificate: %v", err)
	}
	renewedExpiresAt := time.Now().Add(90 * 24 * time.Hour)
	issueCount := 0
	certificateManager := automaticSSLIssuerFunc(func(context.Context, string) channelacme.IssueResult {
		issueCount++
		return channelacme.IssueResult{Certificate: renewedCertificate, ExpiresAt: renewedExpiresAt}
	})

	result := application.prepareAutomaticSSLDomainWithPolicy(certificateManager, certificateDomain, []net.IP{serverIP}, true)
	if issueCount != 1 || result.state != "ready" || !result.forced || result.certificate != renewedCertificate {
		t.Fatalf("forced renewal result = %+v issue_count=%d", result, issueCount)
	}
}

func TestAutomaticSSLDNSRequiresEveryPublicRecordToMatch(t *testing.T) {
	serverIPs := []net.IP{net.ParseIP("8.8.8.8")}
	if !domainIPRecordsMatchAll([]net.IP{net.ParseIP("8.8.8.8")}, serverIPs) {
		t.Fatal("matching DNS record was rejected")
	}
	if domainIPRecordsMatchAll([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}, serverIPs) {
		t.Fatal("partially matching DNS records were accepted")
	}
}

func TestAutomaticSSLExternalIPRequiresIndependentConfirmation(t *testing.T) {
	confirmedIPv4 := net.ParseIP("8.8.8.8")
	confirmedIPv6 := net.ParseIP("2001:4860:4860::8888")
	conflictingIPv4 := net.ParseIP("1.1.1.1")
	probeResults := []automaticSSLExternalIPResult{
		{probe: automaticSSLExternalIPProbe{name: "first-v4"}, ip: confirmedIPv4},
		{probe: automaticSSLExternalIPProbe{name: "second-v4"}, ip: confirmedIPv4},
		{probe: automaticSSLExternalIPProbe{name: "conflicting-v4"}, ip: conflictingIPv4},
		{probe: automaticSSLExternalIPProbe{name: "first-v6"}, ip: confirmedIPv6},
	}

	confirmedIPs, err := confirmedAutomaticSSLServerIPs(probeResults, []net.IP{confirmedIPv6})
	if err != nil {
		t.Fatalf("confirm external server IPs: %v", err)
	}
	if len(confirmedIPs) != 2 || !automaticSSLIPListContains(confirmedIPs, confirmedIPv4) || !automaticSSLIPListContains(confirmedIPs, confirmedIPv6) {
		t.Fatalf("confirmed IPs = %v, want independently confirmed IPv4 and IPv6", confirmedIPs)
	}
	if automaticSSLIPListContains(confirmedIPs, conflictingIPv4) {
		t.Fatalf("single unconfirmed provider address was accepted: %v", confirmedIPs)
	}

	_, err = confirmedAutomaticSSLServerIPs([]automaticSSLExternalIPResult{{probe: automaticSSLExternalIPProbe{name: "single"}, ip: confirmedIPv4}}, nil)
	if err == nil {
		t.Fatal("single provider address was accepted without an interface match")
	}
}

func automaticSSLIPListContains(ipList []net.IP, expectedIP net.IP) bool {
	for _, candidateIP := range ipList {
		if candidateIP.Equal(expectedIP) {
			return true
		}
	}
	return false
}

func TestAutomaticSSLNextCheckDelayEscalatesNearExpiry(t *testing.T) {
	now := time.Now()
	for _, testCase := range []struct {
		remaining time.Duration
		want      time.Duration
	}{
		{remaining: 20 * 24 * time.Hour, want: 24 * time.Hour},
		{remaining: 10 * 24 * time.Hour, want: 6 * time.Hour},
		{remaining: 5 * 24 * time.Hour, want: time.Hour},
		{remaining: 24 * time.Hour, want: 15 * time.Minute},
	} {
		if got := automaticSSLNextCheckDelay(now.Add(testCase.remaining), now); got != testCase.want {
			t.Fatalf("remaining %s: delay=%s want=%s", testCase.remaining, got, testCase.want)
		}
	}
}

func TestTLSHandshakeNoiseAggregateLogsFirstEventThenProgressiveSummaries(t *testing.T) {
	startedAt := time.Date(2026, 7, 24, 0, 30, 0, 0, time.UTC)
	noise, ok := diagnosticlog.ParseTLSHandshakeNoise(`http: TLS handshake error from 127.0.0.1:53497: EOF`)
	if !ok {
		t.Fatal("test TLS noise was not classified")
	}
	aggregate := newTLSHandshakeNoiseAggregate()
	firstMessages := aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt})
	if len(firstMessages) != 1 || firstMessages[0] != noise.Message {
		t.Fatalf("first messages = %#v, want original message", firstMessages)
	}
	for eventIndex := 0; eventIndex < 3; eventIndex++ {
		if messages := aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt.Add(time.Duration(eventIndex+1) * time.Minute)}); len(messages) != 0 {
			t.Fatalf("repeat %d produced immediate messages: %#v", eventIndex, messages)
		}
	}
	if messages := aggregate.flushDue(startedAt.Add(4*time.Minute), false); len(messages) != 0 {
		t.Fatalf("summary was emitted before five minutes: %#v", messages)
	}
	fiveMinuteMessages := aggregate.flushDue(startedAt.Add(5*time.Minute), false)
	if len(fiveMinuteMessages) != 1 ||
		!strings.Contains(fiveMinuteMessages[0], "class=connection_closed") ||
		!strings.Contains(fiveMinuteMessages[0], "source=loopback") ||
		!strings.Contains(fiveMinuteMessages[0], "interval=5m") ||
		!strings.Contains(fiveMinuteMessages[0], "new=3 total=4") {
		t.Fatalf("five minute summary = %#v", fiveMinuteMessages)
	}

	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt.Add(6 * time.Minute)})
	if messages := aggregate.flushDue(startedAt.Add(34*time.Minute), false); len(messages) != 0 {
		t.Fatalf("thirty minute summary was emitted early: %#v", messages)
	}
	thirtyMinuteMessages := aggregate.flushDue(startedAt.Add(35*time.Minute), false)
	if len(thirtyMinuteMessages) != 1 ||
		!strings.Contains(thirtyMinuteMessages[0], "interval=30m") ||
		!strings.Contains(thirtyMinuteMessages[0], "new=1 total=5") {
		t.Fatalf("thirty minute summary = %#v", thirtyMinuteMessages)
	}
}

func TestTLSHandshakeNoiseAggregateFinalizesRepeatsAndDropsSingleIdleEvent(t *testing.T) {
	startedAt := time.Date(2026, 7, 24, 0, 30, 0, 0, time.UTC)
	noise, ok := diagnosticlog.ParseTLSHandshakeNoise(`http: TLS handshake error from 127.0.0.1:53497: EOF`)
	if !ok {
		t.Fatal("test TLS noise was not classified")
	}
	aggregate := newTLSHandshakeNoiseAggregate()
	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt})
	if messages := aggregate.flushDue(startedAt.Add(tlsHandshakeNoiseIdleTTL), false); len(messages) != 0 {
		t.Fatalf("single idle event produced an extra summary: %#v", messages)
	}
	if len(aggregate.groupsByKey) != 0 {
		t.Fatalf("single idle group was not removed: %#v", aggregate.groupsByKey)
	}

	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt})
	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt.Add(time.Minute)})
	finalMessages := aggregate.flushDue(startedAt.Add(tlsHandshakeNoiseIdleTTL+time.Minute), false)
	if len(finalMessages) != 1 ||
		!strings.Contains(finalMessages[0], "interval=final") ||
		!strings.Contains(finalMessages[0], "new=1 total=2") {
		t.Fatalf("final summary = %#v", finalMessages)
	}
}

func TestTLSHandshakeNoiseAggregateMarksSaturatedCountsAsLowerBounds(t *testing.T) {
	startedAt := time.Date(2026, 7, 24, 0, 30, 0, 0, time.UTC)
	noise, ok := diagnosticlog.ParseTLSHandshakeNoise(`http: TLS handshake error from 127.0.0.1:53497: EOF`)
	if !ok {
		t.Fatal("test TLS noise was not classified")
	}
	aggregate := newTLSHandshakeNoiseAggregate()
	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt})
	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt.Add(time.Minute)})
	aggregate.saturated = true
	messages := aggregate.flushDue(startedAt.Add(5*time.Minute), false)
	if len(messages) != 1 || !strings.Contains(messages[0], "lower_bound=true") {
		t.Fatalf("saturated summary = %#v", messages)
	}
	if aggregate.saturated {
		t.Fatal("saturation marker was not cleared after summary")
	}
}

func TestTLSHandshakeNoiseAggregateFlushesPendingRepeatsOnShutdown(t *testing.T) {
	startedAt := time.Date(2026, 7, 24, 0, 30, 0, 0, time.UTC)
	noise, ok := diagnosticlog.ParseTLSHandshakeNoise(`http: TLS handshake error from 127.0.0.1:53497: EOF`)
	if !ok {
		t.Fatal("test TLS noise was not classified")
	}
	aggregate := newTLSHandshakeNoiseAggregate()
	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt})
	aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt.Add(time.Second)})
	messages := aggregate.flushDue(startedAt.Add(time.Minute), true)
	if len(messages) != 1 ||
		!strings.Contains(messages[0], "interval=shutdown") ||
		!strings.Contains(messages[0], "new=1 total=2") {
		t.Fatalf("shutdown summary = %#v", messages)
	}
	if len(aggregate.groupsByKey) != 0 {
		t.Fatalf("shutdown retained groups: %d", len(aggregate.groupsByKey))
	}
}

func TestTLSHandshakeNoiseAggregateBoundsSourceGroups(t *testing.T) {
	startedAt := time.Date(2026, 7, 24, 0, 30, 0, 0, time.UTC)
	aggregate := newTLSHandshakeNoiseAggregate()
	normalGroupLimit := tlsHandshakeNoiseMaximumGroups - tlsHandshakeNoiseReservedOverflowGroups
	for sourceIndex := 0; sourceIndex < normalGroupLimit+100; sourceIndex++ {
		source := fmt.Sprintf("source-%d", sourceIndex)
		noise := diagnosticlog.TLSHandshakeNoise{
			Class:   "connection_closed",
			Source:  source,
			Message: "http: TLS handshake error from 127.0.0.1:1: EOF",
		}
		aggregate.observe(tlsHandshakeNoiseEvent{Noise: noise, OccurredAt: startedAt})
	}
	if len(aggregate.groupsByKey) > tlsHandshakeNoiseMaximumGroups {
		t.Fatalf("group count = %d, maximum = %d", len(aggregate.groupsByKey), tlsHandshakeNoiseMaximumGroups)
	}
	overflowKey := strings.Join([]string{"connection_closed", "other"}, "\x00")
	overflowGroup, found := aggregate.groupsByKey[overflowKey]
	if !found || overflowGroup.displaySource != "other" || overflowGroup.total != 100 {
		t.Fatalf("overflow group = %+v found=%t", overflowGroup, found)
	}
}

func TestAutoCertHostPolicyRechecksExpiringCachedCertificate(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.automaticSSLAvailable = true
	writeCachedAutoCertForTest(t, application, "expiring.example.com", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "expiring.example.com", "/", "Home", "<h1>Home</h1>"); err != nil {
		t.Fatalf("insert page: %v", err)
	}

	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	previousInterfaceLookup := lookupServerInterfaceIPs
	defer func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
		lookupServerInterfaceIPs = previousInterfaceLookup
	}()
	lookupIPRecords = func(domain string) ([]net.IP, error) {
		if domain == "expiring.example.com" {
			return []net.IP{net.ParseIP("198.51.100.25")}, nil
		}
		return nil, os.ErrNotExist
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		return "203.0.113.10", nil
	}
	lookupServerInterfaceIPs = func() ([]net.IP, error) {
		return nil, nil
	}

	if err := application.autoCertHostPolicy(context.Background(), "expiring.example.com"); err == nil {
		t.Fatal("autoCertHostPolicy accepted expiring cached certificate without fresh DNS match")
	}
	setting := application.domainAutomaticSSLSetting(context.Background(), "expiring.example.com")
	if strings.TrimSpace(setting.LastFailureReason) == "" || strings.TrimSpace(setting.RetryAfter) == "" {
		t.Fatalf("SSL failure state was not stored: %+v", setting)
	}
}

func TestAutoCertHostPolicyRejectsPrivateDomainsBeforeDNS(t *testing.T) {
	application, _ := newTestApplication(t)
	application.automaticSSLAvailable = true
	application.setDomainAutomaticSSLManual(context.Background(), "printer.local", true)

	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	defer func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
	}()
	lookupIPRecords = func(string) ([]net.IP, error) {
		t.Fatal("private automatic SSL domain should not query DNS")
		return nil, os.ErrInvalid
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		t.Fatal("private automatic SSL domain should not detect external IP")
		return "", os.ErrInvalid
	}

	if err := application.autoCertHostPolicy(context.Background(), "printer.local"); err == nil {
		t.Fatal("autoCertHostPolicy accepted .local domain")
	}
}

func TestAutoCertHostPolicyBackoffSkipsPrechecks(t *testing.T) {
	application, rawDB := newTestApplication(t)
	application.automaticSSLAvailable = true
	if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "backoff.example.com", "/", "Home", "<h1>Home</h1>"); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	application.recordDomainAutomaticSSLFailure(context.Background(), "backoff.example.com", "previous DNS failure", time.Hour)

	previousIPLookup := lookupIPRecords
	previousExternalIPLookup := lookupServerExternalIP
	defer func() {
		lookupIPRecords = previousIPLookup
		lookupServerExternalIP = previousExternalIPLookup
	}()
	lookupIPRecords = func(string) ([]net.IP, error) {
		t.Fatal("automatic SSL retry backoff should skip DNS")
		return nil, os.ErrInvalid
	}
	lookupServerExternalIP = func(context.Context) (string, error) {
		t.Fatal("automatic SSL retry backoff should skip external IP detection")
		return "", os.ErrInvalid
	}

	if err := application.autoCertHostPolicy(context.Background(), "backoff.example.com"); err == nil {
		t.Fatal("autoCertHostPolicy accepted domain during retry backoff")
	}
}

func TestAutoCertIssuanceGuardRejectsDuplicateDomain(t *testing.T) {
	application, _ := newTestApplication(t)
	if err := application.beginAutoCertIssuance(context.Background(), "duplicate.example.com"); err != nil {
		t.Fatalf("begin first issuance: %v", err)
	}
	if err := application.beginAutoCertIssuance(context.Background(), "duplicate.example.com"); err == nil {
		t.Fatal("duplicate issuance was accepted while first request is in progress")
	}
	application.finishAutoCertIssuance("duplicate.example.com")
	if err := application.beginAutoCertIssuance(context.Background(), "duplicate.example.com"); err != nil {
		t.Fatalf("begin issuance after finish: %v", err)
	}
	application.finishAutoCertIssuance("duplicate.example.com")
}

func TestAutomaticSSLStatusViewExplainsReadyWaitingAndErrors(t *testing.T) {
	readyStatus := automaticSSLStatusView(DomainAutomaticSSLSetting{Domain: "example.com", Available: true, Enabled: true}, nil)
	if readyStatus.OverallClass != "status-ok" || readyStatus.DomainCheckClass != "status-ok" || readyStatus.CertificateClass != "status-ok" {
		t.Fatalf("ready status = %+v, want all ok", readyStatus)
	}

	waitingStatus := automaticSSLStatusView(DomainAutomaticSSLSetting{Domain: "example.com", Available: true}, nil)
	if waitingStatus.OverallClass != "status-warn" || waitingStatus.DomainCheckClass != "status-warn" || waitingStatus.CertificateClass != "status-warn" {
		t.Fatalf("waiting status = %+v, want all warn", waitingStatus)
	}

	manualOffStatus := automaticSSLStatusView(DomainAutomaticSSLSetting{Domain: "example.com", Available: true, ManuallyDisabled: true}, nil)
	if manualOffStatus.OverallTextKey != "domain_settings_ssl_status_disabled" || manualOffStatus.CertificateTextKey != "domain_settings_ssl_certificate_disabled" {
		t.Fatalf("manual off status = %+v, want disabled copy", manualOffStatus)
	}

	portErrorStatus := automaticSSLStatusView(DomainAutomaticSSLSetting{Domain: "example.com", Available: false}, nil)
	if portErrorStatus.OverallClass != "status-bad" || portErrorStatus.CertificateTextKey != "domain_settings_ssl_certificate_ports_error" {
		t.Fatalf("port error status = %+v, want red port error", portErrorStatus)
	}

	ipErrorStatus := automaticSSLStatusView(DomainAutomaticSSLSetting{Domain: "example.com", Available: true}, os.ErrNotExist)
	if ipErrorStatus.DomainCheckClass != "status-bad" || ipErrorStatus.DomainCheckTextKey != "domain_settings_ssl_domain_check_error" {
		t.Fatalf("ip error status = %+v, want red domain check error", ipErrorStatus)
	}
}

func TestDomainLogsRotateDailyAndKeepAnalyticsData(t *testing.T) {
	application, rawDB := newTestApplication(t)
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	logDir := application.domainLogDir("example.com")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLogPath := filepath.Join(logDir, "2026-05-07.log")
	recentLogPath := filepath.Join(logDir, "2026-05-08.log")
	if err := os.WriteFile(oldLogPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentLogPath, []byte("recent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := rawDB.Exec(`INSERT INTO analytics_reports(domain,generated_at,period_start,period_end,event_count,report_json) VALUES(?,?,?,?,?,?)`, "example.com", now.Format(time.RFC3339), "", "", 1, "{}")
	if err != nil {
		t.Fatal(err)
	}

	application.cleanupOldDomainLogs("example.com", now)
	if _, err := os.Stat(oldLogPath); !os.IsNotExist(err) {
		t.Fatalf("old log still exists, stat error = %v", err)
	}
	if _, err := os.Stat(recentLogPath); err != nil {
		t.Fatalf("recent log was removed: %v", err)
	}

	application.appendDomainLogEvent(domainLogEvent{Domain: "example.com", OccurredAt: now, Message: "AUTOCERT certificate requested"})
	todayLogPath := filepath.Join(logDir, "2026-05-13.log")
	todayLog, err := os.ReadFile(todayLogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(todayLog), "AUTOCERT certificate requested") {
		t.Fatalf("today log does not contain certificate message: %q", string(todayLog))
	}
	var reportCount int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM analytics_reports WHERE domain=?`, "example.com").Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 1 {
		t.Fatalf("analytics report count = %d, want 1", reportCount)
	}

	problemLogDir := application.problemLogDir()
	if err := os.MkdirAll(problemLogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldProblemLogPath := filepath.Join(problemLogDir, "2026-05-07.log")
	if err := os.WriteFile(oldProblemLogPath, []byte("old problem\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	application.appendProblemLogEvent(now, "AUTOCERT DNS refresh started")
	if _, err := os.Stat(oldProblemLogPath); !os.IsNotExist(err) {
		t.Fatalf("old problem log still exists, stat error = %v", err)
	}
	problemLogPath := filepath.Join(problemLogDir, "2026-05-13.log")
	problemLog, err := os.ReadFile(problemLogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(problemLog), "AUTOCERT DNS refresh started") {
		t.Fatalf("problem log does not contain diagnostic message: %q", string(problemLog))
	}
}

func TestServeTLSWithAutoCertUsesTLSOnListener(t *testing.T) {
	application, _ := newTestApplication(t)
	certificateSource := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(certificateSource.Close)
	tlsConfig := certificateSource.TLS.Clone()
	if tlsConfig == nil {
		t.Fatal("test TLS config is nil")
	}
	tlsConfig.MinVersion = tls.VersionTLS12
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		application.serveTLSWithAutoCert(context.Background(), listener, tlsConfig, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("secure"))
		}))
		close(serverDone)
	}()

	client := certificateSource.Client()
	response, err := client.Get("https://" + listener.Addr().String() + "/")
	if err != nil {
		_ = listener.Close()
		t.Fatalf("HTTPS request failed: %v", err)
	}
	defer response.Body.Close()
	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if string(bodyBytes) != "secure" {
		_ = listener.Close()
		t.Fatalf("HTTPS body = %q, want secure", string(bodyBytes))
	}

	_ = listener.Close()
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("TLS server did not stop after listener close")
	}
}

func TestListenOnAvailablePortFallsBackWhenRequestedPortIsBusy(t *testing.T) {
	busyListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busyListener.Close()

	_, portText, err := net.SplitHostPort(busyListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	busyPort, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	fallbackListener, fallbackPort, err := listenOnAvailablePort(busyPort)
	if err != nil {
		t.Fatal(err)
	}
	defer fallbackListener.Close()

	if fallbackPort == busyPort {
		t.Fatalf("fallback port = busy port %d", busyPort)
	}
	if fallbackPort < 9898 {
		t.Fatalf("fallback port = %d, want 9898 or higher", fallbackPort)
	}
}

func TestParseListenPortsAcceptsStandardPairAndCustomPort(t *testing.T) {
	standardPorts, err := parseListenPorts("80,443")
	if err != nil {
		t.Fatalf("parse standard pair: %v", err)
	}
	if standardPorts.HTTPPort != 80 || !standardPorts.TLSEnabled || standardPorts.Raw != "80,443" {
		t.Fatalf("standard ports = %+v", standardPorts)
	}

	legacyStandardPorts, err := parseListenPorts("80")
	if err != nil {
		t.Fatalf("parse legacy standard port: %v", err)
	}
	if legacyStandardPorts.HTTPPort != 80 || !legacyStandardPorts.TLSEnabled || legacyStandardPorts.Raw != "80,443" {
		t.Fatalf("legacy standard ports = %+v", legacyStandardPorts)
	}

	customPorts, err := parseListenPorts("8080")
	if err != nil {
		t.Fatalf("parse custom port: %v", err)
	}
	if customPorts.HTTPPort != 8080 || customPorts.TLSEnabled {
		t.Fatalf("custom ports = %+v", customPorts)
	}
}

func TestParseListenPortsRejectsPartialTLSAndMultipleCustomPorts(t *testing.T) {
	for _, rawPorts := range []string{"443", "80,444", "8080,9090", "abc"} {
		if _, err := parseListenPorts(rawPorts); err == nil {
			t.Fatalf("parseListenPorts(%q) succeeded, want error", rawPorts)
		}
	}
}

func TestAutomaticSSLUnavailableHintExplainsHowToFixLaunchMode(t *testing.T) {
	if key := automaticSSLUnavailableHintKey(false); key != "domain_settings_ssl_unavailable_admin_hint" {
		t.Fatalf("default-port fallback hint = %q", key)
	}
	if key := automaticSSLUnavailableHintKey(true); key != "domain_settings_ssl_unavailable_custom_ports_hint" {
		t.Fatalf("custom-port hint = %q", key)
	}
	for languageCode, translations := range translationCatalog {
		for _, translationKey := range []string{"domain_settings_ssl_unavailable_admin_hint", "domain_settings_ssl_unavailable_custom_ports_hint"} {
			if strings.TrimSpace(translations[translationKey]) == "" {
				t.Fatalf("language %s is missing %s", languageCode, translationKey)
			}
		}
	}
}

func TestReplaceTemplateBlocksHandlesNestedTemplateMatches(t *testing.T) {
	sourceHTML := `<html><body><div class="SiteBrush-Template outer"><section>before<div class="SiteBrush-Template inner">new inner</div>after</section></div></body></html>`
	targetHTML := `<html><body><div class="SiteBrush-Template outer"><section>before<div class="SiteBrush-Template inner">old inner</div>after</section></div><p>tail</p></body></html>`

	updatedHTML, changed := sitebrushtemplate.ReplaceBlocks(targetHTML, sitebrushtemplate.ExtractBlocks(sourceHTML))
	if !changed {
		t.Fatal("changed = false, want true")
	}

	expectedHTML := `<html><body><div class="SiteBrush-Template outer"><section>before<div class="SiteBrush-Template inner">new inner</div>after</section></div><p>tail</p></body></html>`
	if updatedHTML != expectedHTML {
		t.Fatalf("updated html = %q, want %q", updatedHTML, expectedHTML)
	}
}

func TestMovedPageRedirectsFromOldPathToNewPath(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/contacts", "Contacts", "<h1>Contacts</h1>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/contacts", "Contacts", "<h1>Contacts</h1>")
	if err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/contacts", "<h1>Contacts</h1>", time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}

	saveForm := url.Values{}
	saveForm.Set("path", "/address")
	saveForm.Set("previous_path", "/contacts")
	saveForm.Set("title", "Address")
	saveForm.Set("html", "<h1>Address</h1>")
	saveRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/address?save", strings.NewReader(saveForm.Encode()))
	saveRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	saveResponse := httptest.NewRecorder()
	application.route(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusFound {
		t.Fatalf("save status = %d, body=%q", saveResponse.Code, saveResponse.Body.String())
	}

	oldPathRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/contacts", nil)
	oldPathResponse := httptest.NewRecorder()
	application.route(oldPathResponse, oldPathRequest)
	if oldPathResponse.Code != http.StatusMovedPermanently {
		t.Fatalf("old path status = %d, body=%q", oldPathResponse.Code, oldPathResponse.Body.String())
	}
	if location := oldPathResponse.Header().Get("Location"); location != "/address" {
		t.Fatalf("old path location = %q, want %q", location, "/address")
	}
}

func TestCanonicalTrailingSlashRedirectsExistingPage(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/service", "Service", "<html><body>Service page</body></html>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/service", "Service", "<html><body>Service page</body></html>")
	if err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/service", "<html><body>Service page</body></html>")

	redirectResponse := httptest.NewRecorder()
	application.route(redirectResponse, httptest.NewRequest(http.MethodGet, "http://localhost:8080/service", nil))
	if redirectResponse.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d, body=%q", redirectResponse.Code, redirectResponse.Body.String())
	}
	if location := redirectResponse.Header().Get("Location"); location != "/service/" {
		t.Fatalf("redirect location = %q, want %q", location, "/service/")
	}

	pageResponse := httptest.NewRecorder()
	application.route(pageResponse, httptest.NewRequest(http.MethodGet, "http://localhost:8080/service/", nil))
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("page status = %d, body=%q", pageResponse.Code, pageResponse.Body.String())
	}
	if !strings.Contains(pageResponse.Body.String(), "Service page") {
		t.Fatalf("page body does not include service content: %q", pageResponse.Body.String())
	}
}

func TestMissingPageReturns404ForGuest(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{
		`Страница <strong>/missing</strong> не найдена. Пожалуйста <a class="alert-link" href="/missing?visual">создайте эту страницу</a>.`,
		`class="alert missing-page-alert alert-info"`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("missing page did not include %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, `method="post" action="/missing?grab" data-grab-form`) {
		t.Fatalf("guest missing page unexpectedly offers copy form: %s", body)
	}
	if strings.Contains(body, "sitebrush / /missing") {
		t.Fatalf("guest missing page still includes the old footer label: %s", body)
	}
}

func TestMissingPageReturns404ForAdminWithCopyOption(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `method="post" action="/missing?grab" data-grab-form`) {
		t.Fatalf("admin missing page does not offer copy form: %s", body)
	}
	if !strings.Contains(body, `class="card card-body missing-page-form missing-page-copy-form"`) {
		t.Fatalf("admin missing page does not use styled copy form: %s", body)
	}
	if !strings.Contains(body, `/p/static/copy.png`) || !strings.Contains(body, `/p/static/backup.png`) {
		t.Fatalf("admin missing page does not use Sitebrush form icons: %s", body)
	}
	if !strings.Contains(body, `name="copy_whole_site"`) {
		t.Fatalf("admin missing page does not offer whole-site copy option: %s", body)
	}
}

func listStoredFiles(rootPath string) ([]string, error) {
	storedFiles := make([]string, 0)
	walkErr := filepath.WalkDir(rootPath, func(currentPath string, currentEntry os.DirEntry, walkErr error) error {
		if walkErr != nil || currentEntry.IsDir() {
			return walkErr
		}
		storedFiles = append(storedFiles, currentPath)
		return nil
	})
	return storedFiles, walkErr
}

func TestStaticArchivePathForURI(t *testing.T) {
	testCases := []struct {
		pageURI          string
		expectedFilePath string
	}{
		{pageURI: "/", expectedFilePath: "index.html"},
		{pageURI: "/catalog/product", expectedFilePath: "catalog/product/index.html"},
		{pageURI: "/blog/2024/post", expectedFilePath: "blog/2024/post/index.html"},
		{pageURI: "/assets/style.css", expectedFilePath: "assets/style.css"},
		{pageURI: "/page.html", expectedFilePath: "page.html"},
	}
	for _, testCase := range testCases {
		actualFilePath := staticArchivePathForURI(testCase.pageURI)
		if actualFilePath != testCase.expectedFilePath {
			t.Fatalf("staticArchivePathForURI(%q) = %q, want %q", testCase.pageURI, actualFilePath, testCase.expectedFilePath)
		}
	}
}

func TestBackupArchivePathAllowedRequiresBackupRoot(t *testing.T) {
	application := &App{storagePath: t.TempDir()}
	backupRoot := application.backupRootDir()
	insideArchive := filepath.Join(backupRoot, "site.zip")
	outsideArchive := filepath.Join(t.TempDir(), "site.zip")
	if !application.backupArchivePathAllowed(insideArchive) {
		t.Fatalf("archive under backup root was rejected: %s", insideArchive)
	}
	if application.backupArchivePathAllowed(outsideArchive) {
		t.Fatalf("archive outside backup root was allowed: %s", outsideArchive)
	}
	if application.backupArchivePathAllowed(filepath.Join(backupRoot, "site.txt")) {
		t.Fatal("non-zip backup archive was allowed")
	}
}

func TestImportDomainBackupRejectsUnsafeZIPEntry(t *testing.T) {
	var archiveBuffer bytes.Buffer
	zipWriter := zip.NewWriter(&archiveBuffer)
	unsafeWriter, err := zipWriter.Create("../outside.txt")
	if err != nil {
		t.Fatalf("create unsafe entry: %v", err)
	}
	if _, err := unsafeWriter.Write([]byte("outside")); err != nil {
		t.Fatalf("write unsafe entry: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	backupZIP, err := zip.NewReader(bytes.NewReader(archiveBuffer.Bytes()), int64(archiveBuffer.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	_, err = (&App{}).importDomainBackupZIP(context.Background(), "localhost", "/", backupZIP)
	if err == nil || !strings.Contains(err.Error(), "unsafe backup entry") {
		t.Fatalf("unsafe backup entry error = %v", err)
	}
}

func TestBackupExportWritesStaticStructureFromURI(t *testing.T) {
	application, rawDB := newTestApplication(t)
	homeHTML := `<h1>home</h1><a href="/catalog/product">Product</a><a href="/page.html?view=1#top">Page</a><a href="http://localhost/blog/2024/post">Same host</a><img src="/p/assets/logo.png"><img srcset="/p/assets/logo.png 1x, /p/assets/logo@2x.png 2x"><link rel="stylesheet" href="/p/assets/style.css"><script src="/p/assets/app.js"></script><a href="https://external.test/path">External</a>`
	productRevisionHTML := `<h1>revision</h1><a href="/">Home</a><a href="/blog/2024/post#read">Post</a><img src="/p/assets/logo.png">`
	_, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/", "Home", homeHTML)
	if err != nil {
		t.Fatalf("insert /: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/catalog/product", "Product", "<h1>draft</h1>")
	if err != nil {
		t.Fatalf("insert /catalog/product: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/page.html", "Page", "<h1>page</h1>")
	if err != nil {
		t.Fatalf("insert /page.html: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/blog/2024/post", "Post", "<h1>post</h1>")
	if err != nil {
		t.Fatalf("insert /blog/2024/post: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/catalog/product", productRevisionHTML, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision /catalog/product: %v", err)
	}

	domainFilesDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(filepath.Join(domainFilesDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	styleCSS := `@import url("/p/assets/fonts.css"); body{background:url("/p/assets/logo.png")} @font-face{src:url("/p/assets/font.woff2")}`
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "style.css"), []byte(styleCSS), 0o644); err != nil {
		t.Fatalf("write style.css: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "fonts.css"), []byte(`@font-face{src:url("/p/assets/font.woff2")}`), 0o644); err != nil {
		t.Fatalf("write fonts.css: %v", err)
	}
	appJS := `const logoPath = "/p/assets/logo.png"; const productPath = "/catalog/product"; const externalPath = "https://external.test/app.js";`
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "app.js"), []byte(appJS), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	for _, assetName := range []string{"logo.png", "logo@2x.png", "font.woff2"} {
		if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", assetName), []byte(assetName), 0o644); err != nil {
			t.Fatalf("write %s: %v", assetName, err)
		}
	}

	var archiveBuffer bytes.Buffer
	if err := application.writeDomainBackupZIP(context.Background(), "localhost", &archiveBuffer); err != nil {
		t.Fatalf("writeDomainBackupZIP: %v", err)
	}
	archiveReader, err := zip.NewReader(bytes.NewReader(archiveBuffer.Bytes()), int64(archiveBuffer.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	archiveFileByName := make(map[string]*zip.File, len(archiveReader.File))
	for _, archiveFile := range archiveReader.File {
		archiveFileByName[archiveFile.Name] = archiveFile
	}
	for _, expectedFileName := range []string{
		"backup.json",
		"index.html",
		"catalog/product/index.html",
		"page.html",
		"blog/2024/post/index.html",
		"p/assets/style.css",
		"p/assets/fonts.css",
		"p/assets/app.js",
		"p/assets/logo.png",
	} {
		if _, exists := archiveFileByName[expectedFileName]; !exists {
			t.Fatalf("backup archive missing %q", expectedFileName)
		}
	}

	rootPageBody := readZipTextFile(t, archiveFileByName, "index.html")
	for _, expectedFragment := range []string{
		`href="catalog/product/index.html"`,
		`href="page.html?view=1#top"`,
		`href="blog/2024/post/index.html"`,
		`src="p/assets/logo.png"`,
		`srcset="p/assets/logo.png 1x, p/assets/logo@2x.png 2x"`,
		`href="p/assets/style.css"`,
		`src="p/assets/app.js"`,
		`href="https://external.test/path"`,
	} {
		if !strings.Contains(rootPageBody, expectedFragment) {
			t.Fatalf("index.html missing rewritten fragment %q in %q", expectedFragment, rootPageBody)
		}
	}

	productPageBody := readZipTextFile(t, archiveFileByName, "catalog/product/index.html")
	for _, expectedFragment := range []string{
		`<h1>revision</h1>`,
		`href="../../index.html"`,
		`href="../../blog/2024/post/index.html#read"`,
		`src="../../p/assets/logo.png"`,
	} {
		if !strings.Contains(productPageBody, expectedFragment) {
			t.Fatalf("catalog/product/index.html missing rewritten fragment %q in %q", expectedFragment, productPageBody)
		}
	}

	styleBody := readZipTextFile(t, archiveFileByName, "p/assets/style.css")
	for _, expectedFragment := range []string{
		`@import url("fonts.css")`,
		`url("logo.png")`,
		`url("font.woff2")`,
	} {
		if !strings.Contains(styleBody, expectedFragment) {
			t.Fatalf("p/assets/style.css missing rewritten fragment %q in %q", expectedFragment, styleBody)
		}
	}

	scriptBody := readZipTextFile(t, archiveFileByName, "p/assets/app.js")
	for _, expectedFragment := range []string{
		`const logoPath = "logo.png"`,
		`const productPath = "../../catalog/product/index.html"`,
		`const externalPath = "https://external.test/app.js"`,
	} {
		if !strings.Contains(scriptBody, expectedFragment) {
			t.Fatalf("p/assets/app.js missing rewritten fragment %q in %q", expectedFragment, scriptBody)
		}
	}
}

func TestBillingDeleteSiteCreatesVerifiedBackupBeforeRemovingData(t *testing.T) {
	application, _ := newTestApplication(t)
	domain := "customer.example"
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName(domain)+".db")
	if err := ensureParentDir(siteDatabasePath); err != nil {
		t.Fatal(err)
	}
	siteDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()
	siteApplication := &App{db: siteDB, storagePath: application.storagePath, dbPath: application.serverControlDBPath()}
	if err := siteApplication.migrate(contextWithDomain(context.Background(), domain)); err != nil {
		t.Fatalf("migrate site db: %v", err)
	}
	insertSiteQuotaAdmin(t, siteDB, domain)
	if _, err := siteDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, "/", "Home", "<h1>customer</h1>"); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	if err := os.MkdirAll(application.domainStaticDir(domain), 0o755); err != nil {
		t.Fatalf("create static dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(application.domainStaticDir(domain), "index.html"), []byte("<h1>static</h1>"), 0o644); err != nil {
		t.Fatalf("write static file: %v", err)
	}
	if err := os.MkdirAll(application.domainFilesDirForDomain(domain), 0o755); err != nil {
		t.Fatalf("create files dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(application.domainFilesDirForDomain(domain), "logo.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write file asset: %v", err)
	}
	controlDB, err := openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlDB.Exec(`INSERT INTO server_managers(domain,email,role,scope_domain,created_at) VALUES(?,?,?,?,?)`, "owner.example", "owner@example.com", "owner", "*", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := controlDB.Exec(`INSERT INTO site_service_assignments(domain,plan_id,service_status,updated_at) VALUES(?,?,?,?)`, domain, 0, "paid", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert assignment: %v", err)
	}
	_ = controlDB.Close()

	request := httptest.NewRequest(http.MethodPost, "http://owner.example/?billing", nil)
	backup, err := application.deleteManagedSiteWithBackup(context.Background(), request, domain, hostingandsupport.DefaultDeletionBackupRetentionDays)
	if err != nil {
		t.Fatalf("delete with backup: %v", err)
	}
	if backup.FileName == "" || backup.DownloadURL == "" {
		t.Fatalf("backup view missing file or URL: %+v", backup)
	}
	if _, err := os.Stat(siteDatabasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("site database still exists or stat failed differently: %v", err)
	}
	if _, err := os.Stat(application.domainStaticDir(domain)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("site static dir still exists or stat failed differently: %v", err)
	}
	backupPath := filepath.Join(application.backupRootDir(), backup.FileName)
	archiveReader, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatalf("open deletion backup: %v", err)
	}
	defer archiveReader.Close()
	archiveFileByName := make(map[string]*zip.File, len(archiveReader.File))
	for _, archiveFile := range archiveReader.File {
		archiveFileByName[archiveFile.Name] = archiveFile
	}
	for _, expectedName := range []string{
		"backup.json",
		"metadata.json",
		"database/" + filepath.Base(siteDatabasePath),
		"static/" + domainStorageName(domain) + "/index.html",
		"p/logo.png",
	} {
		if _, found := archiveFileByName[expectedName]; !found {
			t.Fatalf("deletion backup missing %q", expectedName)
		}
	}
	metadataBody := readZipTextFile(t, archiveFileByName, "metadata.json")
	if !strings.Contains(metadataBody, "admin@customer.example") || !strings.Contains(metadataBody, "owner@example.com") {
		t.Fatalf("metadata missing owner contacts: %s", metadataBody)
	}
	controlDB, err = openServerControlDatabaseForTest(context.Background(), application)
	if err != nil {
		t.Fatal(err)
	}
	defer controlDB.Close()
	var assignmentCount int
	if err := controlDB.QueryRow(`SELECT COUNT(1) FROM site_service_assignments WHERE domain=?`, domain).Scan(&assignmentCount); err != nil {
		t.Fatalf("read assignment count: %v", err)
	}
	if assignmentCount != 0 {
		t.Fatalf("assignment count = %d, want 0", assignmentCount)
	}
	var token string
	if err := controlDB.QueryRow(`SELECT token FROM site_deletion_backups WHERE domain=?`, domain).Scan(&token); err != nil {
		t.Fatalf("read deletion backup token: %v", err)
	}
	if token == "" {
		t.Fatal("backup token is empty")
	}
	select {
	case mailJob := <-application.emailDelivery:
		if mailJob.Message.To != "admin@customer.example" && mailJob.Message.To != "owner@example.com" {
			t.Fatalf("unexpected backup email recipient: %+v", mailJob.Message)
		}
	default:
		t.Fatal("backup creation email was not queued")
	}

	response := httptest.NewRecorder()
	application.downloadManagedSiteDeletionBackup(response, httptest.NewRequest(http.MethodGet, "http://owner.example/?billing_backup_download&token="+url.QueryEscape(token), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("download status = %d, body=%q", response.Code, response.Body.String())
	}
	var downloadCount int
	if err := controlDB.QueryRow(`SELECT download_count FROM site_deletion_backups WHERE domain=?`, domain).Scan(&downloadCount); err != nil {
		t.Fatalf("read download count: %v", err)
	}
	if downloadCount != 1 {
		t.Fatalf("download count = %d, want 1", downloadCount)
	}
}

func TestBillingDeleteSiteKeepsDataWhenBackupCreationFails(t *testing.T) {
	application, _ := newTestApplication(t)
	domain := "blocked-backup.example"
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName(domain)+".db")
	if err := ensureParentDir(siteDatabasePath); err != nil {
		t.Fatal(err)
	}
	siteDB, err := sql.Open("sqlite", "file:"+siteDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()
	if err := (&App{db: siteDB, storagePath: application.storagePath, dbPath: application.serverControlDBPath()}).migrate(contextWithDomain(context.Background(), domain)); err != nil {
		t.Fatalf("migrate site db: %v", err)
	}
	insertSiteQuotaAdmin(t, siteDB, domain)
	if err := os.WriteFile(application.backupRootDir(), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("block backup dir: %v", err)
	}

	_, err = application.deleteManagedSiteWithBackup(context.Background(), httptest.NewRequest(http.MethodPost, "http://owner.example/?billing", nil), domain, hostingandsupport.DefaultDeletionBackupRetentionDays)
	if err == nil {
		t.Fatal("delete succeeded despite backup directory failure")
	}
	if _, statErr := os.Stat(siteDatabasePath); statErr != nil {
		t.Fatalf("site database was removed after backup failure: %v", statErr)
	}
}

func readZipTextFile(t *testing.T, archiveFileByName map[string]*zip.File, fileName string) string {
	t.Helper()
	archiveFile, exists := archiveFileByName[fileName]
	if !exists {
		t.Fatalf("backup archive missing %s", fileName)
	}
	archiveFileReader, err := archiveFile.Open()
	if err != nil {
		t.Fatalf("open %s: %v", fileName, err)
	}
	fileBytes, readErr := io.ReadAll(archiveFileReader)
	_ = archiveFileReader.Close()
	if readErr != nil {
		t.Fatalf("read %s: %v", fileName, readErr)
	}
	return string(fileBytes)
}

func TestSendHostingSnapshotNetChanPayloadDeliversAndCloses(t *testing.T) {
	listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	payload := []byte(`{"kind":"hosting_snapshot"}`)
	receivedPayload := make(chan []byte, 1)
	serverFinished := make(chan error, 1)
	go func() {
		connection, isOpen := <-listener.Channels
		if !isOpen {
			serverFinished <- errors.New("netchan listener closed before accepting a connection")
			return
		}
		incomingTask, isOpen := <-connection.Receive
		if !isOpen {
			serverFinished <- errors.New("netchan connection closed before receiving payload")
			return
		}
		receivedPayload <- append([]byte(nil), incomingTask.Payload...)
		incomingTask.Response <- sitebrushNetChanResponse{Status: "stored", StatusCode: http.StatusOK}
		<-connection.Done
		serverFinished <- nil
	}()

	workerStop := make(chan struct{})
	defer close(workerStop)
	deliveries := startHostingSnapshotNetChanDeliveryWorker(workerStop)
	consumerStop := make(chan struct{})
	defer close(consumerStop)
	if err := submitHostingSnapshotNetChanDelivery(consumerStop, deliveries, listener.Address, payload); err != nil {
		t.Fatalf("send payload: %v", err)
	}
	if err := <-serverFinished; err != nil {
		t.Fatalf("close server connection: %v", err)
	}
	if received := <-receivedPayload; !bytes.Equal(received, payload) {
		t.Fatalf("received payload = %q, want %q", received, payload)
	}
}

func TestSendHostingSnapshotNetChanPayloadCancelsUnreadDelivery(t *testing.T) {
	listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	workerStop := make(chan struct{})
	defer close(workerStop)
	deliveries := startHostingSnapshotNetChanDeliveryWorker(workerStop)
	consumerStop := make(chan struct{})
	sendFinished := make(chan error, 1)
	go func() {
		sendFinished <- submitHostingSnapshotNetChanDelivery(consumerStop, deliveries, listener.Address, []byte("unread"))
	}()

	connection, isOpen := <-listener.Channels
	if !isOpen {
		t.Fatal("netchan listener closed before accepting a connection")
	}
	close(consumerStop)
	select {
	case err := <-sendFinished:
		if !errors.Is(err, errHostingSnapshotNetChanDeliveryCanceled) {
			t.Fatalf("send error = %v, want delivery canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled netchan delivery did not return")
	}
	if err := connection.Abort(); err != nil {
		t.Fatalf("abort server connection: %v", err)
	}
}

func TestHostingSnapshotNetChanConsumerReusesResponseChannel(t *testing.T) {
	listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverFinished := make(chan error, 1)
	go func() {
		for taskIndex := 0; taskIndex < 2; taskIndex++ {
			connection, isOpen := <-listener.Channels
			if !isOpen {
				serverFinished <- errors.New("netchan listener closed before accepting both tasks")
				return
			}
			task, isOpen := <-connection.Receive
			if !isOpen {
				serverFinished <- errors.New("netchan connection closed before receiving task")
				return
			}
			task.Response <- sitebrushNetChanResponse{Status: "stored", StatusCode: http.StatusOK}
			<-connection.Done
		}
		serverFinished <- nil
	}()

	workerStop := make(chan struct{})
	defer close(workerStop)
	deliveries := startHostingSnapshotNetChanDeliveryWorker(workerStop)
	consumerStop := make(chan struct{})
	defer close(consumerStop)
	response := make(chan hostingSnapshotNetChanDeliveryResponse, 1)
	for taskIndex := 0; taskIndex < 2; taskIndex++ {
		payload := []byte(strconv.Itoa(taskIndex))
		if err := submitHostingSnapshotNetChanDeliveryWithResponse(consumerStop, deliveries, response, listener.Address, payload); err != nil {
			t.Fatalf("send task %d: %v", taskIndex, err)
		}
	}
	if err := <-serverFinished; err != nil {
		t.Fatal(err)
	}
}

func TestHostingSnapshotNetChanListenerReceivesMalformedPayload(t *testing.T) {
	listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	listenerStop := make(chan struct{})
	application := &App{}
	go application.serveHostingSnapshotNetChanListener(listenerStop, context.Background(), listener)
	workerStop := make(chan struct{})
	defer close(workerStop)
	deliveries := startHostingSnapshotNetChanDeliveryWorker(workerStop)
	consumerStop := make(chan struct{})
	defer close(consumerStop)
	if err := submitHostingSnapshotNetChanDelivery(consumerStop, deliveries, listener.Address, []byte("{")); err == nil {
		t.Fatal("malformed payload was accepted")
	}
	close(listenerStop)
	select {
	case <-listener.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("netchan listener did not stop after malformed payload")
	}
}

func TestHostingSnapshotNetChanDeliverySkipsTaskWithClosedResponse(t *testing.T) {
	listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	workerStop := make(chan struct{})
	defer close(workerStop)
	deliveries := startHostingSnapshotNetChanDeliveryWorker(workerStop)
	response := make(chan hostingSnapshotNetChanDeliveryResponse)
	close(response)
	deliveries <- hostingSnapshotNetChanDeliveryTask{address: listener.Address, payload: []byte("unneeded"), response: response}

	select {
	case <-listener.Channels:
		t.Fatal("worker started a task whose response channel was closed")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHostingSnapshotNetChanListenerStopsActiveConnections(t *testing.T) {
	listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	application := &App{}
	go application.serveHostingSnapshotNetChanListener(stop, context.Background(), listener)
	connection, err := netchan.Dial[sitebrushNetChanRequest](listener.Address)
	if err != nil {
		close(stop)
		t.Fatal(err)
	}
	response := make(chan sitebrushNetChanResponse, 1)
	if err := connection.Deliver(sitebrushNetChanRequest{Payload: []byte("{"), Response: response}); err != nil {
		close(stop)
		t.Fatalf("deliver payload before listener shutdown: %v", err)
	}
	if result := <-response; result.StatusCode != http.StatusBadRequest {
		close(stop)
		t.Fatalf("malformed payload status = %d, want %d", result.StatusCode, http.StatusBadRequest)
	}
	close(stop)

	select {
	case <-listener.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("netchan listener did not stop")
	}
	if err := connection.Abort(); err != nil {
		t.Fatalf("abort client connection: %v", err)
	}
}
