package mcp

// action="i18n", the translation domain routed as one op-keyed surface.
//
// The individual i18n tools (GetObjectTextsInLanguage, GetDataElementLabels,
// GetMessageClassTexts, GetTextPool, CompareLanguages, WriteMessageClassTexts,
// WriteDataElementLabels, and the new TextsGet/TextsSet) are also registered
// as standalone tools in expert/focused mode. This router makes the whole
// domain reachable from the hyperfocused single-tool (SAP) surface, which is
// the mode that ships — before it, an agent in hyperfocused mode could not
// read or write a text pool at all.

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// i18nTypes are the translation operations, addressed as
// SAP(action="i18n", params={"op": "..."}).
func (s *Server) i18nTypes() map[string]server.ToolHandlerFunc {
	return map[string]server.ToolHandlerFunc{
		"texts":               s.handleGetObjectTextsInLanguage,
		"data_element_labels": s.handleGetDataElementLabels,
		"message_class_texts": s.handleGetMessageClassTexts,
		"text_pool":           s.handleTextsGet,
		"texts_get":           s.handleTextsGet,
		"texts_set":           s.handleTextsSet,
		"write_text_pool":     s.handleTextsSet,
		"compare_languages":   s.handleCompareObjectLanguages,
		"write_labels":        s.handleWriteDataElementLabels,
		"write_message_texts": s.handleWriteMessageClassTextsAutoLock,
	}
}

// i18nOps lists the operations, sorted, for the message a wrong one earns.
func i18nOps() []string {
	return []string{
		"compare_languages", "data_element_labels", "message_class_texts",
		"text_pool", "texts", "texts_get", "texts_set", "write_labels",
		"write_message_texts", "write_text_pool",
	}
}

// routeI18nAction routes action="i18n".
func (s *Server) routeI18nAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "i18n" {
		return nil, false, nil
	}
	op := getStringParam(params, "op")
	// A target such as "PROG ZDEMO" / "CLAS ZCL_DEMO" / "DTEL ZED_X" fills
	// the object: object_type/object_name for the text-pool handlers, and
	// name for the label / message-class handlers.
	if objectName != "" {
		if getStringParam(params, "object_name") == "" {
			params["object_type"], params["object_name"] = objectType, objectName
		}
		if getStringParam(params, "name") == "" {
			params["name"] = objectName
		}
	}
	handler, known := s.i18nTypes()[op]
	if !known {
		// The action is recognised, so it owns the answer — falling through
		// would tell a caller action="i18n" does not exist.
		return newToolResultError(fmt.Sprintf(
			"SAP(action=\"i18n\") needs params.op — one of %v.\n"+
				"  SAP(action=\"i18n\", params={\"op\": \"texts_get\", \"program_name\": \"ZDEMO\"})\n"+
				"  SAP(action=\"i18n\", params={\"op\": \"texts_set\", \"program_name\": \"ZDEMO\", \"texts\": {\"P_DEVC\": \"Package\"}, \"dry_run\": true})\n"+
				"  SAP(action=\"i18n\", params={\"op\": \"data_element_labels\", \"name\": \"ZED_DEMO\", \"language\": \"EN\"})\n"+
				"  SAP(action=\"i18n\", params={\"op\": \"write_labels\", \"name\": \"ZED_DEMO\", \"language\": \"ES\", \"short\": \"Texto\"})",
			i18nOps())), true, nil
	}
	return s.callHandler(ctx, handler, params)
}
