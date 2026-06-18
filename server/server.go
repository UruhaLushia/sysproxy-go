//go:build sysproxy_server

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/UruhaLushia/sysproxy-go/sysproxy"
)

const (
	DefaultNetwork     = "tcp"
	DefaultTCPAddress  = "127.0.0.1:0"
	DefaultUnixAddress = "/tmp/sparkle-helper.sock"
)

type Options struct {
	Network string
	Address string
}

type Request struct {
	Server string `json:"server"`
	Bypass string `json:"bypass"`
	URL    string `json:"url"`

	Device           string `json:"device,omitempty"`
	OnlyActiveDevice bool   `json:"only_active_device,omitempty"`
}

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func Start(opt Options) error {
	listener, err := Listen(opt)
	if err != nil {
		return err
	}

	log.Printf("%s listening at: %s", listener.Addr().Network(), listener.Addr().String())
	return (&http.Server{Handler: Handler()}).Serve(listener)
}

func Listen(opt Options) (net.Listener, error) {
	network := strings.ToLower(strings.TrimSpace(opt.Network))
	if network == "" {
		network = DefaultNetwork
	}
	address := strings.TrimSpace(opt.Address)

	switch network {
	case "tcp", "tcp4", "tcp6":
		if address == "" {
			address = DefaultTCPAddress
		}
		return net.Listen(network, address)
	case "unix":
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("unix listen is not supported on %s", runtime.GOOS)
		}
		if address == "" {
			address = DefaultUnixAddress
		}
		if err := prepareUnixSocket(address); err != nil {
			return nil, err
		}
		listener, err := net.Listen(network, address)
		if err != nil {
			return nil, fmt.Errorf("unix listen error: %w", err)
		}
		_ = os.Chmod(address, 0o666)
		return listener, nil
	default:
		return nil, fmt.Errorf("unsupported listen network: %s", network)
	}
}

func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", method(http.MethodGet, status))
	mux.HandleFunc("/events", method(http.MethodGet, events))
	mux.HandleFunc("/ping", method(http.MethodGet, ping))
	mux.HandleFunc("/pac", method(http.MethodPost, pac))
	mux.HandleFunc("/proxy", method(http.MethodPost, proxy))
	mux.HandleFunc("/disable", method(http.MethodPost, disable))
	return mux
}

func prepareUnixSocket(address string) error {
	dir := filepath.Dir(address)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("directory creation error: %w", err)
	}
	if err := os.Remove(address); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unlink error: %w", err)
	}
	return nil
}

func method(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func ping(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, "ok", "pong")
}

func status(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status, err := sysproxy.QueryProxySettings(optionsFromQuery(r))
	log.Println("QueryProxySettings took:", time.Since(start))
	if err != nil {
		sendError(w, err)
		return
	}
	writeJSON(w, status)
}

func events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	opts := optionsFromQuery(r)
	sendEvent(w, flusher, "ready", Response{Status: "ok", Message: "ready"})

	for {
		if err := sysproxy.WaitProxySettingsChange(ctx, opts); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			sendEvent(w, flusher, "error", Response{Status: "error", Message: err.Error()})
			return
		}

		status, err := sysproxy.QueryProxySettings(opts)
		if err != nil {
			sendEvent(w, flusher, "error", Response{Status: "error", Message: err.Error()})
			return
		}
		sendEvent(w, flusher, "update", status)
	}
}

func pac(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decodeRequest(r, &req); err != nil {
		sendError(w, err)
		return
	}

	start := time.Now()
	err := sysproxy.SetPac(&sysproxy.Options{PACURL: req.URL, Device: req.Device, OnlyActiveDevice: req.OnlyActiveDevice})
	log.Println("SetPac took:", time.Since(start), "\nURL:", req.URL)
	if err != nil {
		sendError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func proxy(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decodeRequest(r, &req); err != nil {
		sendError(w, err)
		return
	}

	start := time.Now()
	err := sysproxy.SetProxy(&sysproxy.Options{Proxy: req.Server, Bypass: req.Bypass, Device: req.Device, OnlyActiveDevice: req.OnlyActiveDevice})
	log.Println("SetProxy took:", time.Since(start), "\nserver:", req.Server, "\nbypass:", req.Bypass)
	if err != nil {
		sendError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func disable(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decodeRequest(r, &req); err != nil {
		sendError(w, err)
		return
	}

	start := time.Now()
	err := sysproxy.DisableProxy(&sysproxy.Options{Device: req.Device, OnlyActiveDevice: req.OnlyActiveDevice})
	log.Println("DisableProxy took:", time.Since(start))
	if err != nil {
		sendError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func optionsFromQuery(r *http.Request) *sysproxy.Options {
	query := r.URL.Query()
	opt := &sysproxy.Options{
		Device:           query.Get("device"),
		OnlyActiveDevice: true,
	}
	if value := query.Get("only_active_device"); value != "" {
		opt.OnlyActiveDevice, _ = strconv.ParseBool(value)
	}
	return opt
}

func decodeRequest(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func sendEvent(w http.ResponseWriter, flusher http.Flusher, event string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(Response{Status: "error", Message: err.Error()})
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

func sendJSON(w http.ResponseWriter, status string, message string) {
	writeJSON(w, Response{Status: status, Message: message})
}

func sendError(w http.ResponseWriter, err error) {
	sendJSON(w, "error", err.Error())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
