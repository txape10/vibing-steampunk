package adt

import (
	"context"
	"fmt"
	"strings"
)

// CheckObjectPackageByName checks the package an existing object lives in
// against the configured package whitelist, finding the object by TADIR type
// and name instead of by ADT URL. It is for callers that act on an object
// through a path with no ADT URL (the ZADT_VSP WebSocket: MOVE, text pool).
//
// With no whitelist configured it returns nil without sending anything. With
// one it fails closed: an object that cannot be found, a search window that
// may hide further objects of that name, or one hit in a package that is not
// allowed all refuse. It runs one stateless search, so it belongs before any
// lock or WebSocket work, never inside a lock window.
func (c *Client) CheckObjectPackageByName(ctx context.Context, objType, name, opName string) error {
	if len(c.config.Safety.AllowedPackages) == 0 {
		return nil
	}

	wantType := searchTypeRoot(objType)
	hits, incomplete, err := c.SearchObjectExact(ctx, name, "", 0)
	if err != nil {
		return fmt.Errorf("operation '%s': cannot resolve the package of %s %s, refused: %w", opName, objType, name, err)
	}

	var matches []SearchResult
	for _, h := range hits {
		if searchTypeRoot(h.Type) == wantType {
			matches = append(matches, h)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("operation '%s': %s %s not found, package cannot be checked, refused", opName, objType, name)
	}
	if incomplete != "" {
		return fmt.Errorf("operation '%s': package of %s %s cannot be established reliably, refused: %s", opName, objType, name, incomplete)
	}
	for _, m := range matches {
		if m.PackageName == "" {
			return fmt.Errorf("operation '%s': package of %s %s is unknown, refused", opName, objType, name)
		}
		if err := c.checkPackageSafety(m.PackageName); err != nil {
			return fmt.Errorf("operation '%s': source package of %s %s: %w", opName, objType, name, err)
		}
	}
	return nil
}

// ObjectPackageByName returns the package an existing object lives in, found by
// TADIR type and name. Unlike CheckObjectPackageByName it always searches,
// whatever the whitelist. It fails closed: not found, a search window that may
// hide more objects, an empty package or hits in different packages are errors.
// One stateless search: never call it inside a lock window.
func (c *Client) ObjectPackageByName(ctx context.Context, objType, name, opName string) (string, error) {
	wantType := searchTypeRoot(objType)
	hits, incomplete, err := c.SearchObjectExact(ctx, name, "", 0)
	if err != nil {
		return "", fmt.Errorf("operation '%s': cannot resolve the package of %s %s, refused: %w", opName, objType, name, err)
	}
	pkg := ""
	for _, h := range hits {
		if searchTypeRoot(h.Type) != wantType {
			continue
		}
		if h.PackageName == "" {
			return "", fmt.Errorf("operation '%s': package of %s %s is unknown, refused", opName, objType, name)
		}
		if pkg != "" && !strings.EqualFold(pkg, h.PackageName) {
			return "", fmt.Errorf("operation '%s': %s %s is ambiguous (packages %s and %s), refused", opName, objType, name, pkg, h.PackageName)
		}
		pkg = h.PackageName
	}
	if pkg == "" {
		return "", fmt.Errorf("operation '%s': %s %s not found, package cannot be established, refused", opName, objType, name)
	}
	if incomplete != "" {
		return "", fmt.Errorf("operation '%s': package of %s %s cannot be established reliably, refused: %s", opName, objType, name, incomplete)
	}
	return pkg, nil
}

// searchTypeRoot reduces an object type to its TADIR-level root: "TABL/DS"
// and "TABL" both give "TABL"; an include ("INCL", "PROG/I") is a PROG.
func searchTypeRoot(t string) string {
	t = strings.ToUpper(strings.TrimSpace(t))
	if t == "INCL" {
		return "PROG"
	}
	t = CanonicalObjectType(t)
	if i := strings.Index(t, "/"); i >= 0 {
		t = t[:i]
	}
	if t == "INCL" {
		return "PROG"
	}
	return t
}
