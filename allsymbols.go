package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"sort"
	"strings"
	"strconv"

	"golang.org/x/tools/go/packages"
)

// ChromaDocument represents a chunk to be stored
type ChromaDocument struct {
	ID       string                 `json:"id"`
	Document string                 `json:"document"`
	Metadata map[string]interface{} `json:"metadata"`
}

func main() {
	// IMPORTANT: Set this to the absolute path of your 'sdn' directory.
	// Make sure this directory contains a go.mod file or is part of a go.work workspace.
	projectPath := "/home/vsunku/DEV/builder"

	chunks, err := processGoProject(projectPath)
	if err != nil {
		log.Fatalf("Error processing Go project: %v", err)
	}

	outputFileName := "code_chunks_test.json" // New output file name
	jsonData, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		log.Fatalf("Error marshaling chunks to JSON: %v", err)
	}

	err = ioutil.WriteFile(outputFileName, jsonData, 0644)
	if err != nil {
		log.Fatalf("Error writing JSON to file: %v", err)
	}

	fmt.Printf("Successfully extracted %d code chunks to %s\n", len(chunks), outputFileName)
}

func processGoProject(projectPath string) ([]ChromaDocument, error) {
	var chunks []ChromaDocument
	fset := token.NewFileSet()

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedExportsFile |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
		Fset:  fset,
		Dir:   projectPath,
		Tests: false,
	}

	log.Printf("Loading packages from %s...", projectPath)
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}
	log.Printf("Finished loading %d packages.", len(pkgs))

	hasErrors := false
	for _, pkg := range pkgs {
		if pkg.Errors != nil {
			for _, pkgErr := range pkg.Errors {
				log.Printf("Package loading error in %s: %v", pkg.ID, pkgErr)
				hasErrors = true
			}
		}
	}
	if hasErrors {
		log.Println("Errors occurred during package loading. Some information might be incomplete. Continuing with available data.")
	}

	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil || pkg.Syntax == nil || pkg.Fset == nil {
			log.Printf("Skipping package %s due to missing type information, syntax trees, or fileset.", pkg.ID)
			continue
		}

		for _, file := range pkg.Syntax {
			filePath := fset.File(file.Pos()).Name()
			originalFileBytes, err := ioutil.ReadFile(filePath)
			if err != nil {
				log.Printf("Error reading file %s: %v", filePath, err)
				continue
			}

			packageName := pkg.Name
			originalFileContentString := string(originalFileBytes) // Convert once for slicing

			// Iterate over all top-level declarations in the file
			for _, decl := range file.Decls {
				// Initialize common metadata fields
				metadata := map[string]interface{}{
					"file_path":    filePath,
					"package_name": packageName,
				}

				// --- Extract Pos/End for the current declaration ---
				startPos := fset.Position(decl.Pos())
				endPos := fset.Position(decl.End())

				startOffset := startPos.Offset
				endOffset := endPos.Offset

				// Basic offset validation for the declaration's overall chunk
				if startOffset < 0 || endOffset > len(originalFileContentString) || startOffset > endOffset {
					log.Printf("Warning: Invalid offsets for declaration in %s (line %d): start=%d, end=%d, file_len=%d. Skipping declaration.",
						filePath, startPos.Line, startOffset, endOffset, len(originalFileContentString))
					continue // Skip this declaration if offsets are invalid
				}
				// Initial chunkCode for the whole declaration block
				declChunkCode := originalFileContentString[startOffset:endOffset]

				// --- Determine the type of declaration and extract specific info ---
				if funcDecl, isFuncDecl := decl.(*ast.FuncDecl); isFuncDecl {
					// Handle Function/Method Declaration
					metadata["entity_type"] = "function"
					metadata["entity_name"] = funcDecl.Name.Name
					metadata["start_line"] = startPos.Line
					metadata["end_line"] = endPos.Line
					metadata["signature"] = getSignature(funcDecl.Type, pkg.TypesInfo)

					if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
						metadata["entity_type"] = "method"
						receiverType := getTypeString(funcDecl.Recv.List[0].Type, pkg.TypesInfo)
						metadata["receiver_type"] = receiverType
						metadata["entity_name"] = receiverType + "." + funcDecl.Name.Name
					}

					// Apply replacements to the function's code chunk
					finalChunkCode := applyQualifierReplacements(declChunkCode, funcDecl, pkg.TypesInfo)

					chunks = append(chunks, ChromaDocument{
						ID:       fmt.Sprintf("%s:%d-%d-%s", filePath, startPos.Line, endPos.Line, funcDecl.Name.Name),
						Document: finalChunkCode,
						Metadata: metadata,
					})

				} else if genDecl, isGenDecl := decl.(*ast.GenDecl); isGenDecl {
					// Handle General Declaration (var, const, type, import)
					if genDecl.Tok == token.IMPORT {
						continue // Skip import declarations; they're handled by qualifier replacement logic
					}

					// For GenDecl, we process each 'Spec' within it separately.
					// The metadata's line numbers for specs will be per-spec.
					for _, spec := range genDecl.Specs {
						specStartPos := fset.Position(spec.Pos())
						specEndPos := fset.Position(spec.End())
						specStartOffset := specStartPos.Offset
						specEndOffset := specEndPos.Offset

						if specStartOffset < 0 || specEndOffset > len(originalFileContentString) || specStartOffset > specEndOffset {
							log.Printf("Warning: Invalid offsets for spec in %s (line %d): start=%d, end=%d, file_len=%d. Skipping spec.",
								filePath, specStartPos.Line, specStartOffset, specEndOffset, len(originalFileContentString))
							continue
						}
						specChunkCode := originalFileContentString[specStartOffset:specEndOffset]

						// Create specific metadata for this spec
						specMetadata := make(map[string]interface{})
						for k, v := range metadata { // Copy common file/package info
							specMetadata[k] = v
						}
						specMetadata["start_line"] = specStartPos.Line
						specMetadata["end_line"] = specEndPos.Line
						specMetadata["declaration_kind"] = genDecl.Tok.String() // "var", "const", "type"

						var entityName string

						if typeSpec, isTypeSpec := spec.(*ast.TypeSpec); isTypeSpec {
							// Handle Type Declaration (struct, interface, alias, etc.)
							specMetadata["entity_type"] = "type_declaration"
							entityName = typeSpec.Name.Name
							specMetadata["entity_name"] = entityName
							specMetadata["type_definition"] = getTypeString(typeSpec.Type, pkg.TypesInfo)

							if _, isStruct := typeSpec.Type.(*ast.StructType); isStruct {
								specMetadata["type_category"] = "struct"
							} else if _, isInterface := typeSpec.Type.(*ast.InterfaceType); isInterface {
								specMetadata["type_category"] = "interface"
							} else {
								specMetadata["type_category"] = "alias_or_basic"
							}

							// Apply replacements to the type spec's code chunk
							finalChunkCode := applyQualifierReplacements(specChunkCode, typeSpec, pkg.TypesInfo)

							chunks = append(chunks, ChromaDocument{
								ID:       fmt.Sprintf("%s:%d-%d-%s", filePath, specStartPos.Line, specEndPos.Line, entityName),
								Document: finalChunkCode,
								Metadata: specMetadata,
							})

						} else if valueSpec, isValueSpec := spec.(*ast.ValueSpec); isValueSpec {
							// Handle Variable or Constant Declaration
							specMetadata["entity_type"] = "value_declaration"
							var names []string
							for _, name := range valueSpec.Names {
								names = append(names, name.Name)
							}
							entityName = strings.Join(names, ", ")
							specMetadata["entity_name"] = entityName

							if valueSpec.Type != nil {
								specMetadata["declared_type"] = getTypeString(valueSpec.Type, pkg.TypesInfo)
							} else if len(valueSpec.Values) > 0 {
								if tv := pkg.TypesInfo.TypeOf(valueSpec.Values[0]); tv != nil {
									specMetadata["inferred_type"] = tv.String()
								}
							}

							// Apply replacements to the value spec's code chunk
							finalChunkCode := applyQualifierReplacements(specChunkCode, valueSpec, pkg.TypesInfo)

							chunks = append(chunks, ChromaDocument{
								ID:       fmt.Sprintf("%s:%d-%d-%s", filePath, specStartPos.Line, specEndPos.Line, entityName),
								Document: finalChunkCode,
								Metadata: specMetadata,
							})
						}
					}
				}
			}
		}
	}

	return chunks, nil
}


