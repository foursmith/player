package fsvr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticatedLazyLibrary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			fmt.Fprint(w, `{"name":"fsvr"}`)
		case "/api/v1/auth":
			if r.Method == "POST" {
				http.SetCookie(w, &http.Cookie{Name: "fsvr", Value: "session", Path: "/", HttpOnly: true})
				fmt.Fprint(w, `{}`)
			} else {
				_, err := r.Cookie("fsvr")
				fmt.Fprintf(w, `{"authenticated":%t}`, err == nil)
			}
		case "/api/v1/sources":
			if _, err := r.Cookie("fsvr"); err != nil {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `[{"id":"local","name":"Videos","kind":"local"}]`)
		case "/api/v1/sources/local/entries":
			fmt.Fprint(w, `[{"id":"sub","name":"Folder","kind":"folder"},{"id":"a/b","name":"Movie.mp4","kind":"video"},{"id":"subtitle","name":"Movie.zh.srt","kind":"subtitle"},{"id":"bad","name":"._bad.mp4","kind":"video"}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if auth, err := c.Connect(ctx); err != nil || auth {
		t.Fatal(auth, err)
	}
	if err = c.Authenticate(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	folders, err := c.Sources(ctx)
	if err != nil || len(folders) != 1 {
		t.Fatal(folders, err)
	}
	videos, children, err := c.Entries(ctx, folders[0])
	if err != nil || len(videos) != 1 || len(children) != 1 {
		t.Fatal(videos, children, err)
	}
	v := videos[0]
	if !strings.HasSuffix(v.Path, "a%2Fb") || !strings.HasSuffix(v.Subtitle, "/subtitle") || !strings.Contains(v.Cookies, "domain=127.0.0.1;") {
		t.Fatal(v)
	}
	if c.Cookies("https://example.org/video") != "" {
		t.Fatal("cookies escaped origin")
	}
}
