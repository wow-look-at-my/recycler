package inspect

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

//go:embed ui/index.html ui/inspector.css ui/inspector.js
var uiFS embed.FS // Named, not globbed: ui/src holds the TypeScript this compiled from.

// Handler answers inspection requests. A Server answers them from a program in this process; `tml serve` answers them
type Handler interface {
	Handle(Request) Response
}

// HTTPHandler serves the browser inspector against any Handler: the page, its assets, the same RPC the socket speaks,
func HTTPHandler(h Handler) http.Handler {
	assets, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// The assets are embedded at build time, so a failure here is a build that shipped without them rather than anything
		panic("inspect: embedded ui assets are unreadable: " + err.Error())
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) { serveRPC(h, w, r) })
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) { serveEvents(h, w, r) })
	return mux
}

func (s *Server) httpHandler() http.Handler { return HTTPHandler(s) }

func serveRPC(h Handler, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, Response{Error: "rpc takes POST"})
		return
	}
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: fmt.Sprintf("request is not JSON: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, h.Handle(req))
}

// serveEvents pushes a single message per painted frame, so the preview follows the program without the page polling it. It
func serveEvents(h Handler, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, Response{Error: "this server cannot stream"})
		return
	}
	head := w.Header()
	head.Set("content-type", "text/event-stream")
	head.Set("cache-control", "no-cache")
	head.Set("connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var last uint64
	for {
		if r.Context().Err() != nil {
			return
		}
		res := h.Handle(Request{Op: "frame", Since: last, ANSI: true})
		if res.Error != "" || res.Frame == nil {
			fmt.Fprintf(w, ": %s\n\n", strings.ReplaceAll(res.Error, "\n", " "))
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		last = res.Frame.Seq
		payload, err := json.Marshal(res.Frame)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: frame\ndata: %s\n\n", payload)
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	encoded, err := json.Marshal(body)
	if err != nil {
		return
	}
	_, _ = w.Write(encoded)
}

func stripANSI(s string) string { return ansi.Strip(s) }

func join(items []string) string { return strings.Join(items, ", ") }
