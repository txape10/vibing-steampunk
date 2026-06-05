// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_crud.go contains handlers for CRUD operations (lock, unlock, create, update, delete).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// routeCRUDAction routes "edit" for LOCK/UNLOCK/UPDATE_SOURCE/
// RECOVER_FAILED_CREATE, "create" for OBJECT/DEVC/TABL/CLONE, "delete"
// for OBJECT.
func (s *Server) routeCRUDAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action == "edit" {
		switch objectType {
		case "LOCK":
			return s.callHandler(ctx, s.handleLockObject, params)
		case "UNLOCK":
			return s.callHandler(ctx, s.handleUnlockObject, params)
		case "UPDATE_SOURCE":
			return s.callHandler(ctx, s.handleUpdateSource, params)
		case "MOVE":
			return s.callHandler(ctx, s.handleMoveObject, params)
		case "COMPARE_SOURCE":
			return s.callHandler(ctx, s.handleCompareSource, params)
		case "RECOVER_FAILED_CREATE":
			return s.callHandler(ctx, s.handleRecoverFailedCreate, params)
		}
	}

	if action == "create" {
		switch objectType {
		case "OBJECT":
			return s.callHandler(ctx, s.handleCreateObject, params)
		case "DEVC":
			return s.callHandler(ctx, s.handleCreatePackage, params)
		case "TABL":
			return s.callHandler(ctx, s.handleCreateTable, params)
		case "STRU":
			return s.callHandler(ctx, s.handleCreateStructure, params)
		case "DOMA":
			return s.callHandler(ctx, s.handleCreateDomain, params)
		case "DTEL":
			return s.callHandler(ctx, s.handleCreateDataElement, params)
		case "TTYP":
			return s.callHandler(ctx, s.handleCreateTableType, params)
		case "ENQU":
			return s.callHandler(ctx, s.handleCreateLockObject, params)
		case "MSAG":
			return s.callHandler(ctx, s.handleCreateMessageClass, params)
		case "CLONE":
			return s.callHandler(ctx, s.handleCloneObject, params)
		}
	}

	if action == "delete" {
		switch objectType {
		case "OBJECT", "":
			if getStringParam(params, "object_url") != "" {
				return s.callHandler(ctx, s.handleDeleteObject, params)
			}
		}
	}

	// read CLASS_INFO
	if action == "read" && objectType == "CLASS_INFO" {
		return s.callHandler(ctx, s.handleGetClassInfo, map[string]any{"class_name": objectName})
	}

	return nil, false, nil
}

// --- CRUD Handlers ---

func (s *Server) handleLockObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	accessMode := "MODIFY"
	if am, ok := request.GetArguments()["access_mode"].(string); ok && am != "" {
		accessMode = am
	}

	result, err := s.adtClient.LockObject(ctx, objectURL, accessMode)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to lock object: %v", err)), nil
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleUnlockObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	lockHandle, ok := request.GetArguments()["lock_handle"].(string)
	if !ok || lockHandle == "" {
		return newToolResultError("lock_handle is required"), nil
	}

	err := s.adtClient.UnlockObject(ctx, objectURL, lockHandle)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to unlock object: %v", err)), nil
	}

	return mcp.NewToolResultText("Object unlocked successfully"), nil
}

func (s *Server) handleUpdateSource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	source, ok := request.GetArguments()["source"].(string)
	if !ok || source == "" {
		return newToolResultError("source is required"), nil
	}

	lockHandle, ok := request.GetArguments()["lock_handle"].(string)
	if !ok || lockHandle == "" {
		return newToolResultError("lock_handle is required"), nil
	}

	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok {
		transport = t
	}

	// Append /source/main to object URL if not already present
	sourceURL := objectURL
	if !strings.HasSuffix(sourceURL, "/source/main") {
		sourceURL = objectURL + "/source/main"
	}

	err := s.adtClient.UpdateSource(ctx, sourceURL, source, lockHandle, transport)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to update source: %v", err)), nil
	}

	return mcp.NewToolResultText("Source updated successfully"), nil
}

