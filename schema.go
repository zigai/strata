package strata

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/zigai/strata/internal/defaulter"
)

// ErrReflectSchema is returned when a schema cannot be reflected from a Go type.
var ErrReflectSchema = errors.New("failed to reflect schema")

type schemaOptions struct {
	id       string
	title    string
	comments []schemaCommentSource
}

type schemaCommentSource struct {
	packagePath string
	directory   string
}

// SchemaOption configures [Schema].
type SchemaOption func(*schemaOptions)

// WithSchemaID sets the root $id of the generated schema.
//
// The value is normally the URL the schema is served from, which editors use to
// resolve references.
func WithSchemaID(id string) SchemaOption {
	return func(opts *schemaOptions) {
		opts.id = id
	}
}

// WithSchemaTitle sets the title of the generated schema, which schema-aware
// editors display.
func WithSchemaTitle(title string) SchemaOption {
	return func(opts *schemaOptions) {
		opts.title = title
	}
}

func WithSchemaComments(packagePath, directory string) SchemaOption {
	return func(opts *schemaOptions) {
		opts.comments = append(opts.comments, schemaCommentSource{packagePath: packagePath, directory: directory})
	}
}

// Schema returns the JSON Schema for T as indented JSON.
//
// Field names use snake_case Go names; field doc comments become descriptions.
// Sources are located in the caller's module. With -trimpath, the working
// directory must be inside that module. WithSchemaComments supplies sources for
// other locations or deployed binaries. Unavailable sources
// wrap [ErrReflectSchema]. No property is required, since a config file may set
// any subset of keys.
//
// [WithSchemaID] and [WithSchemaTitle] set root metadata. By default, $id comes
// from the type's package path.
//
// Unsupported types and reflector panics wrap [ErrReflectSchema].
func Schema[T any](opts ...SchemaOption) ([]byte, error) {
	options := &schemaOptions{
		id:       "",
		title:    "",
		comments: nil,
	}
	for _, opt := range opts {
		opt(options)
	}

	var target T

	typ := reflect.TypeOf(target)
	if typ == nil {
		return nil, fmt.Errorf("%w for %T: no concrete type to reflect", ErrReflectSchema, target)
	}

	caller, sourceFile, _, _ := runtime.Caller(1)
	callerPackage := ""

	if function := runtime.FuncForPC(caller); function != nil {
		name := function.Name()
		if separator := strings.IndexByte(name[strings.LastIndexByte(name, '/')+1:], '.'); separator >= 0 {
			callerPackage = name[:strings.LastIndexByte(name, '/')+1+separator]
		}
	}

	s, err := reflectSchema(typ, sourceFile, callerPackage, options.comments)
	if err != nil {
		return nil, err
	}

	if options.id != "" {
		s.ID = jsonschema.ID(options.id)
	}

	if options.title != "" {
		s.Title = options.title
	}

	data, err := json.Marshal(s, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("marshal json schema: %w", err)
	}

	return data, nil
}

func reflectSchema(typ reflect.Type, sourceFile, callerPackage string, sources []schemaCommentSource) (*jsonschema.Schema, error) {
	reflector := &jsonschema.Reflector{
		ExpandedStruct: true,
		KeyNamer:       defaulter.ToSnakeCase,
		// Every key is optional in a config file, so none is required unless its
		// field says so with a jsonschema:"required" tag.
		RequiredFromJSONSchemaTags: true,
	}

	if len(sources) == 0 {
		var err error

		sources, err = schemaCommentSources(typ, sourceFile, callerPackage)
		if err != nil {
			return nil, fmt.Errorf("%w: locate Go comments: %w", ErrReflectSchema, err)
		}
	}

	reflector.CommentMap = make(map[string]string)
	for _, source := range sources {
		if err := addSchemaComments(reflector.CommentMap, source); err != nil {
			return nil, fmt.Errorf("%w: read Go comments in %s: %w", ErrReflectSchema, source.directory, err)
		}
	}

	var (
		schema   *jsonschema.Schema
		panicErr error
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr = fmt.Errorf("%w for %s: %v", ErrReflectSchema, typ, r)
			}
		}()

		schema = reflector.ReflectFromType(typ)
	}()

	if panicErr != nil {
		return nil, panicErr
	}

	if schema == nil {
		return nil, fmt.Errorf("%w for %s", ErrReflectSchema, typ)
	}

	return schema, nil
}

func schemaCommentSources(typ reflect.Type, sourceFile, callerPackage string) ([]schemaCommentSource, error) {
	packages := make(map[string]bool)
	collectSchemaPackages(typ, make(map[reflect.Type]bool), packages)

	if len(packages) == 0 {
		return nil, nil
	}

	if !filepath.IsAbs(sourceFile) {
		resolved, err := trimmedSourcePath(sourceFile)
		if err != nil {
			return nil, err
		}

		sourceFile = resolved
	}

	callerDirectory := filepath.Dir(sourceFile)

	module, err := schemaCallerModule(callerDirectory, callerPackage)
	if err != nil {
		return nil, err
	}

	var sources []schemaCommentSource

	for packagePath := range packages {
		directory, err := schemaPackageDirectory(packagePath, callerDirectory, callerPackage, module)
		if err != nil {
			return nil, err
		}

		sources = append(sources, schemaCommentSource{packagePath: packagePath, directory: directory})
	}

	return sources, nil
}