// applyQualifierReplacements inspects the given node's subtree for SelectorExprs
// and replaces package qualifiers with their full import paths in the chunkCode string.
// It uses a two-pass replacement strategy with unique placeholders to prevent cascading
// replacements where a full import path might contain another package alias.
func applyQualifierReplacements(chunkCode string, node ast.Node, info *types.Info) string {
	// If the node is nil, or info is nil, we can't inspect for type information.
	// This ensures we don't panic on a nil node or info.
	if node == nil || info == nil {
		return chunkCode // Return original chunk if inspection is not possible
	}

	// Map to store identified replacements: alias -> fullImportPath
	replacements := make(map[string]string)

	// First pass (AST Inspection): Inspect the AST to find all package alias usages (SelectorExpr.X)
	// and map them to their full import paths.
	ast.Inspect(node, func(innerNode ast.Node) bool {
		if selExpr, ok := innerNode.(*ast.SelectorExpr); ok {
			// Check if the selector's X (e.g., "cconfig" in cconfig.Default()) is an identifier
			if ident, isIdent := selExpr.X.(*ast.Ident); isIdent {
				// Get the type information for the identifier
				obj := info.Uses[ident]
				if obj == nil {
					return true // Skip if no type info (e.g., undeclared or built-in)
				}
				// Check if the object is a package name
				if pkgName, isPkgName := obj.(*types.PkgName); isPkgName {
					fullImportPath := pkgName.Imported().Path()
					// Only add to replacements if the alias is different from the full path
					// (i.e., it's an actual alias or an implicit alias that needs expansion)
					if ident.Name != fullImportPath {
						replacements[ident.Name] = fullImportPath
					}
				}
			}
		}
		return true // Continue inspecting the subtree
	})

	// If no replacements are found, return the original chunk code
	if len(replacements) == 0 {
		return chunkCode
	}

	// --- Two-pass replacement strategy to prevent cascading issues ---

	// Pass 1 Setup: Create unique temporary placeholders for each alias
	tempMap := make(map[string]string)  // oldQualifier -> tempPlaceholder
	finalMap := make(map[string]string) // tempPlaceholder -> fullPath (for the second pass)

	placeholderPrefix := "__GO_QUALIFIER_TEMP_" // A unique prefix for placeholders
	i := 0
	for oldQualifier, fullPath := range replacements {
		// Generate a unique placeholder for each alias using a counter
		placeholder := placeholderPrefix + strconv.Itoa(i) + "__"
		tempMap[oldQualifier] = placeholder
		finalMap[placeholder] = fullPath // Store the final mapping for the second pass
		i++
	}

	// Sort the original qualifiers by length in descending order.
	// This is important for the first pass to handle cases where one alias
	// might be a prefix of another (e.g., "log" and "logrus").
	var sortedOldQualifiers []string
	for q := range tempMap {
		sortedOldQualifiers = append(sortedOldQualifiers, q)
	}
	sort.Slice(sortedOldQualifiers, func(i, j int) bool {
		return len(sortedOldQualifiers[i]) > len(sortedOldQualifiers[j])
	})

	// Execute the first pass: Replace actual aliases with unique placeholders
	for _, oldQualifier := range sortedOldQualifiers {
		placeholder := tempMap[oldQualifier]
		// Replace `alias.` with `placeholder.`
		// This assumes the alias is always followed by a dot for a SelectorExpr.
		chunkCode = strings.ReplaceAll(chunkCode, oldQualifier+".", placeholder+".")
	}

	// Pass 2 Setup: Prepare placeholders for the final replacement
	// Sorting placeholders by length descending is also a good practice,
	// though less critical if placeholders are guaranteed not to contain each other.
	var sortedPlaceholders []string
	for p := range finalMap {
		sortedPlaceholders = append(sortedPlaceholders, p)
	}
	sort.Slice(sortedPlaceholders, func(i, j int) bool {
		return len(sortedPlaceholders[i]) > len(sortedPlaceholders[j])
	})

	// Execute the second pass: Replace the unique placeholders with their full import paths
	for _, placeholder := range sortedPlaceholders {
		fullPath := finalMap[placeholder]
		// Replace `placeholder.` with `fullPath.`
		chunkCode = strings.ReplaceAll(chunkCode, placeholder+".", fullPath+".")
	}

	return chunkCode
}

