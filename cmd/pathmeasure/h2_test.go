package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func startH2TestServer(t *testing.T, server *http.Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("HTTP/2 server did not stop")
		}
	})
	return "http://" + ln.Addr().String()
}

func h2TestTransport(t *testing.T) *http.Transport {
	t.Helper()
	d, err := dialer("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	transport := clearTextH2Transport(d)
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

// Read what peers actually receive, including connection credit, rather than
// just checking that configuration fields were assigned.
func TestClearTextH2AdvertisesWindows(t *testing.T) {
	cases := []struct {
		window             int
		stream, connection uint32
	}{
		{0, 1 << 20, 1 << 20}, {-1, 1 << 20, 1 << 20},
		{1024, 1024, 1 << 20}, {65535, 65535, 65535},
		{8 << 20, 8 << 20, 8 << 20}, {math.MaxInt32, math.MaxInt32, math.MaxInt32},
	}
	if strconv.IntSize == 64 {
		invalid := int64(math.MaxInt32) + 1
		cases = append(cases, struct {
			window             int
			stream, connection uint32
		}{int(invalid), 1 << 20, 1 << 20})
	}
	for _, tt := range cases {
		t.Run(strconv.Itoa(tt.window), func(t *testing.T) {
			addr := startH2TestServer(t, clearTextH2Server(tt.window, http.NotFoundHandler()))
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(addr, "http://"), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
				t.Fatal(err)
			}
			framer := http2.NewFramer(conn, conn)
			if err := framer.WriteSettings(); err != nil {
				t.Fatal(err)
			}
			ping := [8]byte{'w', 'i', 'n', 'd', 'o', 'w'}
			if err := framer.WritePing(false, ping); err != nil {
				t.Fatal(err)
			}
			var stream uint32
			connection := uint32(65535)
			// The PING acknowledgment follows the initial settings and window
			// update, including when the connection window stays at 65535.
		readFrames:
			for {
				frame, err := framer.ReadFrame()
				if err != nil {
					t.Fatal(err)
				}
				switch f := frame.(type) {
				case *http2.SettingsFrame:
					if value, ok := f.Value(http2.SettingInitialWindowSize); ok {
						stream = value
					}
				case *http2.WindowUpdateFrame:
					if f.StreamID == 0 {
						connection += f.Increment
					}
				case *http2.PingFrame:
					if f.IsAck() && f.Data == ping {
						break readFrames
					}
				}
			}
			if stream != tt.stream || connection != tt.connection {
				t.Fatalf("advertised stream/connection windows = %d/%d, want %d/%d", stream, connection, tt.stream, tt.connection)
			}
		})
	}
}

func TestClearTextH2UploadReusesBoundConnection(t *testing.T) {
	type observation struct {
		remote string
		length int64
		proto  int
	}
	requests := make(chan observation, 3)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requests <- observation{r.RemoteAddr, r.ContentLength, r.ProtoMajor}
		fmt.Fprintf(w, "%d", n)
	})
	addr := startH2TestServer(t, clearTextH2Server(65535, handler))
	client := &http.Client{Transport: h2TestTransport(t), Timeout: 5 * time.Second}
	body := bytes.Repeat([]byte("upload"), 32768)
	var remote string
	for i := 0; i < 2; i++ {
		resp, err := client.Post(addr, "application/octet-stream", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.ProtoMajor != 2 || string(data) != strconv.Itoa(len(body)) {
			t.Fatalf("response protocol/body = %s/%q", resp.Proto, data)
		}
		observed := <-requests
		host, _, err := net.SplitHostPort(observed.remote)
		if err != nil || host != "127.0.0.1" || observed.proto != 2 || observed.length != int64(len(body)) {
			t.Fatalf("request = %+v", observed)
		}
		if i > 0 && observed.remote != remote {
			t.Fatalf("connection changed from %s to %s", remote, observed.remote)
		}
		remote = observed.remote
	}
	// OPTIONS * must still reach the measurement handler.
	req, err := http.NewRequest(http.MethodOptions, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.Path = "*"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(data) != "0" {
		t.Fatalf("OPTIONS * response = %q, %v", data, err)
	}
}

func TestClearTextH2DoesNotFallBackToHTTP1(t *testing.T) {
	t.Run("server", func(t *testing.T) {
		called := make(chan struct{}, 1)
		addr := startH2TestServer(t, clearTextH2Server(0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called <- struct{}{} })))
		transport := &http.Transport{}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		resp, err := client.Get(addr)
		if err == nil {
			resp.Body.Close()
			t.Fatal("HTTP/1 request unexpectedly succeeded")
		}
		select {
		case <-called:
			t.Fatal("HTTP/1 request reached HTTP/2 handler")
		default:
		}
	})
	t.Run("client", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "HTTP/1") }))
		defer server.Close()
		client := &http.Client{Transport: h2TestTransport(t), Timeout: 5 * time.Second}
		resp, err := client.Get(server.URL)
		if err == nil {
			resp.Body.Close()
			t.Fatal("HTTP/2 client fell back to HTTP/1")
		}
	})
	t.Run("source-address", func(t *testing.T) {
		addr := startH2TestServer(t, clearTextH2Server(0, http.NotFoundHandler()))
		// An IPv6-only source cannot dial this IPv4 listener. Success would mean
		// the supplied dialer's source binding was lost during the migration.
		d, err := dialer("::1")
		if err != nil {
			t.Fatal(err)
		}
		transport := clearTextH2Transport(d)
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		resp, err := client.Get(addr)
		if err == nil {
			resp.Body.Close()
			t.Fatal("transport ignored the requested source address")
		}
	})
}