func (s *Server) handleCreateObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectType, ok := request.GetArguments()["object_type"].(string)
	if !ok || objectType == "" {
		return newToolResultError("object_type is required"), nil
	}

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	description, ok := request.GetArguments()["description"].(string)
	if !ok || description == "" {
		return newToolResultError("description is required"), nil
	}

	packageName, ok := request.GetArguments()["package_name"].(string)
	if !ok || packageName == "" {
		return newToolResultError("package_name is required"), nil
	}

	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok {
		transport = t
	}

	parentName := ""
	if p, ok := request.GetArguments()["parent_name"].(string); ok {
		parentName = p
	}

	// RAP-specific options
	serviceDefinition := ""
	if sd, ok := request.GetArguments()["service_definition"].(string); ok {
		serviceDefinition = sd
	}
	bindingVersion := ""
	if bv, ok := request.GetArguments()["binding_version"].(string); ok {
		bindingVersion = bv
	}
	bindingCategory := ""
	if bc, ok := request.GetArguments()["binding_category"].(string); ok {
		bindingCategory = bc
	}

	opts := adt.CreateObjectOptions{
		ObjectType:        adt.CreatableObjectType(objectType),
		Name:              name,
		Description:       description,
		PackageName:       packageName,
		Transport:         transport,
		ParentName:        parentName,
		ServiceDefinition: serviceDefinition,
		BindingVersion:    bindingVersion,
		BindingCategory:   bindingCategory,
	}

	err := s.adtClient.CreateObject(ctx, opts)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create object: %v", err)), nil
	}

	// Return the object URL for convenience
	objURL := adt.GetObjectURL(opts.ObjectType, opts.Name, opts.ParentName)
	result := map[string]string{
		"status":     "created",
		"object_url": objURL,
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleCreatePackage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	name = strings.ToUpper(name)

	description, ok := request.GetArguments()["description"].(string)
	if !ok || description == "" {
		return newToolResultError("description is required"), nil
	}

	parent := ""
	if p, ok := request.GetArguments()["parent"].(string); ok && p != "" {
		parent = strings.ToUpper(p)
	}

	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok && t != "" {
		transport = t
	}

	softwareComponent := ""
	if sc, ok := request.GetArguments()["software_component"].(string); ok && sc != "" {
		softwareComponent = strings.ToUpper(sc)
	}

	// Transportable packages require transport parameter
	if !strings.HasPrefix(name, "$") && transport == "" {
		return newToolResultError("transport is required for creating transportable packages (non-$ packages). Use --enable-transports flag."), nil
	}

	opts := adt.CreateObjectOptions{
		ObjectType:        adt.ObjectTypePackage,
		Name:              name,
		Description:       description,
		PackageName:       parent, // Parent package
		Transport:         transport,
		SoftwareComponent: softwareComponent,
	}

	err := s.adtClient.CreateObject(ctx, opts)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create package: %v", err)), nil
	}

	result := map[string]string{
		"status":      "created",
		"package":     name,
		"description": description,
	}
	if parent != "" {
		result["parent"] = parent
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleCreateTable(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	description, ok := request.GetArguments()["description"].(string)
	if !ok || description == "" {
		return newToolResultError("description is required"), nil
	}

	fieldsJSON, ok := request.GetArguments()["fields"].(string)
	if !ok || fieldsJSON == "" {
		return newToolResultError("fields is required (JSON array)"), nil
	}

	// Parse fields JSON
	var fields []adt.TableField
	if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
		return newToolResultError(fmt.Sprintf("Invalid fields JSON: %v", err)), nil
	}

	if len(fields) == 0 {
		return newToolResultError("At least one field is required"), nil
	}

	// Optional parameters
	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}

	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok && t != "" {
		transport = t
	}

	deliveryClass := "A"
	if dc, ok := request.GetArguments()["delivery_class"].(string); ok && dc != "" {
		deliveryClass = strings.ToUpper(dc)
	}

	opts := adt.CreateTableOptions{
		Name:          name,
		Description:   description,
		Package:       pkg,
		Fields:        fields,
		Transport:     transport,
		DeliveryClass: deliveryClass,
	}

	err := s.adtClient.CreateTable(ctx, opts)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create table: %v", err)), nil
	}

	result := map[string]interface{}{
		"status":      "created",
		"table":       strings.ToUpper(name),
		"package":     pkg,
		"description": description,
		"fields":      len(fields),
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleCreateStructure(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}

	description, ok := request.GetArguments()["description"].(string)
	if !ok || description == "" {
		return newToolResultError("description is required"), nil
	}

	fieldsJSON, ok := request.GetArguments()["fields"].(string)
	if !ok || fieldsJSON == "" {
		return newToolResultError("fields is required (JSON array)"), nil
	}

	var fields []adt.TableField
	if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
		return newToolResultError(fmt.Sprintf("Invalid fields JSON: %v", err)), nil
	}
	if len(fields) == 0 {
		return newToolResultError("At least one field is required"), nil
	}

	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}

	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok && t != "" {
		transport = t
	}

	opts := adt.CreateStructureOptions{
		Name:        name,
		Description: description,
		Package:     pkg,
		Fields:      fields,
		Transport:   transport,
	}

	if err := s.adtClient.CreateStructure(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create structure: %v", err)), nil
	}

	result := map[string]interface{}{
		"status":      "created",
		"structure":   strings.ToUpper(name),
		"package":     pkg,
		"description": description,
		"fields":      len(fields),
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil

}

