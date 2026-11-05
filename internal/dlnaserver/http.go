package dlnaserver

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

const contentFeatures = "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"

func escaped(value string) string {
	var out bytes.Buffer
	xml.EscapeText(&out, []byte(value))
	return out.String()
}

func tag(name, value string) string { return "<" + name + ">" + escaped(value) + "</" + name + ">" }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", serverHeader)
	switch {
	case r.URL.Path == "/device.xml":
		if allowRead(w, r) {
			writeXML(w, http.StatusOK, s.description())
		}
	case r.URL.Path == "/content/scpd.xml" || r.URL.Path == "/connection/scpd.xml":
		if allowRead(w, r) {
			writeXML(w, http.StatusOK, serviceDescription(r.URL.Path == "/content/scpd.xml"))
		}
	case r.URL.Path == "/content/control":
		s.control(w, r, contentType)
	case r.URL.Path == "/connection/control":
		s.control(w, r, connectionType)
	case r.URL.Path == "/content/event" || r.URL.Path == "/connection/event":
		s.subscribe(w, r)
	case strings.HasPrefix(r.URL.Path, "/media/") || strings.HasPrefix(r.URL.Path, "/subtitle/"):
		s.serveFile(w, r)
	default:
		http.NotFound(w, r)
	}
}

func allowRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(status)
	io.WriteString(w, xml.Header+body)
}

func (s *Server) description() string {
	return `<root xmlns="urn:schemas-upnp-org:device-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><device>` +
		tag("deviceType", mediaServerType) + tag("friendlyName", s.name) + tag("manufacturer", "Foursmith") + tag("modelName", "Foursmith Player") + tag("UDN", s.udn) +
		`<dlna:X_DLNADOC xmlns:dlna="urn:schemas-dlna-org:device-1-0">DMS-1.50</dlna:X_DLNADOC><serviceList>` +
		`<service>` + tag("serviceType", contentType) + `<serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId><SCPDURL>/content/scpd.xml</SCPDURL><controlURL>/content/control</controlURL><eventSubURL>/content/event</eventSubURL></service>` +
		`<service>` + tag("serviceType", connectionType) + `<serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId><SCPDURL>/connection/scpd.xml</SCPDURL><controlURL>/connection/control</controlURL><eventSubURL>/connection/event</eventSubURL></service></serviceList></device></root>`
}

func requestBase(r *http.Request) string {
	// Use the accepted socket's address, never a caller-controlled Host header.
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		return "http://" + addr.String()
	}
	return ""
}

func resourceURL(base, kind string, item object) string {
	name := item.Name
	if kind == "subtitle" {
		name = filepath.Base(item.Subtitle)
	}
	return base + "/" + kind + "/" + item.ID + "/" + url.PathEscape(name)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	item, found := s.objects[parts[1]]
	path, name, mimeType := item.File, item.Name, item.MIME
	if parts[0] == "subtitle" {
		path, name, mimeType = item.Subtitle, filepath.Base(item.Subtitle), mediaType(item.Subtitle)
	}
	if !found || item.Container || path == "" || parts[2] != name {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if s.transfers == nil {
		s.transfers = map[*http.Request]transfer{}
	}
	s.transfers[r] = transfer{source: item.Source, stop: func() {
		http.NewResponseController(w).SetWriteDeadline(time.Now())
	}}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.transfers, r)
		s.mu.Unlock()
	}()
	// Paths are resolved when catalogued. Replacing one with a symlink must
	// never grant access to a file outside the shared catalog.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	mode := r.Header.Get("transferMode.dlna.org")
	if mode == "" {
		mode = "Streaming"
	}
	if mode != "Streaming" && mode != "Interactive" && mode != "Background" {
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	w.Header().Set("transferMode.dlna.org", mode)
	if parts[0] == "media" {
		w.Header().Set("contentFeatures.dlna.org", contentFeatures)
		if item.Subtitle != "" {
			w.Header().Set("CaptionInfo.sec", resourceURL(requestBase(r), "subtitle", item))
		}
	}
	// ServeContent handles byte ranges, HEAD, conditional requests and seeking
	// without buffering whole videos or modifying the local player.
	http.ServeContent(w, r, name, actual.ModTime(), file)
}

type soapAction struct {
	XMLName xml.Name
	Args    []struct {
		XMLName xml.Name
		Value   string `xml:",chardata"`
	} `xml:",any"`
}