/*
// applyQualifierReplacements inspects the given node's subtree for SelectorExprs
// and replaces package qualifiers with their full import paths in the chunkCode string.
func applyQualifierReplacements(chunkCode string, node ast.Node, info *types.Info) string {
	// If the node is nil, or info is nil, we can't inspect for type information.
	// This ensures we don't panic on a nil node or info.
	if node == nil || info == nil {
		return chunkCode // Return original chunk if inspection is not possible
	}

	replacements := make(map[string]string)

	ast.Inspect(node, func(innerNode ast.Node) bool {
		if selExpr, ok := innerNode.(*ast.SelectorExpr); ok {
			if ident, isIdent := selExpr.X.(*ast.Ident); isIdent {
				obj := info.Uses[ident]
				if obj == nil {
					return true
				}
				if pkgName, isPkgName := obj.(*types.PkgName); isPkgName {
					fullImportPath := pkgName.Imported().Path()
					if ident.Name != fullImportPath {
						replacements[ident.Name] = fullImportPath
					}
				}
			}
		}
		return true
	})

	var sortedOldQualifiers []string
	for oldQualifier := range replacements {
		sortedOldQualifiers = append(sortedOldQualifiers, oldQualifier)
	}
	sort.Slice(sortedOldQualifiers, func(i, j int) bool {
		return len(sortedOldQualifiers[i]) > len(sortedOldQualifiers[j])
	})

	for _, oldQualifier := range sortedOldQualifiers {
		fullPath := replacements[oldQualifier]
		chunkCode = strings.ReplaceAll(chunkCode, oldQualifier+".", fullPath+".")
	}
	return chunkCode
}*/

