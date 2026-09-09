// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_i18n.go contains handlers for translation/internationalization operations.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// --- i18n Handlers ---

func (s *Server) handleGetObjectTextsInLanguage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	content, err := s.adtClient.GetObjectTextsInLanguage(ctx, objectURL, lang)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetObjectTextsInLanguage failed: %v", err)), nil
	}

	return mcp.NewToolResultText(content), nil
}

func (s *Server) handleGetDataElementLabels(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	labels, err := s.adtClient.GetDataElementLabels(ctx, name, lang)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetDataElementLabels failed: %v", err)), nil
	}

	jsonBytes, err := json.MarshalIndent(labels, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to format result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

func (s *Server) handleGetMessageClassTexts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	texts, err := s.adtClient.GetMessageClassTexts(ctx, name, lang)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetMessageClassTexts failed: %v", err)), nil
	}

	if len(texts) == 0 {
		return mcp.NewToolResultText("No messages found."), nil
	}

	jsonBytes, err := json.MarshalIndent(texts, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to format result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

func (s *Server) handleWriteMessageClassTexts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	lockHandle, ok := request.GetArguments()["lock_handle"].(string)
	if !ok || lockHandle == "" {
		return newToolResultError("lock_handle is required"), nil
	}

	transport, _ := request.GetArguments()["transport"].(string)

	// texts is an upsert list; delete_numbers is the only way to actually
	// remove a message. At least one of the two must carry something, or the
	// call would be a no-op.
	var texts []adt.MessageClassMessage
	if textsRaw, ok := request.GetArguments()["texts"]; ok && textsRaw != nil {
		textsJSON, err := json.Marshal(textsRaw)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse texts: %v", err)), nil
		}
		if err := json.Unmarshal(textsJSON, &texts); err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse texts: %v", err)), nil
		}
	}

	var deleteNumbers []string
	if deleteRaw, ok := request.GetArguments()["delete_numbers"]; ok && deleteRaw != nil {
		deleteJSON, err := json.Marshal(deleteRaw)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse delete_numbers: %v", err)), nil
		}
		if err := json.Unmarshal(deleteJSON, &deleteNumbers); err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse delete_numbers: %v", err)), nil
		}
	}

	if len(texts) == 0 && len(deleteNumbers) == 0 {
		return newToolResultError("at least one of texts or delete_numbers is required"), nil
	}

	err := s.adtClient.WriteMessageClassTexts(ctx, name, lang, texts, deleteNumbers, lockHandle, transport)
	if err != nil {
		return newToolResultError(fmt.Sprintf("WriteMessageClassTexts failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Message class %s texts updated successfully in language %s.", name, lang)), nil
}

// handleWriteMessageClassTextsAutoLock is the auto-locking counterpart of
// handleWriteMessageClassTexts, for callers that don't manage lock handles
// themselves — currently only the hyperfocused `edit MSAG` route
// (routeSourceAction in handlers_source.go), since WriteSource itself has no
// MSAG case.
func (s *Server) handleWriteMessageClassTextsAutoLock(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	transport, _ := request.GetArguments()["transport"].(string)

	var texts []adt.MessageClassMessage
	if textsRaw, ok := request.GetArguments()["texts"]; ok && textsRaw != nil {
		textsJSON, err := json.Marshal(textsRaw)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse texts: %v", err)), nil
		}
		if err := json.Unmarshal(textsJSON, &texts); err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse texts: %v", err)), nil
		}
	}

	var deleteNumbers []string
	if deleteRaw, ok := request.GetArguments()["delete_numbers"]; ok && deleteRaw != nil {
		deleteJSON, err := json.Marshal(deleteRaw)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse delete_numbers: %v", err)), nil
		}
		if err := json.Unmarshal(deleteJSON, &deleteNumbers); err != nil {
			return newToolResultError(fmt.Sprintf("Failed to parse delete_numbers: %v", err)), nil
		}
	}

	if len(texts) == 0 && len(deleteNumbers) == 0 {
		return newToolResultError("at least one of texts or delete_numbers is required"), nil
	}

	if err := s.adtClient.WriteMessageClassTextsAutoLock(ctx, name, lang, texts, deleteNumbers, transport); err != nil {
		return newToolResultError(fmt.Sprintf("WriteMessageClassTexts failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Message class %s texts updated successfully in language %s.", name, lang)), nil
}

func (s *Server) handleWriteDataElementLabels(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	lockHandle, ok := request.GetArguments()["lock_handle"].(string)
	if !ok || lockHandle == "" {
		return newToolResultError("lock_handle is required"), nil
	}

	transport, _ := request.GetArguments()["transport"].(string)

	labels := &adt.DataElementLabels{}
	if short, ok := request.GetArguments()["short"].(string); ok {
		labels.Short = short
	}
	if medium, ok := request.GetArguments()["medium"].(string); ok {
		labels.Medium = medium
	}
	if long, ok := request.GetArguments()["long"].(string); ok {
		labels.Long = long
	}
	if heading, ok := request.GetArguments()["heading"].(string); ok {
		labels.Heading = heading
	}

	err := s.adtClient.WriteDataElementLabels(ctx, name, lang, labels, lockHandle, transport)
	if err != nil {
		return newToolResultError(fmt.Sprintf("WriteDataElementLabels failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Data element %s labels updated successfully in language %s.", name, lang)), nil
}

func (s *Server) handleGetTextPoolInLanguage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	programName, ok := request.GetArguments()["program_name"].(string)
	if !ok || programName == "" {
		return newToolResultError("program_name is required"), nil
	}

	lang, ok := request.GetArguments()["language"].(string)
	if !ok || lang == "" {
		return newToolResultError("language is required"), nil
	}

	entries, err := s.adtClient.GetTextPoolInLanguage(ctx, programName, lang)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetTextPoolInLanguage failed: %v", err)), nil
	}

	if len(entries) == 0 {
		return mcp.NewToolResultText("No text pool entries found."), nil
	}

	jsonBytes, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to format result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

func (s *Server) handleCompareObjectLanguages(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	sourceLang, ok := request.GetArguments()["source_language"].(string)
	if !ok || sourceLang == "" {
		return newToolResultError("source_language is required"), nil
	}

	targetLang, ok := request.GetArguments()["target_language"].(string)
	if !ok || targetLang == "" {
		return newToolResultError("target_language is required"), nil
	}

	comparison, err := s.adtClient.CompareObjectLanguages(ctx, objectURL, sourceLang, targetLang)
	if err != nil {
		return newToolResultError(fmt.Sprintf("CompareObjectLanguages failed: %v", err)), nil
	}

	jsonBytes, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to format result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}