func (s *Server) control(w http.ResponseWriter, r *http.Request, service string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var envelope struct {
		Body struct {
			Actions []soapAction `xml:",any"`
		} `xml:"Body"`
	}
	decoder := xml.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&envelope); err != nil || len(envelope.Body.Actions) != 1 {
		s.fault(w, 402, "Invalid Args")
		return
	}
	action := envelope.Body.Actions[0]
	if action.XMLName.Space != service || strings.Trim(r.Header.Get("SOAPAction"), `"`) != service+"#"+action.XMLName.Local {
		s.fault(w, 401, "Invalid Action")
		return
	}
	args := map[string]string{}
	for _, arg := range action.Args {
		if _, duplicate := args[arg.XMLName.Local]; duplicate {
			s.fault(w, 402, "Invalid Args")
			return
		}
		args[arg.XMLName.Local] = arg.Value
	}
	definition, found := serviceActions[service][action.XMLName.Local]
	if !found {
		s.fault(w, 401, "Invalid Action")
		return
	}
	inputs := 0
	for _, arg := range definition {
		if arg.direction == "in" {
			inputs++
			if _, found := args[arg.name]; !found {
				s.fault(w, 402, "Invalid Args")
				return
			}
		}
	}
	if inputs != len(args) {
		s.fault(w, 402, "Invalid Args")
		return
	}
	var response string
	s.mu.RLock()
	objects, updateID := s.objects, s.updateID
	switch action.XMLName.Local {
	case "Browse":
		var code int
		response, code = browse(objects, updateID, args, requestBase(r), s.prioritizeMetadata)
		if code != 0 {
			s.mu.RUnlock()
			s.fault(w, code, map[int]string{402: "Invalid Args", 701: "No Such Object", 709: "Unsupported or invalid sort criteria", 710: "No such container"}[code])
			return
		}
	case "GetSearchCapabilities":
		response = tag("SearchCaps", "")
	case "GetSortCapabilities":
		response = tag("SortCaps", "dc:title")
	case "GetSystemUpdateID":
		response = tag("Id", strconv.FormatUint(uint64(updateID), 10))
	case "GetProtocolInfo":
		response = tag("Source", protocolInfo(objects)) + tag("Sink", "")
	case "GetCurrentConnectionIDs":
		response = tag("ConnectionIDs", "0")
	case "GetCurrentConnectionInfo":
		if args["ConnectionID"] != "0" {
			s.mu.RUnlock()
			s.fault(w, 706, "Invalid connection reference")
			return
		}
		response = `<RcsID>-1</RcsID><AVTransportID>-1</AVTransportID><ProtocolInfo></ProtocolInfo><PeerConnectionManager></PeerConnectionManager><PeerConnectionID>-1</PeerConnectionID><Direction>Output</Direction><Status>OK</Status>`
	}
	s.mu.RUnlock()
	writeXML(w, http.StatusOK, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:`+action.XMLName.Local+`Response xmlns:u="`+service+`">`+response+`</u:`+action.XMLName.Local+`Response></s:Body></s:Envelope>`)
}

func (s *Server) fault(w http.ResponseWriter, code int, description string) {
	writeXML(w, http.StatusInternalServerError, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`+tag("errorCode", strconv.Itoa(code))+tag("errorDescription", description)+`</UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
}

func browse(objects catalog, updateID uint32, args map[string]string, base string, prioritize func([]object)) (string, int) {
	item, found := objects[args["ObjectID"]]
	if !found {
		return "", 701
	}
	start, e1 := strconv.ParseUint(args["StartingIndex"], 10, 32)
	count, e2 := strconv.ParseUint(args["RequestedCount"], 10, 32)
	if e1 != nil || e2 != nil {
		return "", 402
	}
	criteria := args["SortCriteria"]
	if criteria != "" && criteria != "+dc:title" && criteria != "-dc:title" {
		return "", 709
	}
	var items []object
	total := 1
	switch args["BrowseFlag"] {
	case "BrowseMetadata":
		if start != 0 {
			return "", 402
		}
		items = []object{item}
	case "BrowseDirectChildren":
		if !item.Container {
			return "", 710
		}
		total = len(item.Children)
		from := min(start, uint64(total))
		to := uint64(total)
		if count > 0 {
			to = min(to, from+count)
		}
		children := item.Children
		if criteria == "" {
			// The catalog already stores natural folder-first order. Page IDs
			// before materializing objects, so one page costs only its own rows.
			children = children[from:to]
		}
		items = make([]object, 0, len(children))
		for _, id := range children {
			items = append(items, objects[id])
		}
		if criteria != "" {
			comparator := collate.New(language.Und, collate.Numeric, collate.IgnoreCase)
			slices.SortStableFunc(items, func(a, b object) int {
				order := comparator.CompareString(a.Name, b.Name)
				if criteria[0] == '-' {
					return -order
				}
				return order
			})
			items = items[from:to]
		}
	default:
		return "", 402
	}
	prioritize(items)
	var result strings.Builder
	result.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:sec="http://www.sec.co.kr/">`)
	for _, item := range items {
		result.WriteString(item.didl(base, args["Filter"]))
	}
	result.WriteString(`</DIDL-Lite>`)
	return tag("Result", result.String()) + tag("NumberReturned", strconv.Itoa(len(items))) + tag("TotalMatches", strconv.Itoa(total)) + tag("UpdateID", strconv.FormatUint(uint64(updateID), 10)), 0
}