// trimmedSourcePath finds the file that a binary built with -trimpath names
// by its module path, such as "example.com/app/config.go", in the module that
// holds the working directory.
func trimmedSourcePath(sourceFile string) (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%w: caller source path %q is not absolute: %w", ErrReflectSchema, sourceFile, err)
	}

	for {
		modulePath, err := goModulePath(filepath.Join(directory, "go.mod"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: locate caller module: %w", ErrReflectSchema, err)
		}

		if relative, found := strings.CutPrefix(filepath.ToSlash(sourceFile), modulePath+"/"); err == nil && found {
			return filepath.Join(directory, filepath.FromSlash(relative)), nil
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("%w: caller source path %q is not in the module of the working directory; use WithSchemaComments", ErrReflectSchema, sourceFile)
		}

		directory = parent
	}
}

func goModulePath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	for line := range strings.Lines(string(data)) {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module"); found && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			name, _, _ := strings.Cut(strings.TrimSpace(rest), "//")

			return strings.Trim(strings.TrimSpace(name), `"`), nil
		}
	}

	return "", fmt.Errorf("%w: %s declares no module", fs.ErrNotExist, path)
}

func schemaCallerModule(callerDirectory, callerPackage string) (schemaCommentSource, error) {
	directory := callerDirectory
	for {
		_, err := os.Stat(filepath.Join(directory, "go.mod"))
		if err == nil {
			return schemaModuleFromCaller(directory, callerDirectory, callerPackage)
		}

		if !os.IsNotExist(err) {
			return schemaCommentSource{}, fmt.Errorf("locate caller module: %w", err)
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return schemaCommentSource{packagePath: "", directory: ""}, nil
		}

		directory = parent
	}
}

func schemaModuleFromCaller(directory, callerDirectory, callerPackage string) (schemaCommentSource, error) {
	modulePath := strings.TrimSuffix(callerPackage, "_test")
	if callerPackage == "main" {
		if info, ok := debug.ReadBuildInfo(); ok {
			modulePath = info.Main.Path
		}
	} else {
		relative, err := filepath.Rel(directory, callerDirectory)
		if err != nil {
			return schemaCommentSource{}, fmt.Errorf("locate caller package: %w", err)
		}

		if relative != "." {
			modulePath = strings.TrimSuffix(modulePath, "/"+filepath.ToSlash(relative))
		}
	}

	return schemaCommentSource{packagePath: modulePath, directory: directory}, nil
}

func collectSchemaPackages(typ reflect.Type, seen map[reflect.Type]bool, packages map[string]bool) {
	if seen[typ] {
		return
	}

	seen[typ] = true
	if typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		collectSchemaPackages(typ.Elem(), seen, packages)
		return
	}

	if !defaulter.IsNestedStructType(typ) {
		return
	}

	if typ.PkgPath() != "" {
		packages[typ.PkgPath()] = true
	}

	for field := range typ.Fields() {
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		schemaName, _, _ := strings.Cut(field.Tag.Get("jsonschema"), ",")

		if field.IsExported() && jsonName != "-" && schemaName != "-" {
			collectSchemaPackages(field.Type, seen, packages)
		}
	}
}

func schemaPackageDirectory(packagePath, callerDirectory, callerPackage string, module schemaCommentSource) (string, error) {
	importPath := strings.TrimSuffix(packagePath, "_test")
	switch {
	case packagePath == "main" || packagePath == callerPackage:
		return callerDirectory, nil
	case module.packagePath != "" && importPath == module.packagePath:
		return module.directory, nil
	case module.packagePath != "" && strings.HasPrefix(importPath, module.packagePath+"/"):
		return filepath.Join(module.directory, filepath.FromSlash(strings.TrimPrefix(importPath, module.packagePath+"/"))), nil
	default:
		return "", fmt.Errorf("%w: source for package %q is outside the caller module; use WithSchemaComments", ErrReflectSchema, packagePath)
	}
}

func addSchemaComments(comments map[string]string, source schemaCommentSource) error {
	directory, err := filepath.Abs(source.directory)
	if err != nil {
		return fmt.Errorf("resolve source directory: %w", err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read comment source directory: %w", err)
	}

	found := false

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		filename := filepath.Join(directory, entry.Name())

		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse comment source: %w", err)
		}

		if strings.HasSuffix(file.Name.Name, "_test") != strings.HasSuffix(source.packagePath, "_test") {
			continue
		}

		found = true

		addSchemaFileComments(comments, source.packagePath, file)
	}

	if !found {
		return fmt.Errorf("%w: no Go source for package %q", ErrReflectSchema, source.packagePath)
	}

	return nil
}

func addSchemaFileComments(comments map[string]string, packagePath string, file *ast.File) {
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}

		for _, specification := range group.Specs {
			typ, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}

			addSchemaTypeComments(comments, packagePath, group, typ)
		}
	}
}

func addSchemaTypeComments(comments map[string]string, packagePath string, group *ast.GenDecl, typ *ast.TypeSpec) {
	prefix := packagePath + "." + typ.Name.Name

	text := typ.Doc.Text()
	if text == "" {
		text = group.Doc.Text()
	}

	var documentation doc.Package

	comments[prefix] = documentation.Synopsis(text)

	structure, ok := typ.Type.(*ast.StructType)
	if !ok {
		return
	}

	for _, field := range structure.Fields.List {
		text := field.Doc.Text()
		if text == "" {
			text = field.Comment.Text()
		}

		for _, name := range field.Names {
			comments[prefix+"."+name.Name] = strings.TrimSpace(text)
		}
	}
}
