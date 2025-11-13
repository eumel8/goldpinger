// Copyright 2018 Bloomberg Finance L.P.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package goldpinger

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
)

func doDNSProbe(addr string, timeout time.Duration) error {
	zap.L().Debug("Starting DNS probe", zap.String("addr", addr), zap.Duration("timeout", timeout))

	resolver := net.Resolver{}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startTime := time.Now()
	ips, err := resolver.LookupHost(ctx, addr)
	duration := time.Since(startTime)

	if err != nil {
		zap.L().Warn("DNS probe failed",
			zap.String("addr", addr),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		return err
	}

	if len(ips) == 0 {
		zap.L().Warn("DNS probe resolved to 0 IPs",
			zap.String("addr", addr),
			zap.Duration("duration", duration),
		)
		return fmt.Errorf("%s was resolved to 0 ips", addr)
	}

	zap.L().Info("DNS probe successful",
		zap.String("addr", addr),
		zap.Strings("resolved_ips", ips),
		zap.Int("ip_count", len(ips)),
		zap.Duration("duration", duration),
	)
	return nil
}

func doTCPProbe(addr string, timeout time.Duration) error {
	zap.L().Debug("Starting TCP probe", zap.String("addr", addr), zap.Duration("timeout", timeout))

	startTime := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	duration := time.Since(startTime)

	if err != nil {
		// Categorize the error for better debugging
		var errType string
		if netErr, ok := err.(net.Error); ok {
			if netErr.Timeout() {
				errType = "timeout"
			} else if netErr.Temporary() {
				errType = "temporary"
			} else {
				errType = "network"
			}
		} else {
			errType = "unknown"
		}

		zap.L().Warn("TCP probe failed",
			zap.String("addr", addr),
			zap.String("error_type", errType),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		return err
	}

	if conn != nil {
		localAddr := conn.LocalAddr().String()
		remoteAddr := conn.RemoteAddr().String()

		zap.L().Info("TCP probe successful",
			zap.String("addr", addr),
			zap.String("local_addr", localAddr),
			zap.String("remote_addr", remoteAddr),
			zap.Duration("duration", duration),
		)
		defer conn.Close()
	}

	return nil
}

func doHTTPProbe(addr string, timeout time.Duration) error {
	zap.L().Debug("Starting HTTP probe", zap.String("addr", addr), zap.Duration("timeout", timeout))

	startTime := time.Now()

	// Parse URL
	u, err := url.Parse(addr)
	if err != nil {
		zap.L().Error("Failed to parse URL",
			zap.String("addr", addr),
			zap.Error(err),
		)
		return err
	}

	// Validate scheme
	if u.Scheme != "http" && u.Scheme != "https" {
		zap.L().Error("Invalid URL scheme",
			zap.String("addr", addr),
			zap.String("scheme", u.Scheme),
		)
		return fmt.Errorf("invalid url scheme: '%s' in address", u.Scheme)
	}

	zap.L().Debug("URL parsed successfully",
		zap.String("addr", addr),
		zap.String("scheme", u.Scheme),
		zap.String("host", u.Host),
		zap.String("path", u.Path),
	)

	// Configure HTTP client
	client := http.Client{Timeout: timeout}

	// Configure transport based on scheme
	if u.Scheme == "https" {
		zap.L().Debug("Configuring HTTPS transport with TLS skip verify",
			zap.String("addr", addr),
		)
		client.Transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	} else {
		zap.L().Debug("Configuring HTTP transport",
			zap.String("addr", addr),
		)
		client.Transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}

	// Perform HTTP GET request
	zap.L().Debug("Sending HTTP GET request", zap.String("addr", addr))
	resp, err := client.Get(addr)
	duration := time.Since(startTime)

	if err != nil {
		// Categorize the error for better debugging
		var errType string
		if netErr, ok := err.(net.Error); ok {
			if netErr.Timeout() {
				errType = "timeout"
			} else if netErr.Temporary() {
				errType = "temporary"
			} else {
				errType = "network"
			}
		} else if urlErr, ok := err.(*url.Error); ok {
			errType = fmt.Sprintf("url_error_%s", urlErr.Op)
		} else {
			errType = "unknown"
		}

		zap.L().Warn("HTTP probe request failed",
			zap.String("addr", addr),
			zap.String("error_type", errType),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
		return err
	}

	defer resp.Body.Close()

	// Log response details
	contentLength := resp.ContentLength
	contentType := resp.Header.Get("Content-Type")
	server := resp.Header.Get("Server")

	zap.L().Debug("HTTP probe received response",
		zap.String("addr", addr),
		zap.Int("status_code", resp.StatusCode),
		zap.String("status", resp.Status),
		zap.String("proto", resp.Proto),
		zap.Int64("content_length", contentLength),
		zap.String("content_type", contentType),
		zap.String("server", server),
		zap.Duration("duration", duration),
	)

	// Check status code
	if resp.StatusCode != 200 {
		zap.L().Warn("HTTP probe failed with non-200 status code",
			zap.String("addr", addr),
			zap.Int("status_code", resp.StatusCode),
			zap.String("status", resp.Status),
			zap.Duration("duration", duration),
		)
		return fmt.Errorf("%s returned non-200 resp: %d", addr, resp.StatusCode)
	}

	zap.L().Info("HTTP probe successful",
		zap.String("addr", addr),
		zap.Int("status_code", resp.StatusCode),
		zap.Duration("duration", duration),
	)

	return nil
}
