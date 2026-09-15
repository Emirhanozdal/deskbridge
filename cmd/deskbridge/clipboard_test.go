package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalizedDownloads(t *testing.T) {
	if got := xdgDownloadDir(`XDG_DOWNLOAD_DIR="$HOME/Indirilenler"`, "/home/user"); got != "/home/user/Indirilenler" {
		t.Fatal(got)
	}
	if got := xdgDownloadDir(`XDG_DOWNLOAD_DIR="$(command)"`, "/home/user"); got != "" {
		t.Fatal("accepted shell expression")
	}
}

func TestUploadDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	for _, text := range []string{"one", "two"} {
		if _, err := saveUniqueUpload(strings.NewReader(text), dir, "a.txt"); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "one" {
		t.Fatal("original overwritten")
	}
	data, _ = os.ReadFile(filepath.Join(dir, "a (1).txt"))
	if string(data) != "two" {
		t.Fatal("duplicate missing")
	}
}

func TestClipboardRequestValidation(t *testing.T) {
	dir, queue := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ok"), 0600)
	os.Symlink(filepath.Join(dir, "a.txt"), filepath.Join(dir, "alias.txt"))
	server := newReceiverServer(receiverOptions{dir: dir, token: "secret", clipboardQueue: queue})
	for _, tt := range []struct {
		body, token string
		status      int
	}{
		{`{"files":["a.txt"]}`, "", 401},
		{`{"files":["../a.txt"]}`, "secret", 400},
		{`{"files":["alias.txt"]}`, "secret", 400},
		{`{"files":["a.txt"]}`, "secret", 202},
	} {
		req := httptest.NewRequest(http.MethodPost, "/clipboard", strings.NewReader(tt.body))
		req.Header.Set("X-DeskBridge-Token", tt.token)
		rr := httptest.NewRecorder()
		server.Handler.ServeHTTP(rr, req)
		if rr.Code != tt.status {
			t.Fatalf("got %d want %d", rr.Code, tt.status)
		}
	}
	entries, _ := os.ReadDir(queue)
	if len(entries) != 1 {
		t.Fatalf("expected one event got %d", len(entries))
	}
}