func TestH2ProxyStreamsUploadAndPreservesResponse(t *testing.T) {
	first := make(chan struct{}, 1)
	upstream := startH2TestServer(t, clearTextH2Server(65535, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.URL.RequestURI() != "/upload?q=1" || r.Header.Get("X-Request") != "probe" || r.ContentLength != -1 {
			t.Errorf("forwarded request = %s %s headers=%v length=%d", r.Proto, r.URL, r.Header, r.ContentLength)
		}
		prefix := make([]byte, 4)
		if _, err := io.ReadFull(r.Body, prefix); err != nil {
			t.Error(err)
			return
		}
		first <- struct{}{}
		rest, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("X-Response", "forwarded")
		w.WriteHeader(http.StatusCreated)
		w.Write(append(prefix, rest...))
	})))
	upstreamClient := &http.Client{Transport: h2TestTransport(t), Timeout: 5 * time.Second}
	proxy := startH2TestServer(t, clearTextH2Server(65535, h2ProxyHandler(strings.TrimPrefix(upstream, "http://"), upstreamClient)))
	client := &http.Client{Transport: h2TestTransport(t), Timeout: 5 * time.Second}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req, err := http.NewRequest(http.MethodPost, proxy+"/upload?q=1", reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request", "probe")
	done := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && (resp.ProtoMajor != 2 || resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Response") != "forwarded" || string(body) != "headtail") {
			err = fmt.Errorf("proxy response = %s %d %v %q", resp.Proto, resp.StatusCode, resp.Header, body)
		}
		done <- err
	}()
	if _, err := io.WriteString(writer, "head"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first: // Upstream sees the prefix before the input stream is closed.
	case <-time.After(5 * time.Second):
		t.Fatal("proxy buffered the upload instead of streaming")
	}
	if _, err := io.WriteString(writer, "tail"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy response did not finish")
	}
}

func TestH2ProxyPropagatesCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	upstream := startH2TestServer(t, clearTextH2Server(0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	})))
	upstreamClient := &http.Client{Transport: h2TestTransport(t), Timeout: 5 * time.Second}
	proxy := startH2TestServer(t, clearTextH2Server(0, h2ProxyHandler(strings.TrimPrefix(upstream, "http://"), upstreamClient)))
	client := &http.Client{Transport: h2TestTransport(t), Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request was not canceled")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("client error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client request did not stop")
	}
}
