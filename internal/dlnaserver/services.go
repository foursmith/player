package dlnaserver

import "strings"

type argument struct{ name, direction, variable string }
type variable struct{ name, kind, event string }

var serviceActions = map[string]map[string][]argument{
	contentType: {
		"GetSearchCapabilities": {{"SearchCaps", "out", "SearchCapabilities"}},
		"GetSortCapabilities":   {{"SortCaps", "out", "SortCapabilities"}},
		"GetSystemUpdateID":     {{"Id", "out", "SystemUpdateID"}},
		"Browse": {
			{"ObjectID", "in", "A_ARG_TYPE_ObjectID"}, {"BrowseFlag", "in", "A_ARG_TYPE_BrowseFlag"},
			{"Filter", "in", "A_ARG_TYPE_Filter"}, {"StartingIndex", "in", "A_ARG_TYPE_Index"},
			{"RequestedCount", "in", "A_ARG_TYPE_Count"}, {"SortCriteria", "in", "A_ARG_TYPE_SortCriteria"},
			{"Result", "out", "A_ARG_TYPE_Result"}, {"NumberReturned", "out", "A_ARG_TYPE_Count"},
			{"TotalMatches", "out", "A_ARG_TYPE_Count"}, {"UpdateID", "out", "A_ARG_TYPE_UpdateID"},
		},
	},
	connectionType: {
		"GetProtocolInfo":         {{"Source", "out", "SourceProtocolInfo"}, {"Sink", "out", "SinkProtocolInfo"}},
		"GetCurrentConnectionIDs": {{"ConnectionIDs", "out", "CurrentConnectionIDs"}},
		"GetCurrentConnectionInfo": {
			{"ConnectionID", "in", "A_ARG_TYPE_ConnectionID"}, {"RcsID", "out", "A_ARG_TYPE_RcsID"},
			{"AVTransportID", "out", "A_ARG_TYPE_AVTransportID"}, {"ProtocolInfo", "out", "A_ARG_TYPE_ProtocolInfo"},
			{"PeerConnectionManager", "out", "A_ARG_TYPE_ConnectionManager"}, {"PeerConnectionID", "out", "A_ARG_TYPE_ConnectionID"},
			{"Direction", "out", "A_ARG_TYPE_Direction"}, {"Status", "out", "A_ARG_TYPE_ConnectionStatus"},
		},
	},
}

var allowedValues = map[string][]string{
	"A_ARG_TYPE_BrowseFlag":       {"BrowseMetadata", "BrowseDirectChildren"},
	"A_ARG_TYPE_Direction":        {"Input", "Output"},
	"A_ARG_TYPE_ConnectionStatus": {"OK", "ContentFormatMismatch", "InsufficientBandwidth", "UnreliableChannel", "Unknown"},
}

func serviceDescription(content bool) string {
	service := connectionType
	variables := []variable{
		{"SourceProtocolInfo", "string", "yes"}, {"SinkProtocolInfo", "string", "yes"}, {"CurrentConnectionIDs", "string", "yes"},
		{"A_ARG_TYPE_ConnectionID", "i4", "no"}, {"A_ARG_TYPE_RcsID", "i4", "no"}, {"A_ARG_TYPE_AVTransportID", "i4", "no"},
		{"A_ARG_TYPE_ProtocolInfo", "string", "no"}, {"A_ARG_TYPE_ConnectionManager", "string", "no"},
		{"A_ARG_TYPE_Direction", "string", "no"}, {"A_ARG_TYPE_ConnectionStatus", "string", "no"},
	}
	if content {
		service = contentType
		variables = []variable{
			{"SearchCapabilities", "string", "no"}, {"SortCapabilities", "string", "no"}, {"SystemUpdateID", "ui4", "yes"},
			{"A_ARG_TYPE_ObjectID", "string", "no"}, {"A_ARG_TYPE_BrowseFlag", "string", "no"},
			{"A_ARG_TYPE_Filter", "string", "no"}, {"A_ARG_TYPE_Index", "ui4", "no"}, {"A_ARG_TYPE_Count", "ui4", "no"},
			{"A_ARG_TYPE_SortCriteria", "string", "no"}, {"A_ARG_TYPE_Result", "string", "no"}, {"A_ARG_TYPE_UpdateID", "ui4", "no"},
		}
	}
	var out strings.Builder
	out.WriteString(`<scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><actionList>`)
	for name, args := range serviceActions[service] {
		out.WriteString(`<action>` + tag("name", name) + `<argumentList>`)
		for _, arg := range args {
			out.WriteString(`<argument>` + tag("name", arg.name) + tag("direction", arg.direction) + tag("relatedStateVariable", arg.variable) + `</argument>`)
		}
		out.WriteString(`</argumentList></action>`)
	}
	out.WriteString(`</actionList><serviceStateTable>`)
	for _, v := range variables {
		out.WriteString(`<stateVariable sendEvents="` + v.event + `">` + tag("name", v.name) + tag("dataType", v.kind))
		allowed := allowedValues[v.name]
		if len(allowed) > 0 {
			out.WriteString(`<allowedValueList>`)
			for _, value := range allowed {
				out.WriteString(tag("allowedValue", value))
			}
			out.WriteString(`</allowedValueList>`)
		}
		out.WriteString(`</stateVariable>`)
	}
	out.WriteString(`</serviceStateTable></scpd>`)
	return out.String()
}
