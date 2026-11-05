package dlnaserver

import (
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type subscription struct {
	id, callback, path string
	expires            time.Time
	sequence           uint32
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "SUBSCRIBE" && r.Method != "UNSUBSCRIBE" {
		w.Header().Set("Allow", "SUBSCRIBE, UNSUBSCRIBE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, sub := range s.subs {
		if !sub.expires.After(now) {
			delete(s.subs, id)
		}
	}
	id := r.Header.Get("SID")
	sub := s.subs[id]
	if id != "" && (sub == nil || sub.path != r.URL.Path) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if r.Method == "UNSUBSCRIBE" {
		if sub == nil || r.Header.Get("CALLBACK") != "" || r.Header.Get("NT") != "" {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		delete(s.subs, id)
		w.WriteHeader(http.StatusOK)
		return
	}
	if sub == nil {
		if r.Header.Get("NT") != "upnp:event" || len(s.subs) >= 128 {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		callback := strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("CALLBACK"), "<"), ">")
		u, err := url.Parse(callback)
		remote, _, _ := net.SplitHostPort(r.RemoteAddr)
		// Notify only the subscribing peer's literal IP; no DNS, redirects,
		// proxies or arbitrary callback hosts can turn subscriptions into SSRF.
		if err != nil || u.Scheme != "http" || u.User != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).Equal(net.ParseIP(remote)) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		id = "uuid:" + NewUUID()
		sub = &subscription{id: id, callback: callback, path: r.URL.Path}
		s.subs[id] = sub
	} else if r.Header.Get("CALLBACK") != "" || r.Header.Get("NT") != "" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	seconds := 600
	if value, err := strconv.Atoi(strings.TrimPrefix(r.Header.Get("TIMEOUT"), "Second-")); err == nil {
		seconds = max(30, min(1800, value))
	}
	sub.expires = now.Add(time.Duration(seconds) * time.Second)
	w.Header().Set("SID", id)
	w.Header().Set("TIMEOUT", fmt.Sprintf("Second-%d", seconds))
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.queueEvents()
}

func (s *Server) queueEvents() {
	select {
	case s.events <- struct{}{}:
	default:
	}
}

func (s *Server) eventLoop() {
	transport := &http.Transport{Proxy: nil, MaxConnsPerHost: 2, IdleConnTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var lastUpdate uint32
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.events:
		}
		s.mu.Lock()
		updateID := s.updateID
		properties := map[string]string{
			"/content/event":    `<e:property>` + tag("SystemUpdateID", strconv.FormatUint(uint64(updateID), 10)) + `</e:property>`,
			"/connection/event": `<e:property>` + tag("SourceProtocolInfo", protocolInfo(s.objects)) + `</e:property><e:property>` + tag("SinkProtocolInfo", "") + `</e:property><e:property>` + tag("CurrentConnectionIDs", "0") + `</e:property>`,
		}
		var pending []subscription
		for id, sub := range s.subs {
			if !sub.expires.After(time.Now()) {
				delete(s.subs, id)
				continue
			}
			if sub.sequence == 0 || updateID != lastUpdate {
				pending = append(pending, *sub)
				sub.sequence++
				if sub.sequence == 0 {
					sub.sequence = 1
				}
			}
		}
		s.mu.Unlock()
		lastUpdate = updateID
		for _, sub := range pending {
			if s.ctx.Err() != nil {
				return
			}
			s.notify(client, sub, properties[sub.path])
		}
	}
}

func (s *Server) notify(client *http.Client, sub subscription, properties string) {
	body := xml.Header + `<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">` + properties + `</e:propertyset>`
	request, err := http.NewRequestWithContext(s.ctx, "NOTIFY", sub.callback, strings.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	request.Header.Set("NT", "upnp:event")
	request.Header.Set("NTS", "upnp:propchange")
	request.Header.Set("SID", sub.id)
	request.Header.Set("SEQ", strconv.FormatUint(uint64(sub.sequence), 10))
	response, err := client.Do(request)
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
	}
}
