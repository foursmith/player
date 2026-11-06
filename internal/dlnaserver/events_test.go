package dlnaserver

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foursmith/player/library"
)

type event struct {
	headers http.Header
	body    string
}

func TestEventSubscriptionsInitialChangeRenewalAndUnsubscribe(t *testing.T) {
	s, base := fixture(t, nil)
	events := make(chan event, 10)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		events <- event{r.Header.Clone(), string(body)}
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()
	request := func(method, path, sid, address string, status int) http.Header {
		t.Helper()
		req, err := http.NewRequest(method, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sid != "" {
			req.Header.Set("SID", sid)
		}
		if address != "" {
			req.Header.Set("CALLBACK", "<"+address+">")
			req.Header.Set("NT", "upnp:event")
		}
		req.Header.Set("TIMEOUT", "Second-300")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("%s subscription status=%d", method, response.StatusCode)
		}
		return response.Header
	}
	receive := func() event {
		t.Helper()
		select {
		case value := <-events:
			var body struct{ XMLName xml.Name }
			if err := xml.Unmarshal([]byte(value.body), &body); err != nil || body.XMLName.Local != "propertyset" {
				t.Fatalf("invalid event XML: %v %s", err, value.body)
			}
			return value
		case <-time.After(3 * time.Second):
			t.Fatal("subscription event never arrived")
		}
		return event{}
	}
	headers := request("SUBSCRIBE", "/content/event", "", callback.URL, 200)
	sid := headers.Get("SID")
	if !strings.HasPrefix(sid, "uuid:") || headers.Get("TIMEOUT") != "Second-300" {
		t.Fatal("subscription response lacks identity or expiry", headers)
	}
	initial := receive()
	if initial.headers.Get("SEQ") != "0" || initial.headers.Get("SID") != sid || !strings.Contains(initial.body, "<SystemUpdateID>0</SystemUpdateID>") {
		t.Fatalf("initial event incorrect: %+v", initial)
	}
	request("SUBSCRIBE", "/content/event", sid, "", 200)
	s.SetSources([]library.Source{{ID: "empty", Name: "Empty collection", ShareDLNA: true, Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{})}})
	changed := receive()
	if changed.headers.Get("SEQ") != "1" || !strings.Contains(changed.body, "<SystemUpdateID>1</SystemUpdateID>") {
		t.Fatalf("change event incorrect: %+v", changed)
	}
	request("UNSUBSCRIBE", "/content/event", sid, "", 200)
	request("SUBSCRIBE", "/content/event", sid, "", 412)
	request("SUBSCRIBE", "/content/event", "", "http://192.0.2.1/private", 412)
	request("SUBSCRIBE", "/content/event", "", "http://localhost/private", 412)
	request("SUBSCRIBE", "/connection/event", "", callback.URL, 200)
	connection := receive()
	if connection.headers.Get("SEQ") != "0" || !strings.Contains(connection.body, "SourceProtocolInfo") || !strings.Contains(connection.body, "<CurrentConnectionIDs>0</CurrentConnectionIDs>") {
		t.Fatalf("ConnectionManager event incorrect: %+v", connection)
	}
}