func (s *Server) handleCreateDomain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}
	description, _ := request.GetArguments()["description"].(string)
	dataType, ok := request.GetArguments()["data_type"].(string)
	if !ok || dataType == "" {
		return newToolResultError("data_type is required (e.g. CHAR, NUMC, INT4)"), nil
	}

	length := 0
	if v, ok := request.GetArguments()["length"]; ok {
		switch n := v.(type) {
		case float64:
			length = int(n)
		case int:
			length = n
		}
	}

	decimals := 0
	if v, ok := request.GetArguments()["decimals"]; ok {
		if n, ok := v.(float64); ok {
			decimals = int(n)
		}
	}

	lowercase := false
	if v, ok := request.GetArguments()["lowercase"].(bool); ok {
		lowercase = v
	}

	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}
	transport, _ := request.GetArguments()["transport"].(string)

	var fixedValues []adt.DomainFixedValue
	if fvJSON, ok := request.GetArguments()["fixed_values"].(string); ok && fvJSON != "" {
		if err := json.Unmarshal([]byte(fvJSON), &fixedValues); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid fixed_values JSON: %v", err)), nil
		}
	}

	opts := adt.CreateDomainOptions{
		Name:        name,
		Description: description,
		Package:     pkg,
		DataType:    dataType,
		Length:      length,
		Decimals:    decimals,
		Lowercase:   lowercase,
		FixedValues: fixedValues,
		Transport:   transport,
	}

	if err := s.adtClient.CreateDomain(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create domain: %v", err)), nil
	}

	result := map[string]interface{}{
		"status": "created", "domain": strings.ToUpper(name),
		"package": pkg, "data_type": dataType, "length": length,
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil

}

func (s *Server) handleCreateDataElement(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}
	description, _ := request.GetArguments()["description"].(string)
	typeName, ok := request.GetArguments()["type_name"].(string)
	if !ok || typeName == "" {
		return newToolResultError("type_name is required (domain name or ABAP type)"), nil
	}

	typeKind, _ := request.GetArguments()["type_kind"].(string)
	dataType, _ := request.GetArguments()["data_type"].(string)
	var dataTypeLength, dataTypeDecimals int
	if n, ok := request.GetArguments()["data_type_length"].(float64); ok {
		dataTypeLength = int(n)
	}
	if n, ok := request.GetArguments()["data_type_decimals"].(float64); ok {
		dataTypeDecimals = int(n)
	}
	labelShort, _ := request.GetArguments()["label_short"].(string)
	labelMedium, _ := request.GetArguments()["label_medium"].(string)
	labelLong, _ := request.GetArguments()["label_long"].(string)
	labelHeading, _ := request.GetArguments()["label_heading"].(string)
	searchHelp, _ := request.GetArguments()["search_help"].(string)
	parameterID, _ := request.GetArguments()["parameter_id"].(string)

	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}
	transport, _ := request.GetArguments()["transport"].(string)

	opts := adt.CreateDataElementOptions{
		Name:             name,
		Description:      description,
		Package:          pkg,
		TypeKind:         typeKind,
		TypeName:         typeName,
		DataType:         dataType,
		DataTypeLength:   dataTypeLength,
		DataTypeDecimals: dataTypeDecimals,
		LabelShort:       labelShort,
		LabelMedium:      labelMedium,
		LabelLong:        labelLong,
		LabelHeading:     labelHeading,
		SearchHelp:       searchHelp,
		ParameterID:      parameterID,
		Transport:        transport,
	}

	if err := s.adtClient.CreateDataElement(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create data element: %v", err)), nil
	}

	result := map[string]interface{}{
		"status": "created", "data_element": strings.ToUpper(name),
		"package": pkg, "type_name": typeName,
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil

}

