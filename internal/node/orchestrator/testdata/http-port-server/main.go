// Isolated CHV test fixture. It only listens on Guest loopback.
package main

import (
	"fmt"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/hmr", websocket.Handler(func(c *websocket.Conn) {
		defer c.Close()
		for {
			var text string
			if websocket.Message.Receive(c, &text) != nil {
				return
			}
			if websocket.Message.Send(c, "hmr:"+text) != nil {
				return
			}
		}
	}))
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			fmt.Fprint(w, "data: tick\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "guest:%s:%s:%s", r.Method, r.URL.RequestURI(), b)
	})
	for _, port := range []int{3000, 5173, 8000} {
		go func() { panic(http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux)) }()
	}
	select {}
}
