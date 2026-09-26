// probe-minimax probes the minimax cn anthropic endpoint with the
// supplied key. Use only for one-off connectivity debugging — it
// disables TLS verification when MINIMAX_INSECURE=1 so we can work
// around the platform's expired certificate.
//
// Usage: probe-minimax --key <api_key> [--insecure]
package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

func main() {
	key := flag.String("key", "", "API key")
	insecure := flag.Bool("insecure", false, "skip TLS verification (debug only)")
	url := flag.String("url", "https://api.minimaxi.cn/anthropic/v1/messages", "endpoint URL")
	flag.Parse()

	if *key == "" {
		log.Fatal("--key required")
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if *insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		fmt.Println("TLS verification DISABLED (debug only)")
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: tr}

	body := `{"model":"MiniMax-M3","max_tokens":16,"system":"你是小助理。","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", *url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", *key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Authorization", "Bearer "+*key)

	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("FAIL after %v: %v\n", time.Since(t0), err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("status=%d in %v\n", resp.StatusCode, time.Since(t0))
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
	fmt.Println(string(b))
	// Print any certificate details for debugging
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		for i, c := range resp.TLS.PeerCertificates {
			fmt.Printf("cert[%d] subject=%s issuer=%s expiry=%s\n",
				i, c.Subject, c.Issuer, c.NotAfter.Format(time.RFC3339))
		}
	}
	_ = bytes.MinRead
}