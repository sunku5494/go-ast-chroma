package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/printer" // Still needed for getTypeString/getSignature's fallback via printer
	"go/token"
	"go/types" // Crucial for type information
	"io/ioutil"
	"log"
	"sort"   // For sorting replacements
	"strings"

	"golang.org/x/tools/go/packages" // For loading Go packages
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
	projectPath := "/home/vsunku/DEV/sdn"

	chunks, err := processGoProject(projectPath)
	if err != nil {
		log.Fatalf("Error processing Go project: %v", err)
	}

	outputFileName := "code_chunks_rewritten.json"
	// Corrected indent to "  " (two spaces)
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
	// Use a single FileSet for the entire project loading (this is correct)
	fset := token.NewFileSet()

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedExportsFile |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
		Fset:  fset,        // Associate the FileSet with the packages config
		Dir:   projectPath, // Start loading from the specified project directory
		Tests: false,       // Exclude test files
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
			filePath := fset.File(file.Pos()).Name() // Get the original file path from the global fset

			// --- CRITICAL CHANGE: Read the original file content bytes directly ---
			originalFileBytes, err := ioutil.ReadFile(filePath)
			if err != nil {
				log.Printf("Error reading file %s: %v", filePath, err)
				continue
			}
			// `originalFileContentString` is no longer needed to get bytes for slicing.
			// The `fset` and `file.Pos()/End()` refer to offsets within `originalFileBytes`.
			// So, len(originalFileBytes) is the correct length to compare against.

			packageName := pkg.Name

			ast.Inspect(file, func(node ast.Node) bool {
				if funcDecl, isFuncDecl := node.(*ast.FuncDecl); isFuncDecl {
					startPos := fset.Position(funcDecl.Pos())
					endPos := fset.Position(funcDecl.End())

					startOffset := startPos.Offset
					endOffset := endPos.Offset

					// --- CRITICAL CHANGE: Compare against len(originalFileBytes) ---
					if startOffset < 0 || endOffset > len(originalFileBytes) || startOffset > endOffset {
						log.Printf("Warning: Invalid offsets for function %s in %s: start=%d, end=%d, file_len=%d. Skipping chunk.",
							funcDecl.Name.Name, filePath, startOffset, endOffset, len(originalFileBytes)) // Use originalFileBytes length
						return true
					}
					// --- CRITICAL CHANGE: Slice originalFileBytes directly ---
					chunkCode := string(originalFileBytes[startOffset:endOffset])


					replacements := make(map[string]string)
					ast.Inspect(funcDecl, func(innerNode ast.Node) bool {
						if selExpr, ok := innerNode.(*ast.SelectorExpr); ok {
							if ident, isIdent := selExpr.X.(*ast.Ident); isIdent {
								obj := pkg.TypesInfo.Uses[ident]
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

					metadata := map[string]interface{}{
						"file_path":    filePath,
						"package_name": packageName,
						"entity_type":  "function",
						"entity_name":  funcDecl.Name.Name,
						"start_line":   startPos.Line,
						"end_line":     endPos.Line,
						"signature":    getSignature(funcDecl.Type, pkg.TypesInfo),
					}

					if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
						metadata["entity_type"] = "method"
						receiverType := getTypeString(funcDecl.Recv.List[0].Type, pkg.TypesInfo)
						metadata["receiver_type"] = receiverType
						metadata["entity_name"] = receiverType + "." + funcDecl.Name.Name
					}

					chunks = append(chunks, ChromaDocument{
						ID:       fmt.Sprintf("%s:%d-%d", filePath, startPos.Line, endPos.Line),
						Document: chunkCode,
						Metadata: metadata,
					})
				}
				return true
			})
		}
	}

	return chunks, nil
}

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
		// Use printer.Fprint as a final fallback for complex AST types that getTypeString doesn't explicitly handle.
		// This requires a temporary FileSet for the single expression.
		tmpFset := token.NewFileSet()
		var b bytes.Buffer
		if err := printer.Fprint(&b, tmpFset, expr); err == nil {
			return b.String()
		}
		return fmt.Sprintf("%T", expr) // Fallback to type name if printing fails
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