func (s *Server) handleCreateTableType(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}
	description, _ := request.GetArguments()["description"].(string)
	rowTypeName, ok := request.GetArguments()["row_type_name"].(string)
	if !ok || rowTypeName == "" {
		return newToolResultError("row_type_name is required (e.g. BAPIRET2)"), nil
	}

	rowTypeKind, _ := request.GetArguments()["row_type_kind"].(string)
	accessType, _ := request.GetArguments()["access_type"].(string)
	keyDef, _ := request.GetArguments()["key_definition"].(string)
	keyKind, _ := request.GetArguments()["key_kind"].(string)

	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}
	transport, _ := request.GetArguments()["transport"].(string)

	opts := adt.CreateTableTypeOptions{
		Name:        name,
		Description: description,
		Package:     pkg,
		RowTypeKind: rowTypeKind,
		RowTypeName: rowTypeName,
		AccessType:  accessType,
		KeyDef:      keyDef,
		KeyKind:     keyKind,
		Transport:   transport,
	}

	if err := s.adtClient.CreateTableType(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create table type: %v", err)), nil
	}

	result := map[string]interface{}{
		"status": "created", "table_type": strings.ToUpper(name),
		"package": pkg, "row_type": rowTypeName,
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil

}

func (s *Server) handleCreateLockObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}
	description, _ := request.GetArguments()["description"].(string)
	primaryTable, ok := request.GetArguments()["primary_table"].(string)
	if !ok || primaryTable == "" {
		return newToolResultError("primary_table is required"), nil
	}

	lockMode, _ := request.GetArguments()["lock_mode"].(string)
	allowRFC, _ := request.GetArguments()["allow_rfc"].(bool)

	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}
	transport, _ := request.GetArguments()["transport"].(string)

	var lockParams []adt.LockObjectParameter
	if lpJSON, ok := request.GetArguments()["lock_parameters"].(string); ok && lpJSON != "" {
		if err := json.Unmarshal([]byte(lpJSON), &lockParams); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid lock_parameters JSON: %v", err)), nil
		}
	}

	opts := adt.CreateLockObjectOptions{
		Name:           name,
		Description:    description,
		Package:        pkg,
		PrimaryTable:   primaryTable,
		LockMode:       lockMode,
		LockParameters: lockParams,
		AllowRFC:       allowRFC,
		Transport:      transport,
	}

	if err := s.adtClient.CreateLockObject(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create lock object: %v", err)), nil
	}

	result := map[string]interface{}{
		"status": "created", "lock_object": strings.ToUpper(name),
		"package": pkg, "primary_table": strings.ToUpper(primaryTable),
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil

}

func (s *Server) handleCreateMessageClass(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}
	description, _ := request.GetArguments()["description"].(string)

	pkg := "$TMP"
	if p, ok := request.GetArguments()["package"].(string); ok && p != "" {
		pkg = strings.ToUpper(p)
	}
	language, _ := request.GetArguments()["language"].(string)
	transport, _ := request.GetArguments()["transport"].(string)

	var messages []adt.MessageClassMessage
	if msgsJSON, ok := request.GetArguments()["messages"].(string); ok && msgsJSON != "" {
		if err := json.Unmarshal([]byte(msgsJSON), &messages); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid messages JSON: %v", err)), nil
		}
	}

	opts := adt.CreateMessageClassOptions{
		Name:        name,
		Description: description,
		Package:     pkg,
		Language:    language,
		Messages:    messages,
		Transport:   transport,
	}

	if err := s.adtClient.CreateMessageClass(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create message class: %v", err)), nil
	}

	result := map[string]interface{}{
		"status":        "created",
		"message_class": strings.ToUpper(name),
		"package":       pkg,
		"messages":      len(messages),
	}
	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil

}