// getTypeString helper: This function now prioritizes using types.Info for accurate type names.
func getTypeString(expr ast.Expr, info *types.Info) string {
	if tv := info.TypeOf(expr); tv != nil {
		return tv.String()
	}

	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + getTypeString(t.X, info)
	case *ast.ArrayType:
		return "[]" + getTypeString(t.Elt, info)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", getTypeString(t.Key, info), getTypeString(t.Value, info))
	case *ast.SelectorExpr:
		if ident, isIdent := t.X.(*ast.Ident); isIdent {
			if obj := info.Uses[ident]; obj != nil {
				if pkgName, isPkgName := obj.(*types.PkgName); isPkgName {
					return pkgName.Imported().Path() + "." + t.Sel.Name
				}
			}
		}
		return fmt.Sprintf("%s.%s", getTypeString(t.X, info), t.Sel.Name)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.ChanType:
		dir := ""
		switch t.Dir {
		case ast.SEND:
			dir = "chan<- "
		case ast.RECV:
			dir = "<-chan "
		default:
			dir = "chan "
		}
		return dir + getTypeString(t.Value, info)
	case *ast.Ellipsis:
		return "..." + getTypeString(t.Elt, info)
	case *ast.FuncType:
		return "func" + getSignature(t, info)
	default:
		tmpFset := token.NewFileSet()
		var b bytes.Buffer
		if err := printer.Fprint(&b, tmpFset, expr); err == nil {
			return b.String()
		}
		return fmt.Sprintf("%T", expr)
	}
}

func getSignature(ft *ast.FuncType, info *types.Info) string {
	var params []string
	if ft.Params != nil {
		for _, field := range ft.Params.List {
			typeStr := getTypeString(field.Type, info)
			if len(field.Names) == 0 {
				params = append(params, typeStr)
			} else {
				for _, name := range field.Names {
					params = append(params, name.Name+" "+typeStr)
				}
			}
		}
	}
	paramStr := "(" + strings.Join(params, ", ") + ")"

	var results []string
	if ft.Results != nil {
		for _, field := range ft.Results.List {
			typeStr := getTypeString(field.Type, info)
			if len(field.Names) == 0 {
				results = append(results, typeStr)
			} else {
				for _, name := range field.Names {
					results = append(results, name.Name+" "+typeStr)
				}
			}
		}
	}
	resultStr := ""
	if len(results) > 0 {
		if len(results) == 1 && ft.Results.List[0].Names == nil {
			resultStr = " " + results[0]
		} else {
			resultStr = " (" + strings.Join(results, ", ") + ")"
		}
	}

	return paramStr + resultStr
}

// This function is no longer used for AST modification affecting qualifiers.
func replaceImportAliases(pkg *packages.Package, file *ast.File) *ast.File {
	return file // No AST modification done here.
}
