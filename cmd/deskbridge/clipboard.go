package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func saveUniqueUpload(reader io.Reader, dir, name string) (string, error) {
	temp, err := os.CreateTemp(dir, ".deskbridge-upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	if _, err := io.Copy(temp, reader); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		// Linking a completed temporary file is atomic and never follows or overwrites an existing target.
		if err := os.Link(temp.Name(), filepath.Join(dir, candidate)); err == nil {
			return candidate, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("too many files named %s", name)
}

func registerClipboardReceiver(mux *http.ServeMux, opts receiverOptions) {
	mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, opts.token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"clipboardFiles": opts.clipboardQueue != "", "version": "0.3.1"})
	})
	mux.HandleFunc("/clipboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		if !authorized(r, opts.token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		if opts.clipboardQueue == "" {
			http.Error(w, "desktop clipboard unavailable", 409)
			return
		}
		var payload struct {
			Files []string `json:"files"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&payload); err != nil || len(payload.Files) == 0 || len(payload.Files) > 100 {
			http.Error(w, "invalid file list", 400)
			return
		}
		paths := make([]string, 0, len(payload.Files))
		for _, name := range payload.Files {
			if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
				http.Error(w, "invalid file name", 400)
				return
			}
			path, err := filepath.Abs(filepath.Join(opts.dir, name))
			if err != nil {
				http.Error(w, "invalid path", 400)
				return
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				http.Error(w, "file unavailable", 400)
				return
			}
			paths = append(paths, path)
		}
		if err := os.MkdirAll(opts.clipboardQueue, 0700); err != nil {
			http.Error(w, "clipboard unavailable", 500)
			return
		}
		entries, err := os.ReadDir(opts.clipboardQueue)
		if err != nil || len(entries) >= 100 {
			http.Error(w, "clipboard inbox full; open DeskBridge", 429)
			return
		}
		file, err := os.CreateTemp(opts.clipboardQueue, ".pending-*")
		if err != nil {
			http.Error(w, "clipboard unavailable", 500)
			return
		}
		defer os.Remove(file.Name())
		err = json.NewEncoder(file).Encode(map[string]any{"files": paths, "receivedAt": time.Now().UTC()})
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			http.Error(w, "clipboard unavailable", 500)
			return
		}
		if err := os.Rename(file.Name(), file.Name()+".json"); err != nil {
			http.Error(w, "clipboard unavailable", 500)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