func (s *Server) handleCompareSource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type1, _ := request.GetArguments()["type1"].(string)
	name1, _ := request.GetArguments()["name1"].(string)
	type2, _ := request.GetArguments()["type2"].(string)
	name2, _ := request.GetArguments()["name2"].(string)

	if type1 == "" || name1 == "" || type2 == "" || name2 == "" {
		return newToolResultError("type1, name1, type2, and name2 are all required"), nil
	}

	// Build options for first object
	opts1 := &adt.GetSourceOptions{}
	if inc, ok := request.GetArguments()["include1"].(string); ok && inc != "" {
		opts1.Include = inc
	}
	if parent, ok := request.GetArguments()["parent1"].(string); ok && parent != "" {
		opts1.Parent = parent
	}

	// Build options for second object
	opts2 := &adt.GetSourceOptions{}
	if inc, ok := request.GetArguments()["include2"].(string); ok && inc != "" {
		opts2.Include = inc
	}
	if parent, ok := request.GetArguments()["parent2"].(string); ok && parent != "" {
		opts2.Parent = parent
	}

	diff, err := s.adtClient.CompareSource(ctx, type1, name1, type2, name2, opts1, opts2)
	if err != nil {
		return newToolResultError(fmt.Sprintf("CompareSource failed: %v", err)), nil
	}

	output, _ := json.MarshalIndent(diff, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleCloneObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectType, _ := request.GetArguments()["object_type"].(string)
	sourceName, _ := request.GetArguments()["source_name"].(string)
	targetName, _ := request.GetArguments()["target_name"].(string)
	pkg, _ := request.GetArguments()["package"].(string)

	if objectType == "" || sourceName == "" || targetName == "" || pkg == "" {
		return newToolResultError("object_type, source_name, target_name, and package are all required"), nil
	}

	result, err := s.adtClient.CloneObject(ctx, objectType, sourceName, targetName, pkg)
	if err != nil {
		return newToolResultError(fmt.Sprintf("CloneObject failed: %v", err)), nil
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleGetClassInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	className, _ := request.GetArguments()["class_name"].(string)
	if className == "" {
		return newToolResultError("class_name is required"), nil
	}

	info, err := s.adtClient.GetClassInfo(ctx, className)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetClassInfo failed: %v", err)), nil
	}

	output, _ := json.MarshalIndent(info, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

// handleRecoverFailedCreate is the MCP-facing recovery primitive for a
// zombie object that a previous CreateObject attempt left behind
// (e.g. after a 5xx where SAP persisted the skeleton before the HTTP
// response failed). The caller does NOT need a lock handle from the
// original session — the handler itself probes existence, acquires a
// fresh lock, and drives DeleteObject. See pkg/adt.RecoverFailedCreate
// and the partial-create RCA for the full story.
//
// Inputs (all required except transport / parent_name):
//
//	object_type   — CLAS / PROG / INTF / FUGR / DDLS / ...
//	name          — object name
//	package_name  — for the safety gate; must be in allowed list
//	parent_name   — required for FUNC (parent function group)
//	transport     — optional TR the zombie was attached to
//
// The output is a structured JSON document describing what was tried:
//
//	{
//	  "status": "cleaned" | "already_clean" | "partial" | "probe_failed",
//	  "object_url": "...",
//	  "package": "...",
//	  "transport": "...",
//	  "cleanup_actions": ["..."],
//	  "manual_steps":    ["..."]
//	}
//
// Manual steps are only populated when the cleanup could not finish
// — typically because another user holds a lock or because DeleteObject
// itself failed. The operator can copy those directly into SAPGUI.
func (s *Server) handleRecoverFailedCreate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectType, ok := request.GetArguments()["object_type"].(string)
	if !ok || objectType == "" {
		return newToolResultError("object_type is required"), nil
	}
	name, ok := request.GetArguments()["name"].(string)
	if !ok || name == "" {
		return newToolResultError("name is required"), nil
	}
	packageName, ok := request.GetArguments()["package_name"].(string)
	if !ok || packageName == "" {
		return newToolResultError("package_name is required"), nil
	}
	parentName := ""
	if p, ok := request.GetArguments()["parent_name"].(string); ok {
		parentName = p
	}
	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok {
		transport = t
	}

	opts := adt.CreateObjectOptions{
		ObjectType:  adt.CreatableObjectType(objectType),
		Name:        name,
		PackageName: packageName,
		ParentName:  parentName,
		Transport:   transport,
	}

	pce := s.adtClient.RecoverFailedCreate(ctx, opts)

	// Classify the outcome for the UI layer. The four cases correspond
	// to the four shapes RecoverFailedCreate returns:
	//
	//   cleaned        → probe found the object, cleanup succeeded
	//   already_clean  → probe found nothing, idempotent no-op
	//   partial        → probe found the object, cleanup could not finish
	//   probe_failed   → could not even determine whether the object exists
	status := "partial"
	switch {
	case pce.CleanupOK && len(pce.CleanupActions) == 1 &&
		strings.Contains(pce.CleanupActions[0], "nothing to recover"):
		status = "already_clean"
	case pce.CleanupOK:
		status = "cleaned"
	case pce.OriginalErr != nil && strings.Contains(pce.OriginalErr.Error(), "existence probe failed"):
		status = "probe_failed"
	}

	result := map[string]any{
		"status":          status,
		"object_url":      pce.ObjectURL,
		"package":         pce.Package,
		"transport":       pce.Transport,
		"cleanup_actions": pce.CleanupActions,
	}
	if len(pce.ManualSteps) > 0 {
		result["manual_steps"] = pce.ManualSteps
	}
	if pce.OriginalErr != nil {
		result["last_error"] = pce.OriginalErr.Error()
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleDeleteObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	transport := ""
	if t, ok := request.GetArguments()["transport"].(string); ok {
		transport = t
	}

	// lock_handle is optional. When not provided, DeleteObjectWithAutoLock acquires
	// the lock and deletes atomically in a single stateful session, avoiding the
	// session-affinity problem where a lock from a prior MCP call is invalidated
	// before the delete call reaches SAP (issue #88 / "lock handle rejected").
	lockHandle, _ := request.GetArguments()["lock_handle"].(string)
	if lockHandle != "" {
		// Caller already holds a lock — use it directly.
		err := s.adtClient.DeleteObject(ctx, objectURL, lockHandle, transport)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to delete object: %v", err)), nil
		}
	} else {
		// Auto-lock + delete atomically.
		err := s.adtClient.DeleteObjectWithAutoLock(ctx, objectURL, transport)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to delete object: %v", err)), nil
		}
	}

	return mcp.NewToolResultText("Object deleted successfully"), nil
}

