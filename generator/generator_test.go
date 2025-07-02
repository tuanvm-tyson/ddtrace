package generator

import (
	"bytes"
	"go/ast"
	"go/token"
	"io"
	"testing"
	"text/template"
	"time"

	"github.com/gojuno/minimock/v3"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/tyson-tuanvm/ddtrace/pkg"
)

func Test_unquote(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		want1 string
	}{
		{
			name:  "unquoted string",
			s:     "abcde",
			want1: "abcde",
		},
		{
			name:  "left quote only",
			s:     `"abcde`,
			want1: "abcde",
		},
		{
			name:  "right quote only",
			s:     `abcde"`,
			want1: "abcde",
		},
		{
			name:  "left and right quotes",
			s:     `"abcde"`,
			want1: "abcde",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1 := unquote(tt.s)
			assert.Equal(t, tt.want1, got1, "unquote returned unexpected result")
		})
	}
}

func Test_findImportPathForName(t *testing.T) {
	type args struct {
		name    string
		imports []*ast.ImportSpec
		cp      *packages.Package
	}
	tests := []struct {
		name string
		args args

		want    string
		wantErr error
	}{
		{
			name: "path from import name",
			args: args{
				name:    "pkg",
				imports: []*ast.ImportSpec{{Name: &ast.Ident{Name: "pkg"}, Path: &ast.BasicLit{Value: "domain/pkgname"}}},
			},
			want: "domain/pkgname",
		},
		{
			name: "path from package imports",
			args: args{
				name: "pkg",
				cp: &packages.Package{
					Imports: map[string]*packages.Package{
						"domain/pkgname": {
							Name: "pkg",
						},
					},
				},
			},
			want: "domain/pkgname",
		},
		{
			name: "not found",
			args: args{
				name: "pkg",
				cp:   &packages.Package{},
			},
			wantErr: errors.Wrapf(errUnknownSelector, "pkg"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := findImportPathForName(tt.args.name, tt.args.imports, tt.args.cp)
			if tt.wantErr != nil {
				assert.EqualError(t, err, tt.wantErr.Error())
			} else {
				assert.Equal(t, tt.want, path)
			}
		})
	}
}