func (o object) didl(base, filter string) string {
	requested := func(property string) bool {
		for _, value := range strings.Split(filter, ",") {
			value = strings.TrimSpace(value)
			if value == "*" || value == property || strings.Contains(property, "@") && value == strings.SplitN(property, "@", 2)[0]+"@*" {
				return true
			}
		}
		return false
	}
	attributes := fmt.Sprintf(` id="%s" parentID="%s" restricted="1"`, escaped(o.ID), escaped(o.Parent))
	if o.Container {
		if requested("@childCount") || requested("container@childCount") {
			attributes += fmt.Sprintf(` childCount="%d"`, len(o.Children))
		}
		return `<container` + attributes + `>` + tag("dc:title", o.Name) + `<upnp:class>object.container.storageFolder</upnp:class></container>`
	}
	body := `<item` + attributes + `>` + tag("dc:title", o.Name) + `<upnp:class>object.item.videoItem</upnp:class>`
	if requested("dc:date") {
		body += tag("dc:date", time.UnixMilli(o.Modified).UTC().Format(time.RFC3339))
	}
	if o.Subtitle != "" && requested("sec:CaptionInfoEx") {
		body += `<sec:CaptionInfoEx sec:type="` + escaped(strings.TrimPrefix(filepath.Ext(o.Subtitle), ".")) + `">` + escaped(resourceURL(base, "subtitle", o)) + `</sec:CaptionInfoEx>`
	}
	resource := false
	for _, value := range strings.Split(filter, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || value == "res" || strings.HasPrefix(value, "res@") {
			resource = true
		}
	}
	if resource {
		attributes := ` protocolInfo="` + escaped("http-get:*:"+o.MIME+":"+contentFeatures) + `"`
		if requested("res@size") {
			attributes += fmt.Sprintf(` size="%d"`, o.Size)
		}
		if requested("res@duration") && o.Info.Duration > 0 {
			milliseconds := int64(math.Round(min(o.Info.Duration, 1e9) * 1000))
			attributes += fmt.Sprintf(` duration="%d:%02d:%02d.%03d"`, milliseconds/3600000, milliseconds/60000%60, milliseconds/1000%60, milliseconds%1000)
		}
		if requested("res@resolution") && o.Info.Width > 0 && o.Info.Height > 0 {
			attributes += fmt.Sprintf(` resolution="%dx%d"`, o.Info.Width, o.Info.Height)
		}
		if requested("res@bitrate") {
			bitrate := o.Info.BitRate / 8
			if bitrate <= 0 && o.Info.Duration > 0 {
				bitrate = int64(float64(o.Size) / o.Info.Duration)
			}
			if bitrate > 0 {
				attributes += fmt.Sprintf(` bitrate="%d"`, bitrate)
			}
		}
		body += `<res` + attributes + `>` + escaped(resourceURL(base, "media", o)) + `</res>`
	}
	return body + `</item>`
}

func protocolInfo(objects catalog) string {
	seen := map[string]bool{}
	var protocols []string
	for _, item := range objects {
		if !item.Container && !seen[item.MIME] {
			seen[item.MIME] = true
			protocols = append(protocols, "http-get:*:"+item.MIME+":"+contentFeatures)
		}
	}
	slices.Sort(protocols)
	return strings.Join(protocols, ",")
}