func (s *Server) handleMoveObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectType, ok := request.GetArguments()["object_type"].(string)
	if !ok || objectType == "" {
		return newToolResultError("object_type is required"), nil
	}

	objectName, ok := request.GetArguments()["object_name"].(string)
	if !ok || objectName == "" {
		return newToolResultError("object_name is required"), nil
	}

	newPackage, ok := request.GetArguments()["new_package"].(string)
	if !ok || newPackage == "" {
		return newToolResultError("new_package is required"), nil
	}

	// Ensure WebSocket client is connected
	if err := s.ensureDebugWSClient(ctx); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect to ZADT_VSP WebSocket: %v. Ensure ZADT_VSP is deployed and SAPC/SICF are configured.", err)), nil
	}

	result, err := s.debugWSClient.MoveObject(ctx, objectType, objectName, newPackage)
	if err != nil {
		return newToolResultError(fmt.Sprintf("MoveObject failed: %v", err)), nil
	}

	// Format result
	if result.Success {
		return mcp.NewToolResultText(fmt.Sprintf("Object moved successfully.\n\nObject: %s %s\nNew Package: %s\nMessage: %s",
			result.Object, result.ObjName, result.NewPackage, result.Message)), nil
	}
	return newToolResultError(fmt.Sprintf("Move failed: %s", result.Message)), nil
}
