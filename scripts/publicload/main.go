// publicload keeps DNS, TCP, TLS and response-wait samples separate.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/publicload"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("url", "", "HTTP/HTTPS URL to measure")
	requests := flag.Int("requests", 10000, "total request count")
	concurrency := flag.Int("concurrency", 300, "parallel clients released together")
	fresh := flag.Bool("new-connections", false, "open a new TCP/TLS connection for each request")
	http2 := flag.Bool("http2", false, "allow HTTP/2; the report includes actual protocols")
	resume := flag.Bool("tls-resume", false, "enable TLS session resumption")
	tlsVersion := flag.String("tls-version", "auto", "TLS version: auto, 1.2, or 1.3")
	warmup := flag.Int("warmup", 0, "unmeasured requests before each run")
	repeats := flag.Int("repeat", 1, "number of independent measured runs")
	burst := flag.Bool("burst", false, "release each wave of clients together")
	allowRejections := flag.Bool("allow-non-2xx", false, "accept expected security rejections")
	certificateFile := flag.String("ca", "", "optional trusted PEM certificate file for a local HTTPS fixture")
	expected := flag.Int64("bytes", 0, "expected response body size; zero disables length comparison")
	expectedDigest := flag.String("sha256", "", "expected response SHA256 digest")
	deadline := flag.Duration("timeout", 15*time.Second, "timeout per request")
	flag.Parse()
	parsed, err := url.Parse(*address)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || *concurrency <= 0 || *requests <= 0 || *deadline <= 0 || *warmup < 0 || *repeats < 1 {
		return errors.New("use -url http[s]://host/path with positive requests, concurrency and timeout")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	switch *tlsVersion {
	case "auto":
	case "1.2":
		tlsConfig.MaxVersion = tls.VersionTLS12
	case "1.3":
		tlsConfig.MinVersion = tls.VersionTLS13
	default:
		return errors.New("tls-version must be auto, 1.2, or 1.3")
	}
	if *resume {
		tlsConfig.ClientSessionCache = tls.NewLRUClientSessionCache(*concurrency)
	}
	if *certificateFile != "" {
		encoded, readErr := os.ReadFile(*certificateFile)
		if readErr != nil {
			return readErr
		}
		roots, poolErr := x509.SystemCertPool()
		if poolErr != nil {
			return poolErr
		}
		if !roots.AppendCertsFromPEM(encoded) {
			return errors.New("CA file has no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	transport := &http.Transport{MaxIdleConns: *concurrency, MaxIdleConnsPerHost: *concurrency, DisableKeepAlives: *fresh, ForceAttemptHTTP2: *http2, DisableCompression: true, TLSClientConfig: tlsConfig}
	// HTTP/2 can reuse a connection while overlapping streams are retiring it.
	// Fresh-connection measurements deliberately use HTTP/1.1.
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.Protocols.SetHTTP2(*http2 && !*fresh)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: *deadline, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-signals:
			close(stop)
		case <-finished:
		}
	}()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	for runIndex := 1; runIndex <= *repeats; runIndex++ {
		options := publicload.Options{URL: parsed.String(), Requests: *requests, Concurrency: *concurrency, ExpectedBytes: *expected, ExpectedSHA256: *expectedDigest, Burst: *burst}
		if *warmup > 0 {
			options.Requests = *warmup
			if _, err := publicload.Run(client, options, stop); err != nil {
				return err
			}
			options.Requests = *requests
		}
		report, loadErr := publicload.Run(client, options, stop)
		if encodeErr := encoder.Encode(struct {
			Run int
			publicload.Report
		}{Run: runIndex, Report: report}); encodeErr != nil {
			return errors.Join(loadErr, encodeErr)
		}
		if loadErr != nil {
			return loadErr
		}
		if report.TransportErrors != 0 || report.LengthMismatches != 0 || report.ContentMismatches != 0 || report.IncompleteTraces != 0 || !*allowRejections && report.Non2xx != 0 {
			return errors.New("load run contains failed requests or incomplete measurements")
		}
	}
	return nil
}