func Test_processIdent(t *testing.T) {
	type args struct {
		i                   *ast.Ident
		input               targetProcessInput
		toCheckForInterface bool
	}
	tests := []struct {
		name string
		args args

		want1      methodsList
		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name: "not an interface",
			args: args{
				i: &ast.Ident{Name: "name"},
				input: targetProcessInput{
					types: []*ast.TypeSpec{{Name: &ast.Ident{Name: "name"}, Type: &ast.StructType{}}},
				},
				toCheckForInterface: true,
			},
			wantErr: true,
			inspectErr: func(err error, t *testing.T) {
				assert.Equal(t, errNotAnInterface, errors.Cause(err))
			},
		},
		{
			name: "not an interface but no need to check",
			args: args{
				i: &ast.Ident{Name: "name"},
				input: targetProcessInput{
					types: []*ast.TypeSpec{{Name: &ast.Ident{Name: "name"}, Type: &ast.StructType{}}},
				},
				toCheckForInterface: false,
			},
			wantErr: false,
		},
		{
			name: "embedded interface found",
			args: args{
				i: &ast.Ident{Name: "name"},
				input: targetProcessInput{
					types: []*ast.TypeSpec{{Name: &ast.Ident{Name: "name"}, Type: &ast.InterfaceType{}}},
				},
				toCheckForInterface: true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := minimock.NewController(t)
			defer mc.Wait(time.Second)

			got1, err := processIdent(tt.args.i, tt.args.input, tt.args.toCheckForInterface)

			assert.Equal(t, tt.want1, got1, "processIdent returned unexpected result")

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func Test_mergeMethods(t *testing.T) {
	type args struct {
		ml1 methodsList
		ml2 methodsList
	}
	tests := []struct {
		name string

		args args

		want1      methodsList
		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name:    "nil method",
			wantErr: false,
		},
		{
			name: "duplicate methods should return outer method",
			args: args{
				ml1: methodsList{
					"method": Method{
						Doc: []string{"outer"},
					},
				},
				ml2: methodsList{
					"method": Method{
						Doc: []string{"inner"},
					},
				},
			},
			wantErr: false,
			want1: methodsList{
				"method": {
					Doc: []string{"outer"},
				},
			},
		},
		{
			name: "success",
			args: args{
				ml1: methodsList{
					"method1": Method{},
				},
				ml2: methodsList{
					"method2": Method{},
				},
			},
			want1: methodsList{
				"method1": Method{},
				"method2": Method{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1, err := mergeMethods(tt.args.ml1, tt.args.ml2)

			assert.Equal(t, tt.want1, got1, "mergeMethods returned unexpected result")

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func Test_processSelector(t *testing.T) {
	type args struct {
		se    *ast.SelectorExpr
		input targetProcessInput
	}
	tests := []struct {
		name string
		args args

		want1      methodsList
		wantErr    bool
		inspectErr func(err error, t *testing.T)
	}{
		{
			name: "import with name not found",
			args: args{
				se: &ast.SelectorExpr{X: &ast.Ident{Name: "unknown"}, Sel: &ast.Ident{Name: "unknown"}},
				input: targetProcessInput{
					processInput: processInput{
						currentPackage: &packages.Package{Imports: nil},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "import not found",
			args: args{
				se: &ast.SelectorExpr{X: &ast.Ident{Name: "unknownpackage"}, Sel: &ast.Ident{Name: "Unknown"}},
				input: targetProcessInput{
					processInput: processInput{
						currentPackage: &packages.Package{Imports: nil},
					},
					imports: []*ast.ImportSpec{{Path: &ast.BasicLit{Value: "unknown_path"}}},
				},
			},
			wantErr: true,
		},
		{
			name: "import failed",
			args: args{
				se: &ast.SelectorExpr{X: &ast.Ident{Name: "io"}, Sel: &ast.Ident{Name: "UnknownInterface"}},
				input: targetProcessInput{
					processInput: processInput{
						fileSet: token.NewFileSet(),
						currentPackage: &packages.Package{Imports: map[string]*packages.Package{
							"io": {},
						}},
					},
					imports: []*ast.ImportSpec{{Path: &ast.BasicLit{Value: "io"}}},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1, err := processSelector(tt.args.se, tt.args.input)

			assert.Equal(t, tt.want1, got1, "processSelector returned unexpected result")

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func Test_processInterface(t *testing.T) {
	type args struct {
		it          *ast.InterfaceType
		targetInput targetProcessInput
	}
	tests := []struct {
		name string
		args args

		want1      methodsList
		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name: "func type",
			args: args{
				it: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{{Name: "methodName"}}, Type: &ast.FuncType{Params: &ast.FieldList{}}}}}},
				targetInput: targetProcessInput{
					processInput: processInput{
						fileSet: token.NewFileSet(),
					},
				},
			},
			want1:   methodsList{"methodName": Method{Name: "methodName", Params: []Param{}}},
			wantErr: false,
		},
		{
			name: "selector expression",
			args: args{
				it: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{
					{
						Names: []*ast.Ident{{Name: "methodName"}},
						Type:  &ast.SelectorExpr{X: &ast.Ident{Name: "unknown"}, Sel: &ast.Ident{Name: "Interface"}},
					},
				}}},
				targetInput: targetProcessInput{
					processInput: processInput{
						fileSet:        token.NewFileSet(),
						currentPackage: &packages.Package{Imports: nil},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "identifier with embedded methods",
			args: args{
				it: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{
					{
						Type: &ast.Ident{Name: "Embedded"},
					},
				}}},
				targetInput: targetProcessInput{
					processInput: processInput{
						fileSet: token.NewFileSet(),
					},
					types: []*ast.TypeSpec{{Name: &ast.Ident{Name: "Embedded"}, Type: &ast.InterfaceType{}}},
				},
			},
			want1:   methodsList{},
			wantErr: false,
		},
		{
			name: "index expression with identifier",
			args: args{
				it: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{
					{
						Type: &ast.IndexExpr{X: &ast.Ident{Name: "Embedded"}, Index: &ast.Ident{Name: "Embedded_2"}},
					},
				}}},
				targetInput: targetProcessInput{
					processInput: processInput{
						fileSet: token.NewFileSet(),
					},
					types: []*ast.TypeSpec{{Name: &ast.Ident{Name: "Embedded"}, Type: &ast.InterfaceType{}}},
				},
			},
			want1:   methodsList{},
			wantErr: false,
		},
		{
			name: "index list expression with identifier",
			args: args{
				it: &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{
					{
						Type: &ast.IndexListExpr{X: &ast.Ident{Name: "Embedded"}, Indices: []ast.Expr{
							&ast.Ident{Name: "Embedded_2"},
						}},
					},
				}}},
				targetInput: targetProcessInput{
					processInput: processInput{
						fileSet: token.NewFileSet(),
					},
					types: []*ast.TypeSpec{{Name: &ast.Ident{Name: "Embedded"}, Type: &ast.InterfaceType{}}},
				},
			},
			want1:   methodsList{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1, err := processInterface(tt.args.it, tt.args.targetInput)

			assert.Equal(t, tt.want1, got1, "processInterface returned unexpected result")

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func Test_typeSpecs(t *testing.T) {
	expected := []*ast.TypeSpec{{
		Name: &ast.Ident{Name: "Interface"},
		Type: &ast.InterfaceType{},
	}}

	f := &ast.File{Decls: []ast.Decl{&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{expected[0]}}}}

	specs := typeSpecs(f)

	assert.Equal(t, expected, specs, "typeSpecs returned unexpected result")
}

func Test_findTarget(t *testing.T) {
	type args struct {
		input processInput
	}
	tests := []struct {
		name string
		args args

		want1      methodsList
		wantErr    bool
		inspectErr func(err error, t *testing.T)
	}{
		{
			name: "not found",
			args: args{
				input: processInput{
					astPackage: &pkg.Package{},
				},
			},
			wantErr: true,
			inspectErr: func(err error, t *testing.T) {
				assert.Equal(t, errTargetNotFound, errors.Cause(err))
			},
		},
		{
			name: "found",
			args: args{
				input: processInput{
					astPackage: &pkg.Package{Files: map[string]*ast.File{
						"file.go": {
							Decls: []ast.Decl{&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
								Name: &ast.Ident{Name: "Interface"},
								Type: &ast.InterfaceType{},
							}}}},
						}}},
					targetName: "Interface",
				},
			},
			wantErr: false,
		},
		{
			name: "found interface alias",
			args: args{
				input: processInput{
					astPackage: &pkg.Package{
						Files: map[string]*ast.File{
							"file.go": {
								Decls: []ast.Decl{&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
									Name: &ast.Ident{Name: "InterfaceAlias"},
									Type: &ast.Ident{
										Name: "Interface",
										//nolint:staticcheck // SA1019, this is an internal object that is still used by ast library.
										Obj: &ast.Object{
											Decl: &ast.TypeSpec{
												Type: &ast.InterfaceType{},
											},
										},
									},
								}}}},
							}},
					},
					targetName: "InterfaceAlias",
				},
			},
			wantErr: false,
		},
		{
			name: "found interface alias on exported type",
			args: args{
				input: processInput{
					astPackage: &pkg.Package{
						Files: map[string]*ast.File{
							"file.go": {
								Decls: []ast.Decl{&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
									Name: &ast.Ident{Name: "InterfaceAlias"},
									Type: &ast.SelectorExpr{
										X: &ast.Ident{
											Name: "io",
										},
										Sel: &ast.Ident{
											Name: "Reader",
										},
									},
								}}}},
								Imports: []*ast.ImportSpec{{
									Path: &ast.BasicLit{
										Value: "io",
									},
								}},
							}},
					},
					targetName: "InterfaceAlias",
				},
			},
			want1: methodsList{
				"Read": Method{
					Name: "Read",
					Params: ParamsSlice{
						{
							Name: "p",
							Type: "[]byte",
						},
					},
					Results: ParamsSlice{
						{
							Name: "n",
							Type: "int",
						},
						{
							Name: "err",
							Type: "error",
						},
					},
					ReturnsError: true,
				},
			},
			wantErr: false,
		},
		{
			name: "found interface alias on exported type with named package",
			args: args{
				input: processInput{
					astPackage: &pkg.Package{
						Files: map[string]*ast.File{
							"file.go": {
								Decls: []ast.Decl{&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
									Name: &ast.Ident{Name: "InterfaceAlias"},
									Type: &ast.SelectorExpr{
										X: &ast.Ident{
											Name: "io_name",
										},
										Sel: &ast.Ident{
											Name: "Reader",
										},
									},
								}}}},
								Imports: []*ast.ImportSpec{
									{
										Name: &ast.Ident{
											Name: "io_name",
										},
										Path: &ast.BasicLit{
											Value: "io",
										},
									}},
							}},
					},
					targetName: "InterfaceAlias",
				},
			},
			want1: methodsList{
				"Read": Method{
					Name: "Read",
					Params: ParamsSlice{
						{
							Name: "p",
							Type: "[]byte",
						},
					},
					Results: ParamsSlice{
						{
							Name: "n",
							Type: "int",
						},
						{
							Name: "err",
							Type: "error",
						},
					},
					ReturnsError: true,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := minimock.NewController(t)
			defer mc.Wait(time.Second)

			got1, err := findTarget(tt.args.input)

			assert.Equal(t, tt.want1, got1.methods, "findInterface returned unexpected result")

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestGenerator_Generate(t *testing.T) {
	type args struct {
		w io.Writer
	}
	tests := []struct {
		name    string
		init    func(t minimock.Tester) Generator
		inspect func(r Generator, w io.Writer, t *testing.T) //inspects Generator after execution of Generate

		args func(t minimock.Tester) args

		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name: "header template error",
			init: func(t minimock.Tester) Generator {
				return Generator{
					headerTemplate: template.Must(template.New("header").Funcs(template.FuncMap{
						"makeError": func() (string, error) { return "", errors.New("template error") },
					}).Parse("{{makeError}}")),
				}
			},
			args: func(t minimock.Tester) args {
				return args{}
			},
			wantErr: true,
		},
		{
			name: "body template error",
			init: func(t minimock.Tester) Generator {
				return Generator{
					headerTemplate: template.Must(template.New("header").Parse("")),
					bodyTemplate: template.Must(template.New("body").Funcs(template.FuncMap{
						"makeError": func() (string, error) { return "", errors.New("template error") },
					}).Parse("{{makeError}}")),
				}
			},
			args: func(t minimock.Tester) args {
				return args{}
			},
			wantErr: true,
		},
		{
			name: "bad generated code",
			init: func(t minimock.Tester) Generator {
				return Generator{
					headerTemplate: template.Must(template.New("header").Parse("not a go code")),
					bodyTemplate:   template.Must(template.New("body").Parse("")),
				}
			},
			args: func(t minimock.Tester) args {
				return args{}
			},
			wantErr: true,
			inspectErr: func(err error, t *testing.T) {
				assert.Contains(t, err.Error(), "failed to format")
			},
		},
		{
			name: "success",
			init: func(t minimock.Tester) Generator {
				return Generator{
					headerTemplate: template.Must(template.New("header").Parse("package success")),
					bodyTemplate:   template.Must(template.New("body").Parse("")),
				}
			},
			args: func(t minimock.Tester) args {
				return args{
					w: bytes.NewBuffer([]byte{}),
				}
			},
			wantErr: false,
		},
		{
			name: "imports can be generated",
			init: func(t minimock.Tester) Generator {
				return Generator{
					Options: Options{
						Imports: []string{`"github.com/pkg/errors"`, `"github.com/sirupsen/logrus"`},
					},
					headerTemplate: template.Must(template.New("header").Parse("package success\n")),
					bodyTemplate: template.Must(template.New("body").Parse(`
						{{.Import "github.com/sirupsen/logrus" }}
						func test(l *logrus.Logger) {}
						`)),
				}
			},
			args: func(t minimock.Tester) args {
				return args{
					w: bytes.NewBuffer([]byte{}),
				}
			},
			inspect: func(_ Generator, w io.Writer, t *testing.T) {
				assert.Equal(t, `package success

import (
	"github.com/sirupsen/logrus"
)

func test(l *logrus.Logger) {}
`, w.(*bytes.Buffer).String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := minimock.NewController(t)
			defer mc.Finish()

			tArgs := tt.args(mc)
			receiver := tt.init(mc)

			err := receiver.Generate(tArgs.w)

			if tt.inspect != nil {
				tt.inspect(receiver, tArgs.w, t)
			}

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func TestNewGenerator(t *testing.T) {
	tests := []struct {
		name    string
		options func(t minimock.Tester) Options

		want1      *Generator
		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name: "bad header template",
			options: func(t minimock.Tester) Options {
				return Options{
					HeaderTemplate: "{{.",
				}
			},
			wantErr: true,
		},
		{
			name: "bad body template",
			options: func(t minimock.Tester) Options {
				return Options{
					HeaderTemplate: "",
					BodyTemplate:   "{{.",
				}
			},
			wantErr: true,
		},
		{
			name: "failed to load source package",
			options: func(t minimock.Tester) Options {
				return Options{
					HeaderTemplate: "",
					BodyTemplate:   "",
					SourcePackage:  "not-exist",
				}
			},
			wantErr: true,
		},
		{
			name: "failed to load destination package",
			options: func(t minimock.Tester) Options {
				return Options{
					HeaderTemplate: "",
					BodyTemplate:   "",
					SourcePackage:  "./",
					OutputFile:     "not-exist/out.go",
				}
			},
			wantErr: true,
		},
		{
			name: "failed to find interface",
			options: func(t minimock.Tester) Options {
				return Options{
					HeaderTemplate: "",
					BodyTemplate:   "",
					SourcePackage:  "./",
					OutputFile:     "./out.go",
					InterfaceName:  "NotExist",
				}
			},
			wantErr: true,
		},
		{
			name: "failed to find interface",
			options: func(t minimock.Tester) Options {
				return Options{
					HeaderTemplate: "",
					BodyTemplate:   "",
					SourcePackage:  "./",
					OutputFile:     "./out.go",
					InterfaceName:  "NotExist",
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := minimock.NewController(t)
			defer mc.Wait(time.Second)

			options := tt.options(mc)

			got1, err := NewGenerator(options)

			assert.Equal(t, tt.want1, got1, "NewGenerator returned unexpected result")

			if tt.wantErr {
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}

		})
	}

	t.Run("unexported interface", func(t *testing.T) {
		options := Options{
			HeaderTemplate: "",
			BodyTemplate:   "",
			SourcePackage:  "testing",
			OutputFile:     "./out.go",
			InterfaceName:  "TB",
		}

		g, err := NewGenerator(options)
		require.Error(t, err)
		assert.Equal(t, errUnexportedMethod, errors.Cause(err))
		assert.Nil(t, g)
	})

	t.Run("success", func(t *testing.T) {
		options := Options{
			HeaderTemplate: "",
			BodyTemplate:   "",
			SourcePackage:  "io",
			OutputFile:     "./out.go",
			InterfaceName:  "Closer",
		}

		g, err := NewGenerator(options)
		assert.NoError(t, err)
		assert.NotNil(t, g)
	})
}
