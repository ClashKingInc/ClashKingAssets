package sc3d

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"sc2fla/internal/sc"
)

//go:embed static/*
var staticFiles embed.FS

type configResponse struct {
	Fingerprint string `json:"fingerprint"`
	BaseURL     string `json:"base_url"`
}

func ServeViewer(addr, fingerprint string) error {
	if fingerprint == "" {
		fingerprint = os.Getenv("FINGERPRINT")
	}
	if fingerprint == "" {
		return fmt.Errorf("missing fingerprint; pass --fingerprint or set FINGERPRINT")
	}
	handler, err := newViewerHandler(fingerprint, &http.Client{Timeout: 45 * time.Second})
	if err != nil {
		return err
	}
	log.Printf("SC3D viewer listening on http://%s (fingerprint %s)", addr, fingerprint)
	return http.ListenAndServe(addr, handler)
}

func newViewerHandler(fingerprint string, client *http.Client) (http.Handler, error) {
	if fingerprint == "" {
		return nil, fmt.Errorf("missing fingerprint")
	}
	if client == nil {
		return nil, fmt.Errorf("missing HTTP client")
	}
	baseURL := "https://game-assets.clashofclans.com/" + fingerprint
	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticRoot)))
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(configResponse{
			Fingerprint: fingerprint,
			BaseURL:     baseURL,
		})
	})
	mux.HandleFunc("/remote/", func(w http.ResponseWriter, r *http.Request) {
		remotePath := strings.TrimPrefix(r.URL.Path, "/remote/")
		if remotePath == "" || strings.Contains(remotePath, "..") || strings.HasPrefix(remotePath, "/") {
			http.Error(w, "invalid remote path", http.StatusBadRequest)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, baseURL+"/"+remotePath, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for _, key := range []string{"Content-Type", "Content-Length", "Last-Modified", "ETag"} {
			if value := resp.Header.Get(key); value != "" {
				w.Header().Set(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	mux.HandleFunc("/texture/", func(w http.ResponseWriter, r *http.Request) {
		remotePath := strings.TrimPrefix(r.URL.Path, "/texture/")
		if remotePath == "" || strings.Contains(remotePath, "..") || strings.HasPrefix(remotePath, "/") {
			http.Error(w, "invalid texture path", http.StatusBadRequest)
			return
		}
		if !strings.HasSuffix(strings.ToLower(remotePath), ".sctx") {
			http.Error(w, "texture path must be .sctx", http.StatusBadRequest)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, baseURL+"/"+remotePath, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			http.Error(w, resp.Status, resp.StatusCode)
			return
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		img, err := sc.DecodeTextureBytes(remotePath, raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Content-Length", fmt.Sprint(encoded.Len()))
		_, _ = w.Write(encoded.Bytes())
	})

	return mux, nil
}
