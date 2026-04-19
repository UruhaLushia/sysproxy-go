//go:build darwin

package sysproxy

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type Request struct {
	Server string `json:"server"`
	Bypass string `json:"bypass"`
	Url    string `json:"url"`

	Device           string `json:"device,omitempty"`
	OnlyActiveDevice bool   `json:"only_active_device,omitempty"`
}

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func router() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/status", status)
	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, "ok", "pong")
	})
	r.Post("/pac", pac)
	r.Post("/proxy", proxy)
	r.Post("/disable", disable)
	return r
}

func status(w http.ResponseWriter, r *http.Request) {
	t := time.Now()
	status, err := QueryProxySettings(&Options{Device: "", OnlyActiveDevice: true})
	log.Println("QueryProxySettings took:", time.Since(t))
	if err != nil {
		sendError(w, err)
		return
	}
	render.JSON(w, r, status)
}

func pac(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decodeRequest(r, &req); err != nil {
		sendError(w, err)
		return
	}

	t := time.Now()
	err := SetPac(&Options{PACURL: req.Url, Device: req.Device, OnlyActiveDevice: req.OnlyActiveDevice})
	log.Println("SetPac took:", time.Since(t), "\nURL:", req.Url)
	if err != nil {
		sendError(w, err)
		return
	}
	render.NoContent(w, r)
}

func proxy(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decodeRequest(r, &req); err != nil {
		sendError(w, err)
		return
	}

	t := time.Now()
	err := SetProxy(&Options{Proxy: req.Server, Bypass: req.Bypass, Device: req.Device, OnlyActiveDevice: req.OnlyActiveDevice})
	log.Println("SetProxy took:", time.Since(t), "\nserver:", req.Server, "\nbypass:", req.Bypass)
	if err != nil {
		sendError(w, err)
		return
	}
	render.NoContent(w, r)
}

func disable(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := decodeRequest(r, &req); err != nil {
		sendError(w, err)
		return
	}

	t := time.Now()
	err := DisableProxy(&Options{Device: req.Device, OnlyActiveDevice: req.OnlyActiveDevice})
	log.Println("DisableProxy took:", time.Since(t))
	if err != nil {
		sendError(w, err)
		return
	}
	render.NoContent(w, r)
}

func decodeRequest(r *http.Request, v any) error {
	if r.ContentLength > 0 {
		return render.DecodeJSON(r.Body, v)
	}
	return nil
}

func sendJSON(w http.ResponseWriter, status string, message string) {
	w.Header().Set("Content-Type", "application/json")
	resp := Response{
		Status:  status,
		Message: message,
	}
	json.NewEncoder(w).Encode(resp)
}

func sendError(w http.ResponseWriter, err error) {
	sendJSON(w, "error", err.Error())
}
